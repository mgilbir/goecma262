package ecma262_test

import (
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/vm"
)

// longInputCases are patterns that match (or reject) long inputs in linear
// time. Before the explicit backtrack stack, each of these recursed once per
// input character and crashed the process with "fatal error: stack overflow"
// on a long enough input; the linear ones also exhausted the fixed step budget
// and reported a valid input as not matching.
func longInputCases(n int) []struct {
	pattern, flags, input string
	want                  bool
} {
	a := strings.Repeat("a", n)
	return []struct {
		pattern, flags, input string
		want                  bool
	}{
		{`^[a-z]+$`, "", a, true},
		{`^[a-z]+$`, "", a + "!", false},
		{`^(a)+$`, "", a, true},
		{`^(?:a|b)+$`, "", strings.Repeat("ab", n/2), true},
		{`^[a-z]*?$`, "", a, true},                  // lazy: one alternative per character
		{`^(?:a|ab)*c`, "", a, false},               // ambiguous: a real choice point per character
		{`^(?:a|ab)*$`, "", a, true},                // ...that succeeds
		{`^(\w+)=\1$`, "", a + "=" + a, true},       // backreference: captures in the memo key
		{`!(?<=^[a-z]*!)`, "", a + "!", true},       // right-to-left loop inside a lookbehind
		{`^(?:(?!b).)*$`, "", a, true},              // a lookahead evaluated per character
		{`^.*$`, "s", strings.Repeat("é", n), true}, // multi-byte runes
		{`^[a-z]+$`, "m", a + "\n" + a, true},
	}
}

// TestBounded_NoStackOverflowOnLongInput runs long-input matches on a goroutine
// whose stack is capped far below what one frame per character needs. A stack
// overflow is a fatal runtime error, not a panic, so a regression here kills
// the test binary rather than failing politely.
func TestBounded_NoStackOverflowOnLongInput(t *testing.T) {
	const n = 300_000
	const maxStack = 256 << 10 // 256 KiB: under 1 byte per input character
	old := debug.SetMaxStack(maxStack)
	defer debug.SetMaxStack(old)

	for _, tc := range longInputCases(n) {
		done := make(chan error, 1)
		go func() {
			re, err := ecma262.CompileFlags(tc.pattern, tc.flags)
			if err != nil {
				done <- err
				return
			}
			got, err := re.MatchStringErr(tc.input)
			if err == nil && got != tc.want {
				err = fmt.Errorf("got %v, want %v", got, tc.want)
			}
			done <- err
		}()
		if err := <-done; err != nil {
			t.Errorf("/%s/%s on %d bytes: %v", tc.pattern, tc.flags, len(tc.input), err)
		}
	}
}

// TestBounded_LinearPatternsWithinLinearBudget checks that the patterns above
// cost a small constant number of steps per input byte, by giving them a fixed
// budget of that size on a 1 MB input: a step count that grew faster than the
// input would exhaust it and surface ErrStepLimit.
func TestBounded_LinearPatternsWithinLinearBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("large inputs")
	}
	const n = 1_000_000
	for _, tc := range longInputCases(n) {
		re := ecma262.MustCompile(tc.pattern, mustFlags(t, tc.flags))
		re.SetMaxSteps(16 * len(tc.input))
		got, err := re.MatchStringErr(tc.input)
		if err != nil {
			t.Errorf("/%s/%s on %d bytes: %v", tc.pattern, tc.flags, len(tc.input), err)
			continue
		}
		if got != tc.want {
			t.Errorf("/%s/%s on %d bytes: got %v, want %v", tc.pattern, tc.flags, len(tc.input), got, tc.want)
		}
	}
}

// TestBounded_DefaultBudgetScalesWithInput: with the default budget, the
// boolean API answers correctly for valid inputs of any length (the audit's
// case: /^[a-z]+$/ used to reject a valid 600,000-character string).
func TestBounded_DefaultBudgetScalesWithInput(t *testing.T) {
	if testing.Short() {
		t.Skip("large inputs")
	}
	a := strings.Repeat("a", 1_000_000)
	for _, pattern := range []string{`^[a-z]+$`, `^(a)+$`, `^(?:a|b)+$`} {
		matched, err := ecma262.MatchString(pattern, flags.Flags(0), a)
		if err != nil || !matched {
			t.Errorf("MatchString(%q) on 1M a's = (%v, %v), want (true, nil)", pattern, matched, err)
		}
	}
}

// TestBounded_LinearTime is a coarse wall-clock guard with a generous bound:
// a 10 MB input must be answered in time proportional to its length. It takes
// well under a second; a per-character recursion or a quadratic path would
// not finish (or would crash) long before the bound.
func TestBounded_LinearTime(t *testing.T) {
	if testing.Short() {
		t.Skip("large inputs")
	}
	if raceEnabled {
		t.Skip("wall-clock guard; the race detector's slowdown is not a regression")
	}
	in := strings.Repeat("a", 10_000_000)
	re := ecma262.MustCompile(`^[a-z]+$`, flags.Flags(0))
	for _, tc := range []struct {
		input string
		want  bool
	}{{in, true}, {in + "!", false}} {
		start := time.Now()
		got, err := re.MatchStringErr(tc.input)
		elapsed := time.Since(start)
		if err != nil || got != tc.want {
			t.Fatalf("got (%v, %v), want (%v, nil)", got, err, tc.want)
		}
		if elapsed > 20*time.Second {
			t.Fatalf("10 MB match took %v", elapsed)
		}
	}
}

// budgetBuster exceeds the default budget quickly: backreferences keep every
// capture combination distinct, so the search is polynomial of high degree.
const budgetBuster = `^(a*)(a*)(a*)\1\2\3$`

