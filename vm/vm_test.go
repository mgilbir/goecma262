package vm_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"

	ecma262 "github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/compiler"
	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// newVM compiles pattern (no flags) into a VM, for tests that need VM-level
// settings the Regexp API does not expose.
func newVM(t *testing.T, pattern string) *vm.VM {
	t.Helper()
	ast, err := parser.New(pattern, parser.Flags{AnnexB: true}).Parse()
	if err != nil {
		t.Fatal(err)
	}
	code, n, err := compiler.Compile(ast)
	if err != nil {
		t.Fatal(err)
	}
	return &vm.VM{Code: code, NumGroups: n}
}

// TestMemoryBoundedForLinearPatterns runs 1 MB inputs under a memory budget of
// one byte per input byte. A backtrack entry per character would need 24 times
// that; linear patterns hold none (an alternative that cannot start is never
// recorded, and a greedy single-rune loop keeps one entry for all its
// iterations), only failure-memo bits.
func TestMemoryBoundedForLinearPatterns(t *testing.T) {
	a := strings.Repeat("a", 1_000_000)
	for _, tc := range []struct {
		pattern, input string
		want           bool
	}{
		{`^[a-z]+$`, a, true},
		{`^[a-z]+$`, a + "!", false},
		{`^(a)+$`, a, true},
		{`^(?:a|b)+$`, a, true},
		{`^.*$`, a, true},
		{`^[a-z]*?$`, a, true},
		{`^[a-z]*\d`, a, false},
		{`[a-z]*!`, a + "!", true},
		{`^(\w+)=\1$`, a + "=" + a, true},
		// The exit can start at every position, so without the single-frame
		// greedy loop each give-back point would be a separate entry.
		{`^.*a`, a, true},
		{`^[a-z]*a$`, a, true},
		// A choice point stays live under a loop that rewrites a capture on
		// every iteration: each slot needs trailing only once per choice.
		{`^(?:a|a)(a)+$`, a, true},
	} {
		v := newVM(t, tc.pattern)
		v.MaxMemory = len(tc.input)
		got, _, _ := v.Match(tc.input, 0)
		if v.Err != nil || got != tc.want {
			t.Errorf("/%s/ on %d bytes: got (%v, %v), want (%v, nil)", tc.pattern, len(tc.input), got, v.Err, tc.want)
		}
	}
}

// TestMemoryBudgetExceeded: a pattern that genuinely needs a choice point per
// character exceeds a small memory budget, and that surfaces as an error that
// is ErrStepLimit under errors.Is (not as a no-match, and not as a crash).
func TestMemoryBudgetExceeded(t *testing.T) {
	v := newVM(t, `^(?:a|ab)*$`)
	v.MaxMemory = 1_000_000
	got, _, _ := v.Match(strings.Repeat("a", 1_000_000), 0)
	if got || !errors.Is(v.Err, vm.ErrStepLimit) || !strings.Contains(v.Err.Error(), "memory") {
		t.Fatalf("got (%v, %v), want (false, memory-limit ErrStepLimit)", got, v.Err)
	}
	// With the default budget the same match succeeds.
	v = newVM(t, `^(?:a|ab)*$`)
	if got, _, _ := v.Match(strings.Repeat("a", 1_000_000), 0); !got || v.Err != nil {
		t.Fatalf("default budget: got (%v, %v), want (true, nil)", got, v.Err)
	}
}

// TestStepLimitIsSentinel: exceeding MaxSteps yields ErrStepLimit itself.
func TestStepLimitIsSentinel(t *testing.T) {
	v := newVM(t, `^(a|a)*b`)
	v.MaxSteps = 50
	if got, _, _ := v.Match(strings.Repeat("a", 1000), 0); got || v.Err != vm.ErrStepLimit {
		t.Fatalf("got (%v, %v), want (false, ErrStepLimit)", got, v.Err)
	}
}

// TestMatchAtAfterSuccess: the failure memo may carry over between MatchAt
// calls only after failed attempts; states visited by a successful attempt
// have not failed.
func TestMatchAtAfterSuccess(t *testing.T) {
	v := newVM(t, `x?a*`)
	if ok, end, _ := v.MatchAt("aa", 0); !ok || end != 2 {
		t.Fatalf("MatchAt(0) = (%v, %d), want (true, 2)", ok, end)
	}
	if ok, end, _ := v.MatchAt("aa", 1); !ok || end != 2 {
		t.Fatalf("MatchAt(1) after a success = (%v, %d), want (true, 2)", ok, end)
	}
}

