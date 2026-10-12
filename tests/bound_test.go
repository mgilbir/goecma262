package ecma262_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/compiler"
	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// boundStats tallies how close compiler.Bound comes to the real cost.
type boundStats struct {
	checked, exact int
	worstBytes     float64 // largest Bytes / Footprint
	worst          string
}

// checkBound compiles pattern as Compile does and checks that compiler.Bound
// predicted it: the instruction count exactly when Bound says so and never
// under, the program's footprint never under, and the same refusals.
func checkBound(t testing.TB, st *boundStats, pattern, flagStr string) {
	t.Helper()
	f, err := flags.Parse(flagStr)
	if err != nil || f.Has(flags.Unicode) && f.Has(flags.UnicodeSets) {
		return
	}
	u, v := f.Has(flags.Unicode), f.Has(flags.UnicodeSets)
	ast, err := parser.New(pattern, parser.Flags{
		IgnoreCase: f.Has(flags.IgnoreCase), Unicode: u, UnicodeSets: v,
		DotAll: f.Has(flags.DotAll), Multiline: f.Has(flags.Multiline), AnnexB: !u && !v,
	}).Parse()
	if err != nil {
		return
	}
	cost, berr := compiler.Bound(ast)
	code, _, cerr := compiler.Compile(ast)
	switch {
	case berr != nil && cerr == nil:
		t.Fatalf("/%s/%s: Bound refused (%v) what Compile compiles", pattern, flagStr, berr)
	case berr != nil:
		if berr.Error() != cerr.Error() {
			t.Fatalf("/%s/%s: Bound failed with %q, Compile with %q", pattern, flagStr, berr, cerr)
		}
		return
	case cerr != nil:
		if !errors.Is(cerr, compiler.ErrProgramTooLarge) || cost.Instructions <= compiler.MaxProgramSize {
			t.Fatalf("/%s/%s: Compile failed with %v, but Bound reported %+v", pattern, flagStr, cerr, cost)
		}
		return
	}
	if cost.Instructions < len(code) || cost.Exact && cost.Instructions != len(code) {
		t.Fatalf("/%s/%s: Bound counted %d instructions (exact %v), Compile emitted %d", pattern, flagStr, cost.Instructions, cost.Exact, len(code))
	}
	if cap(code) != len(code) {
		t.Fatalf("/%s/%s: the instruction slice has capacity %d for %d instructions", pattern, flagStr, cap(code), len(code))
	}
	prog := vm.NewProgram(code, f.Has(flags.IgnoreCase), f.Has(flags.Multiline), f.Has(flags.DotAll), u || v, false)
	fp := prog.Footprint()
	if cost.Bytes < fp {
		t.Fatalf("/%s/%s: Bound's %d bytes under-count the program's footprint %d", pattern, flagStr, cost.Bytes, fp)
	}
	st.checked++
	if cost.Exact {
		st.exact++
	}
	if r := float64(cost.Bytes) / float64(fp); r > st.worstBytes {
		st.worstBytes, st.worst = r, fmt.Sprintf("/%s/%s: %+v, %d instructions, footprint %d", pattern, flagStr, cost, len(code), fp)
	}
}

func TestBoundMatchesCompileOnTest262(t *testing.T) {
	var st boundStats
	seen := map[[2]string]bool{}
	for _, tc := range test262GeneratedCases {
		if k := [2]string{tc.pattern, tc.flags}; !seen[k] {
			seen[k] = true
			checkBound(t, &st, tc.pattern, tc.flags)
		}
	}
	for _, tc := range test262SyntaxCases {
		checkBound(t, &st, tc.pattern, tc.flags)
	}
	t.Logf("%d patterns, %d exact, Bytes at most %.2f× the footprint", st.checked, st.exact, st.worstBytes)
	if st.checked < 1000 {
		t.Fatalf("only %d patterns checked", st.checked)
	}
}

// genPattern builds a random pattern from every construct the compiler
// treats differently, nested up to depth.
type patternGen struct {
	rng    *rand.Rand
	v, u   bool
	groups int
	names  []string
}