var budgetBusterInput = strings.Repeat("a", 300)

// TestBounded_BudgetExceededIsDistinctError covers every entry point that can
// report an error: an exceeded budget must surface as ErrStepLimit (and as
// vm.ErrStepLimit, the same value), never as a plain "no match".
func TestBounded_BudgetExceededIsDistinctError(t *testing.T) {
	re := ecma262.MustCompile(budgetBuster, flags.Flags(0))
	in := budgetBusterInput

	check := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ecma262.ErrStepLimit) || !errors.Is(err, vm.ErrStepLimit) {
			t.Errorf("%s: got error %v, want ErrStepLimit", name, err)
		}
	}

	m, err := ecma262.MatchString(budgetBuster, flags.Flags(0), in)
	check("package MatchString", err)
	if m {
		t.Error("package MatchString reported a match alongside the error")
	}
	_, err = ecma262.Match(budgetBuster, flags.Flags(0), []byte(in))
	check("package Match", err)

	_, err = re.MatchStringErr(in)
	check("MatchStringErr", err)
	_, err = re.MatchErr([]byte(in))
	check("MatchErr", err)
	idx, err := re.FindStringIndexErr(in)
	check("FindStringIndexErr", err)
	if idx != nil {
		t.Error("FindStringIndexErr returned an index alongside the error")
	}
	idx, err = re.FindStringSubmatchIndexErr(in)
	check("FindStringSubmatchIndexErr", err)
	if idx != nil {
		t.Error("FindStringSubmatchIndexErr returned indices alongside the error")
	}
	all, err := re.FindAllStringSubmatchIndexErr(in, -1)
	check("FindAllStringSubmatchIndexErr", err)
	if all != nil {
		t.Error("FindAllStringSubmatchIndexErr returned matches alongside the error")
	}
	out, err := re.ReplaceAllStringErr(in, "x")
	check("ReplaceAllStringErr", err)
	if out != "" {
		t.Error("ReplaceAllStringErr returned text alongside the error")
	}
	parts, err := re.SplitErr(in, -1)
	check("SplitErr", err)
	if parts != nil {
		t.Error("SplitErr returned substrings alongside the error")
	}

	// The methods without an error result document an exceeded budget as no
	// match.
	if re.MatchString(in) || re.FindStringSubmatch(in) != nil || re.FindAllString(in, -1) != nil {
		t.Error("boolean/string methods should report an exceeded budget as no match")
	}
}

// Iterating Err forms must not return the matches found before the budget ran
// out: /a|<buster>/g finds the leading "a" matches cheaply, then exhausts the
// budget on the rest of the input.
func TestBounded_IteratingErrFormsDropPartialResults(t *testing.T) {
	re := ecma262.MustCompile(`x|`+budgetBuster[1:], flags.Global)
	in := "xx" + budgetBusterInput
	all := re.FindAllStringSubmatchIndex(in, -1)
	if len(all) != 2 {
		t.Fatalf("setup: FindAllStringSubmatchIndex = %v, want the two x matches then a budget stop", all)
	}
	if got, err := re.FindAllStringSubmatchIndexErr(in, -1); !errors.Is(err, ecma262.ErrStepLimit) || got != nil {
		t.Errorf("FindAllStringSubmatchIndexErr = (%v, %v), want (nil, ErrStepLimit)", got, err)
	}
	if got, err := re.ReplaceAllStringErr(in, "y"); !errors.Is(err, ecma262.ErrStepLimit) || got != "" {
		t.Errorf("ReplaceAllStringErr = (%q, %v), want (\"\", ErrStepLimit)", got, err)
	}
	if got, err := re.SplitErr(in, -1); !errors.Is(err, ecma262.ErrStepLimit) || got != nil {
		t.Errorf("SplitErr = (%q, %v), want (nil, ErrStepLimit)", got, err)
	}
}

// The new Err forms agree with their error-free counterparts when the budget
// is not exceeded.
func TestBounded_ErrFormsMatchPlainForms(t *testing.T) {
	re := ecma262.MustCompile(`(\d+)(?<unit>[a-z]*)`, flags.Global)
	in := "10kg, 5, 7m"
	idx, err := re.FindStringSubmatchIndexErr(in)
	if err != nil || fmt.Sprint(idx) != fmt.Sprint(re.FindStringSubmatchIndex(in)) {
		t.Errorf("FindStringSubmatchIndexErr = (%v, %v)", idx, err)
	}
	all, err := re.FindAllStringSubmatchIndexErr(in, -1)
	if err != nil || fmt.Sprint(all) != fmt.Sprint(re.FindAllStringSubmatchIndex(in, -1)) {
		t.Errorf("FindAllStringSubmatchIndexErr = (%v, %v)", all, err)
	}
	out, err := re.ReplaceAllStringErr(in, "[$<unit>$1]")
	if err != nil || out != re.ReplaceAllString(in, "[$<unit>$1]") {
		t.Errorf("ReplaceAllStringErr = (%q, %v)", out, err)
	}
	parts, err := re.SplitErr(in, -1)
	if err != nil || fmt.Sprint(parts) != fmt.Sprint(re.Split(in, -1)) {
		t.Errorf("SplitErr = (%q, %v)", parts, err)
	}
	if got, err := re.FindAllStringSubmatchIndexErr("none", -1); got != nil || err != nil {
		t.Errorf("FindAllStringSubmatchIndexErr on no match = (%v, %v), want (nil, nil)", got, err)
	}
}

func mustFlags(t *testing.T, s string) flags.Flags {
	t.Helper()
	f, err := flags.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
