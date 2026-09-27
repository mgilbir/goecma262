package compiler

import (
	"math/rand"
	"strconv"
	"testing"

	"github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// TestNullableIsSound checks that nullable never says false for an expression
// that can match the empty string: the compiler omits the empty check for
// such loop bodies, which is correct only if they cannot. For random
// expressions X it asks the engine whether X matches empty at each position
// of a few inputs, via ^[\s\S]{p}(?:X)(?<=^[\s\S]{p}), which matches exactly
// when X can end where it started, at p.
func TestNullableIsSound(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	atoms := []string{"a", "b", ".", "[ab]", `\b`, `\B`, "^", "$", "(?:)", "(?=a)", "(?!a)", "(?<=a)", "(?<!b)", `\1`, `\2`, "x", `\d`}
	quants := []string{"", "", "*", "+", "?", "{2}", "{0,2}", "{1,}", "*?", "+?"}
	var gen func(d int) string
	gen = func(d int) string {
		if d == 0 || r.Intn(3) == 0 {
			return atoms[r.Intn(len(atoms))] + quants[r.Intn(len(quants))]
		}
		switch r.Intn(4) {
		case 0:
			return gen(d-1) + gen(d-1)
		case 1:
			return gen(d-1) + "|" + gen(d-1)
		case 2:
			return "(" + gen(d-1) + ")" + quants[r.Intn(len(quants))]
		default:
			return "(?:" + gen(d-1) + ")" + quants[r.Intn(len(quants))]
		}
	}
	inputs := []string{"", "a", "ab", "ba", "aab", "x1"}
	checked, nullables := 0, 0
	for i := 0; i < 8000; i++ {
		x := "(" + gen(3) + ")(" + gen(2) + ")" // two groups, so \1 and \2 resolve
		ast, err := parser.New(x, parser.Flags{AnnexB: true}).Parse()
		if err != nil {
			continue
		}
		claim := nullable(ast.Body)
		if claim {
			nullables++
		}
		empty := false
		for _, in := range inputs {
			for p := 0; p <= len(in) && !empty; p++ {
				probe := `^[\s\S]{` + strconv.Itoa(p) + `}(?:` + x + `)(?<=^[\s\S]{` + strconv.Itoa(p) + `})`
				past, err := parser.New(probe, parser.Flags{AnnexB: true}).Parse()
				if err != nil {
					t.Fatalf("probe %s: %v", probe, err)
				}
				code, n, err := Compile(past)
				if err != nil {
					t.Fatalf("probe %s: %v", probe, err)
				}
				m := &vm.VM{Code: code, NumGroups: n}
				if ok, _, _ := m.Match(in, 0); ok {
					empty = true
				}
				if m.Err != nil {
					t.Fatalf("probe %s on %q: %v", probe, in, m.Err)
				}
			}
		}
		checked++
		if empty && !claim {
			t.Errorf("nullable(%s) = false, but it matches the empty string", x)
		}
	}
	if checked < 2000 || nullables == 0 || nullables == checked {
		t.Fatalf("weak sample: %d checked, %d nullable", checked, nullables)
	}
}