func (g *patternGen) atom() string {
	common := []string{`a`, `b`, `.`, `\d`, `\W`, `\s`, `[abc]`, `[^a-z0-9_]`, `[\d\w-]`, `\b`, `\B`, `^`, `$`, `😀`, `A`}
	unicode := []string{`\p{L}`, `\P{Lu}`, `\p{Script=Greek}`, `[\p{Nd}x]`, `\u{1F600}`}
	sets := []string{
		`[\q{ab|cd|}]`, `[\q{abc|abd|x}]`, `[\p{RGI_Emoji}]`, `\p{RGI_Emoji_Flag_Sequence}`,
		`[\p{RGI_Emoji}--\q{x}]`, `[\p{RGI_Emoji}--[a-z]]`, `[[a-z]&&[aeiou]]`, `[\p{L}--[a-z]]`,
		`[^\d]`, `[\q{abc}\p{Emoji_Keycap_Sequence}]`, `[\p{RGI_Emoji}&&\p{RGI_Emoji_ZWJ_Sequence}]`,
		`[\p{RGI_Emoji}\p{RGI_Emoji}]`, `[\p{RGI_Emoji}&&[a]]`, `[[\q{ab}]--[\q{ab}]]`, `\p{Basic_Emoji}`,
		`[\p{RGI_Emoji_Tag_Sequence}\q{}]`, `[[a-c][x-z]\d]`,
	}
	pool := common
	if g.u || g.v {
		pool = append(pool, unicode...)
	}
	if g.v {
		pool = append(pool, sets...)
	}
	if g.groups > 0 && g.rng.Intn(8) == 0 {
		return fmt.Sprintf(`\%d`, 1+g.rng.Intn(g.groups))
	}
	if len(g.names) > 0 && g.rng.Intn(8) == 0 {
		return `\k<` + g.names[g.rng.Intn(len(g.names))] + `>`
	}
	return pool[g.rng.Intn(len(pool))]
}

func (g *patternGen) quantifier() string {
	q := []string{``, ``, ``, `*`, `+`, `?`, `{0}`, `{1}`, `{3}`, `{2,4}`, `{0,3}`, `{2,}`, `{0,1}`}[g.rng.Intn(13)]
	if q != `` && g.rng.Intn(3) == 0 {
		q += `?`
	}
	if g.rng.Intn(40) == 0 {
		q = fmt.Sprintf(`{%d,%d}`, g.rng.Intn(30), 30+g.rng.Intn(30))
	}
	return q
}

func (g *patternGen) term(depth int) string {
	if depth <= 0 || g.rng.Intn(3) > 0 {
		a := g.atom()
		if a == `^` || a == `$` || a == `\b` || a == `\B` {
			return a // not quantifiable outside Annex B
		}
		return a + g.quantifier()
	}
	body := g.disjunction(depth - 1)
	switch g.rng.Intn(10) {
	case 0:
		g.groups++
		return `(` + body + `)` + g.quantifier()
	case 1:
		g.groups++
		name := fmt.Sprintf("n%d", len(g.names))
		g.names = append(g.names, name)
		return `(?<` + name + `>` + body + `)` + g.quantifier()
	case 2:
		return `(?:` + body + `)` + g.quantifier()
	case 3:
		return `(?i:` + body + `)` + g.quantifier()
	case 4:
		return `(?-i:` + body + `)`
	case 5:
		return `(?=` + body + `)`
	case 6:
		return `(?!` + body + `)`
	case 7:
		return `(?<=` + body + `)`
	case 8:
		return `(?<!` + body + `)`
	}
	return `(?:` + body + `)` + g.quantifier()
}

func (g *patternGen) disjunction(depth int) string {
	alts := make([]string, 1+g.rng.Intn(3))
	for i := range alts {
		var sb strings.Builder
		for n := g.rng.Intn(4); n >= 0; n-- {
			sb.WriteString(g.term(depth))
		}
		alts[i] = sb.String()
	}
	return strings.Join(alts, `|`)
}

func TestBoundMatchesCompileOnRandomPatterns(t *testing.T) {
	n := 1500
	if testing.Short() {
		n = 200
	}
	var st boundStats
	for _, fl := range []string{"", "i", "m", "s", "u", "ui", "v", "vi"} {
		rng := rand.New(rand.NewSource(int64(len(fl)) + 7))
		start := time.Now()
		count := n
		if strings.Contains(fl, "v") {
			count = n / 3 // their tries make large programs, slow to analyse
		}
		for i := 0; i < count; i++ {
			g := &patternGen{rng: rng, u: strings.Contains(fl, "u"), v: strings.Contains(fl, "v")}
			checkBound(t, &st, g.disjunction(3), fl)
		}
		t.Logf("flags %q: %v", fl, time.Since(start))
	}
	t.Logf("%d patterns, %d exact, Bytes at most %.2f× the footprint: %s", st.checked, st.exact, st.worstBytes, st.worst)
	if st.checked < 3*n {
		t.Fatalf("only %d patterns compiled", st.checked)
	}
}