// TestProgramSharedAcrossGoroutines: a Regexp without g/y is safe for
// concurrent use, so its Program must be read-only after construction. Run
// with -race.
func TestProgramSharedAcrossGoroutines(t *testing.T) {
	re := ecma262.MustCompile(`^(?:(a)|b)*(?<=(\w))c?$`, flags.Flags(0))
	in := strings.Repeat("ab", 500)
	want := fmt.Sprint(re.FindStringSubmatchIndex(in))
	errs := make(chan string, 8)
	for g := 0; g < 8; g++ {
		go func() {
			for i := 0; i < 50; i++ {
				if got := fmt.Sprint(re.FindStringSubmatchIndex(in)); got != want {
					errs <- got
					return
				}
			}
			errs <- ""
		}()
	}
	for g := 0; g < 8; g++ {
		if got := <-errs; got != "" {
			t.Errorf("concurrent match = %s, want %s", got, want)
		}
	}
}

// pair holds the same pattern compiled with and without the optimisations.
type pair struct {
	opt, ref *ecma262.Regexp
	flags    string
}

func compilePair(pattern, fl string) (pair, error) {
	f, err := flags.Parse(fl)
	if err != nil {
		return pair{}, err
	}
	restore := vm.SetOptimize(false)
	ref, err := ecma262.Compile(pattern, f)
	restore()
	if err != nil {
		return pair{}, err
	}
	opt, err := ecma262.Compile(pattern, f)
	if err != nil {
		return pair{}, fmt.Errorf("compiles only without optimisation: %w", err)
	}
	// The reference explores every alternative (the old engine could spin in
	// an empty lazy loop until the budget ran out), so keep it cheap and skip
	// cases it cannot finish.
	ref.SetMaxSteps(200_000)
	return pair{opt, ref, fl}, nil
}

// compare checks that both engines produce identical results for input with
// the given lastIndex, returning false if the reference ran out of budget.
func (p pair) compare(t *testing.T, input string, lastIndex int) bool {
	t.Helper()
	refAll, err := p.ref.FindAllStringSubmatchIndexErr(input, -1)
	if err != nil {
		return false
	}
	optAll, err := p.opt.FindAllStringSubmatchIndexErr(input, -1)
	if err != nil {
		t.Errorf("/%s/%s on %q: optimised engine failed where the reference did not: %v", p.opt, p.flags, input, err)
		return true
	}
	if !reflect.DeepEqual(refAll, optAll) {
		t.Errorf("/%s/%s on %q: FindAll\n  optimised %v\n  reference %v", p.opt, p.flags, input, optAll, refAll)
	}
	// Stateful (g/y) iteration from lastIndex.
	p.ref.SetLastIndex(lastIndex)
	p.opt.SetLastIndex(lastIndex)
	for i := 0; i < 3; i++ {
		r, o := p.ref.FindStringSubmatch(input), p.opt.FindStringSubmatch(input)
		if !reflect.DeepEqual(r, o) || p.ref.LastIndex() != p.opt.LastIndex() {
			t.Errorf("/%s/%s on %q from lastIndex %d: exec\n  optimised %q (lastIndex %d)\n  reference %q (lastIndex %d)",
				p.opt, p.flags, input, lastIndex, o, p.opt.LastIndex(), r, p.ref.LastIndex())
			break
		}
	}
	return true
}

var (
	genAtoms  = []string{"a", "b", "A", ".", "[ab]", "[^a]", "[a-c]", `\d`, `\w`, `\s`, `\W`, "é", `\n`, "[é-ë]", `\p{L}`, "(?:)", "x", `\b`, `\B`, "^", "$", "k", "[r-t]"}
	genQuants = []string{"*", "+", "?", "{0,2}", "{1,3}", "{2}", "{1,}", "*?", "+?", "??", "{0,2}?", "{2,}?"}
	genInputs = []string{"a", "a", "b", "A", "\n", " ", "1", "é", "x", "\xe2\x82", "\xff", "ab", "€", "\u212a", "ſ", "k"}
	genFlags  = []string{"", "i", "m", "s", "u", "im", "y", "g", "iu", "ms", "gy", "gu"}
)

