package ecma262_test

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/compiler"
	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/parser"
)

func compileBudget(p, fl string, instructions int, bytes int64) (*ecma262.Regexp, error) {
	f, err := flags.Parse(fl)
	if err != nil {
		return nil, err
	}
	return ecma262.Compile(p, f, ecma262.WithBudget(instructions, bytes))
}

func isBudgetError(err error) bool {
	return errors.Is(err, ecma262.ErrBudgetExceeded) && errors.Is(err, ecma262.ErrLimit)
}

func TestBudgetInstructionsIsExact(t *testing.T) {
	for _, p := range []string{`(a{100}){10}`, `[\p{RGI_Emoji}]{3}`, `(?<=\p{RGI_Emoji_Flag_Sequence})x`} {
		size := ecma262.MustCompile(p, flags.UnicodeSets).ProgramSize()
		if _, err := compileBudget(p, "v", size, 0); err != nil {
			t.Errorf("%s: a budget of its %d instructions: %v", p, size, err)
		}
		if _, err := compileBudget(p, "v", size-1, 0); !isBudgetError(err) {
			t.Errorf("%s: a budget of %d instructions, one short: %v", p, size-1, err)
		}
	}
}

// A Regexp compiled within a byte budget holds no more than it, and a budget
// below what a pattern holds refuses it.
func TestBudgetBytesBoundsFootprint(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	check := func(p, fl string) {
		re, err := ecma262.CompileFlags(p, fl)
		if err != nil {
			return
		}
		fp := re.MemoryFootprint()
		if _, err := compileBudget(p, fl, 0, fp-1); !isBudgetError(err) {
			t.Fatalf("/%s/%s holds %d bytes, yet compiles in a budget of %d: %v", p, fl, fp, fp-1, err)
		}
		for i := 0; i < 3; i++ {
			budget := fp/2 + rng.Int63n(3*fp)
			re, err := compileBudget(p, fl, 0, budget)
			switch {
			case err == nil && re.MemoryFootprint() > budget:
				t.Fatalf("/%s/%s compiled in a budget of %d bytes and holds %d", p, fl, budget, re.MemoryFootprint())
			case err != nil && !isBudgetError(err):
				t.Fatalf("/%s/%s with a budget of %d bytes: %v", p, fl, budget, err)
			}
		}
	}
	n := 120
	if testing.Short() {
		n = 30
	}
	for _, fl := range []string{"", "i", "u", "v", "vi"} {
		g := &patternGen{rng: rand.New(rand.NewSource(int64(len(fl)) + 11)), u: fl == "u", v: strings.Contains(fl, "v")}
		for i := 0; i < n; i++ {
			g.groups, g.names = 0, nil
			check(g.disjunction(3), fl)
		}
	}
}

// A budget refuses a pattern before its cost is paid.
func TestBudgetRefusesBeforePaying(t *testing.T) {
	cases := []struct {
		name, pattern string
		f             flags.Flags
		limit         int64 // bytes refusing it may allocate
	}{
		// A million instructions from 15 bytes: refused once parsed.
		{"program", `(a{1000}){1000}`, 0, 16 << 10},
		{"class strings", `(?:\p{RGI_Emoji}){100}`, flags.UnicodeSets, 16 << 10},
		// Refused by its length, unparsed.
		{"source", strings.Repeat("a", 1<<20), 0, 8 << 10},
	}
	for _, tc := range cases {
		ecma262.Compile(tc.pattern, tc.f, ecma262.WithBudget(0, 1<<20)) // build any shared tables
		var err error
		allocated := allocatedBy(func() { _, err = ecma262.Compile(tc.pattern, tc.f, ecma262.WithBudget(0, 1<<20)) })
		if !isBudgetError(err) {
			t.Errorf("%s: %v", tc.name, err)
		}
		if allocated > tc.limit {
			t.Errorf("%s: refusing it allocated %d bytes", tc.name, allocated)
		}
	}
}

// Within a byte budget, Compile as a whole, garbage included, allocates at
// most three times the budget.
func TestBudgetBoundsCompileAllocation(t *testing.T) {
	var worst float64
	check := func(p, fl string) {
		re, err := ecma262.CompileFlags(p, fl)
		if err != nil {
			return
		}
		// About the least budget it compiles in: from its footprint up, by
		// quarters.
		budget := re.MemoryFootprint()
		for {
			if _, err := compileBudget(p, fl, 0, budget); err == nil {
				break
			}
			budget += budget / 4
		}
		got := allocatedBy(func() {
			if _, err := compileBudget(p, fl, 0, budget); err != nil {
				t.Fatal(err)
			}
		})
		if r := float64(got) / float64(budget); r > worst {
			worst = r
		}
		if got > 3*budget {
			t.Fatalf("/%.80s/%s: compiled in a budget of %d bytes, allocating %d", p, fl, budget, got)
		}
	}
	n := 80
	if testing.Short() {
		n = 20
	}
	for _, fl := range []string{"", "i", "u", "v", "vi"} {
		g := &patternGen{rng: rand.New(rand.NewSource(int64(len(fl)) + 13)), u: fl == "u", v: strings.Contains(fl, "v")}
		for i := 0; i < n; i++ {
			g.groups, g.names = 0, nil
			check(g.disjunction(3), fl)
		}
	}
	for _, p := range []string{`(a{1000}){190}`, `([abcdefghijklmnopqrstuvwxyz0123456789]{1000}){190}`,
		strings.Repeat(`[\p{RGI_Emoji}--\p{RGI_Emoji}]`, 300), "[" + strings.Repeat(`\p{RGI_Emoji}`, 300) + "]",
		strings.Repeat(`(?<=[\p{RGI_Emoji}--\q{x}])`, 30), `(?:\p{RGI_Emoji}){50}`, strings.Repeat("a", 100000),
		strings.Repeat("(?:a|b)", 10000), `(?:[\q{abc|abd|x}]){5000}`} {
		check(p, "v")
	}
	t.Logf("at most %.2f times the budget", worst)
}

// CompileChecked shows its check the cost Bound reports, once, before any
// code is emitted, and returns the check's refusal as it is.
func TestCompileCheckedChecksTheCostOnce(t *testing.T) {
	for _, p := range []string{`a`, `(a{1000}){190}`, `[\q{ab|cd}]{3}`, `(?<=\p{RGI_Emoji})x`, `(a{1000}){1000}`} {
		ast, err := parser.New(p, parser.Flags{UnicodeSets: true}).Parse()
		if err != nil {
			t.Fatal(err)
		}
		want, err := compiler.Bound(ast)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		var got compiler.Cost
		_, _, err = compiler.CompileChecked(context.Background(), ast, func(c compiler.Cost) error {
			calls++
			got = c
			return nil
		})
		if calls != 1 || got != want {
			t.Errorf("%s: check called %d times with %+v; want once with %+v", p, calls, got, want)
		}
		if want.Instructions <= compiler.MaxProgramSize && err != nil {
			t.Errorf("%s: %v", p, err)
		}

		refusal := errors.New("refused")
		var cerr error
		allocated := allocatedBy(func() {
			_, _, cerr = compiler.CompileChecked(context.Background(), ast, func(compiler.Cost) error { return refusal })
		})
		// The refusal comes first, even for a pattern over MaxProgramSize,
		// and before the program is allocated.
		if cerr != refusal {
			t.Errorf("%s: error %v, want the check's", p, cerr)
		}
		if allocated > 16<<10 {
			t.Errorf("%s: a refused compile allocated %d bytes", p, allocated)
		}
	}
}