// Patterns at the program-size limit: Bound knows the size before anything
// is emitted, and the limit is exact.
func TestProgramSizeLimitIsExact(t *testing.T) {
	// Group 0's saves and the match, 19 copies of 10,000 and n more.
	at := func(n int) string { return fmt.Sprintf(`(?:a{10000}){19}a{%d}`, n) }
	re, err := ecma262.Compile(at(compiler.MaxProgramSize-190_003), 0)
	if err != nil {
		t.Fatalf("a program of exactly MaxProgramSize instructions: %v", err)
	}
	if re.ProgramSize() != compiler.MaxProgramSize {
		t.Fatalf("ProgramSize %d, want %d", re.ProgramSize(), compiler.MaxProgramSize)
	}
	if _, err := ecma262.Compile(at(compiler.MaxProgramSize-190_002), 0); !errors.Is(err, ecma262.ErrProgramTooLarge) {
		t.Fatalf("a program one instruction over the limit: %v", err)
	}
}

// References to a duplicated name share one list of its groups, which Bound
// must count once.
func TestBoundCountsSharedGroupListsOnce(t *testing.T) {
	const n = 2000
	pattern := "(?:" + strings.Repeat("(?<x>a)|", n-1) + "(?<x>a))" + strings.Repeat(`\k<x>`, n)
	var st boundStats
	checkBound(t, &st, pattern, "")
	ast, err := parser.New(pattern, parser.Flags{AnnexB: true}).Parse()
	if err != nil {
		t.Fatal(err)
	}
	cost, err := compiler.Bound(ast)
	if err != nil {
		t.Fatal(err)
	}
	// One list per reference would be n*n*8 = 32 MB.
	if cost.Bytes > 8<<20 {
		t.Errorf("Bound reports %d bytes", cost.Bytes)
	}
}

// The same for patterns whose classes hold strings, which Bound bounds
// rather than counts: Compile counts them exactly before emitting.
func TestProgramSizeLimitIsExactForClassStrings(t *testing.T) {
	// [\q{ab}] is 5 instructions: a split, a rune switch for each of a
	// and b, a jump and the class of its characters.
	at := func(n int) string { return fmt.Sprintf(`(?:(?:[\q{ab}]){10000}){3}(?:a{10000}){4}a{%d}`, n) }
	ast, err := parser.New(at(1), parser.Flags{UnicodeSets: true}).Parse()
	if err != nil {
		t.Fatal(err)
	}
	if cost, _ := compiler.Bound(ast); cost.Exact {
		t.Fatal("Bound counts [\\q{ab}] exactly; the test needs a pattern it bounds")
	}
	re, err := ecma262.Compile(at(compiler.MaxProgramSize-190_003), flags.UnicodeSets)
	if err != nil {
		t.Fatalf("a program of exactly MaxProgramSize instructions: %v", err)
	}
	if re.ProgramSize() != compiler.MaxProgramSize {
		t.Fatalf("ProgramSize %d, want %d", re.ProgramSize(), compiler.MaxProgramSize)
	}
	if _, err := ecma262.Compile(at(compiler.MaxProgramSize-190_002), flags.UnicodeSets); !errors.Is(err, ecma262.ErrProgramTooLarge) {
		t.Fatalf("a program one instruction over the limit: %v", err)
	}
	// Bound puts this far over the limit, but the subtraction leaves no
	// strings: it compiles to one instruction per copy.
	re, err = ecma262.Compile(`(?:[\p{RGI_Emoji}--\p{RGI_Emoji}]){10000}`, flags.UnicodeSets)
	if err != nil {
		t.Fatal(err)
	}
	if re.ProgramSize() != 10003 {
		t.Errorf("ProgramSize %d, want 10003", re.ProgramSize())
	}
}

func FuzzBound(f *testing.F) {
	for _, s := range []string{`a`, `(a{2,3}){4}`, `[\q{ab|c}]{3}`, `(?<=\p{RGI_Emoji}x)`, `(?i:[\p{RGI_Emoji}--\q{x}])+`, `(?:(?<n>a)|(?<n>b))\k<n>`} {
		for _, fl := range []string{"", "v", "vi", "u"} {
			f.Add(s, fl)
		}
	}
	var st boundStats
	f.Fuzz(func(t *testing.T, pattern, fl string) {
		if len(pattern) > 200 {
			return
		}
		checkBound(t, &st, pattern, fl)
	})
}