// genPattern builds a random pattern exercising quantifiers over single runes
// and groups, alternation, captures, backreferences, lookarounds and anchors.
func genPattern(r *rand.Rand, depth int, groups *int) string {
	if depth <= 0 || r.Intn(4) == 0 {
		return genAtoms[r.Intn(len(genAtoms))]
	}
	sub := func() string { return genPattern(r, depth-1, groups) }
	switch r.Intn(12) {
	case 0, 1:
		return sub() + sub()
	case 2:
		return sub() + "|" + sub()
	case 3:
		*groups++
		return "(" + sub() + ")"
	case 4:
		return "(?:" + sub() + ")" + genQuants[r.Intn(len(genQuants))]
	case 5, 6:
		return genAtoms[r.Intn(len(genAtoms))] + genQuants[r.Intn(len(genQuants))]
	case 7:
		if *groups > 0 {
			return `\` + strconv.Itoa(1+r.Intn(*groups))
		}
		return "a"
	case 8:
		return []string{"(?=", "(?!", "(?<=", "(?<!"}[r.Intn(4)] + sub() + ")"
	case 9:
		*groups++
		return "(" + sub() + ")" + genQuants[r.Intn(len(genQuants))]
	case 10:
		return "[a-z]+"
	default:
		return ".*"
	}
}

func genInput(r *rand.Rand, maxLen int) string {
	var sb strings.Builder
	for n := r.Intn(maxLen + 1); n > 0; n-- {
		sb.WriteString(genInputs[r.Intn(len(genInputs))])
	}
	return sb.String()
}

// TestOptimisedMatchesReference is a differential test: the optimisations
// (failure-memo keying, dead-branch filtering, greedy loop scanning, memo reuse
// across start positions) must never change a result, only its cost. Inputs
// include invalid UTF-8 and lastIndex values inside multi-byte runes.
func TestOptimisedMatchesReference(t *testing.T) {
	iterations := 4000
	if testing.Short() {
		iterations = 500
	}
	r := rand.New(rand.NewSource(1))
	compared := 0
	for i := 0; i < iterations; i++ {
		groups := 0
		pattern := genPattern(r, 4, &groups)
		p, err := compilePair(pattern, genFlags[r.Intn(len(genFlags))])
		if err != nil {
			if strings.Contains(err.Error(), "only without") {
				t.Fatal(err)
			}
			continue
		}
		for k := 0; k < 4; k++ {
			in := genInput(r, 14)
			if p.compare(t, in, r.Intn(len(in)+1)) {
				compared++
			}
		}
		if t.Failed() {
			return
		}
	}
	if compared < iterations {
		t.Fatalf("only %d comparisons completed; the generator is producing mostly invalid or over-budget cases", compared)
	}
}

// TestOptimisedMatchesReferenceCorpus pins cases around the greedy loop's
// give-back path: invalid UTF-8, a start inside a multi-byte rune, and
// right-to-left loops in lookbehinds.
func TestOptimisedMatchesReferenceCorpus(t *testing.T) {
	inputs := []string{"", "a", "aé€b", "a\xe2\x82a", "\xe2\x82\xac\xe2\x82", "€€x", "\xff\xfe€", "ab\nab", "é\xffé", "€€€\xe2\x82é", "kK\u212aſs"}
	patterns := []struct{ pattern, flags string }{
		{`.*(.)`, "y"}, {`(.+)(.)$`, "y"}, {`.*?(.)$`, "y"}, {`(.*)(.)`, "g"},
		{`(?<=(.+))(.)`, "y"}, {`(?<=(.*)(.))`, "g"}, {`(?<=^.*)(.)`, "gs"},
		{`[^a]*(.)`, "y"}, {`\W*(.)`, "gu"}, {`.*$`, "gm"}, {`^.*`, "gm"},
		{`(?<!.*x)(.)`, "g"}, {`(?=(.*))(.)`, "y"}, {`.+?(.)(.)`, "y"},
		{`[a-z]*(\1)`, "g"}, {`(.)*\1`, "g"}, {`.*(.)€`, "y"}, {`(?<=(.)é.*)`, "y"},
		{`(?:k|x)+`, "giu"}, {`[r-t]*`, "giu"}, {`k*?$`, "giu"},
	}
	for _, pc := range patterns {
		p, err := compilePair(pc.pattern, pc.flags)
		if err != nil {
			t.Fatalf("/%s/%s: %v", pc.pattern, pc.flags, err)
		}
		for _, in := range inputs {
			for li := 0; li <= len(in); li++ {
				p.compare(t, in, li)
			}
		}
	}
}

func FuzzOptimisedMatchesReference(f *testing.F) {
	f.Add(`^(a)+$`, "", "aaaa", 0)
	f.Add(`(?:a|ab)*c`, "g", "abababc", 1)
	f.Add(`.*(.)`, "y", "a\xe2\x82a", 2)
	f.Add(`(?<=(\w+))x`, "gi", "abcX", 0)
	f.Add(`((?:)*?)[a-z]+`, "g", "xaAa", 0)
	f.Add(`(a*)*b`, "", "aaac", 0)
	f.Add(`^[a-z]+$`, "m", "ab\ncd", 0)
	f.Fuzz(func(t *testing.T, pattern, fl, input string, lastIndex int) {
		if len(pattern) > 64 || len(input) > 64 {
			return
		}
		p, err := compilePair(pattern, fl)
		if err != nil {
			if strings.Contains(err.Error(), "only without") {
				t.Fatal(err)
			}
			return
		}
		if lastIndex < 0 {
			lastIndex = -lastIndex
		}
		p.compare(t, input, lastIndex%(len(input)+1))
	})
}
