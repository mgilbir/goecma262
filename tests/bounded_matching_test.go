package ecma262_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/vm"
)

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
