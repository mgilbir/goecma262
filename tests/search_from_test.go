package ecma262_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/vm"
)

// The issue's reproducer: a g search from lastIndex 1 that runs out of steps
// reads as no match through MatchString, and must report the budget.
func TestSearchFromReportsTheBudget(t *testing.T) {
	re := ecma262.MustCompile(`(a|aa)*b`, flags.Global)
	re.SetMaxSteps(100_000)
	input := strings.Repeat("a", 1<<16)
	re.SetLastIndex(1)
	if re.MatchString(input) {
		t.Fatal("MatchString matched")
	}
	for name, find := range map[string]func(string, int) ([]int, error){
		"Index":     re.FindStringSubmatchIndexFromErr,
		"Positions": re.FindStringSubmatchPositionsFromErr,
	} {
		if _, err := find(input, 1); !errors.Is(err, ecma262.ErrStepLimit) {
			t.Errorf("%s: error %v, want ErrStepLimit", name, err)
		}
	}
}

func TestSearchFrom(t *testing.T) {
	cases := []struct {
		pattern, flags, input string
		start                 int
		want                  []int
	}{
		{`a`, "", "aXa", 0, []int{0, 1}},
		{`a`, "", "aXa", 1, []int{2, 3}},
		{`a`, "g", "aXa", 1, []int{2, 3}}, // g does not change where it searches
		{`a`, "y", "aXa", 1, nil},         // sticky: only at start
		{`a`, "y", "aXa", 2, []int{2, 3}},
		{`(a)|(b)`, "", "xxb", 1, []int{2, 3, -1, -1, 2, 3}},
		{`a`, "", "aaa", -5, []int{0, 1}}, // a negative start is 0
		{`a`, "", "aaa", 3, nil},
		{`$`, "", "aaa", 3, []int{3, 3}},
		{`a`, "", "aaa", 4, nil}, // past the end
		{`(?<=a)b`, "", "abab", 2, []int{3, 4}},
	}
	for _, tc := range cases {
		re := ecma262.MustCompile(tc.pattern, mustFlags(t, tc.flags))
		re.SetLastIndex(7)
		got, err := re.FindStringSubmatchIndexFromErr(tc.input, tc.start)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("/%s/%s from %d in %q: %v, %v; want %v", tc.pattern, tc.flags, tc.start, tc.input, got, err, tc.want)
		}
		pos, err := re.FindStringSubmatchPositionsFromErr(tc.input, tc.start)
		if err != nil || !reflect.DeepEqual(pos, tc.want) {
			t.Errorf("/%s/%s from %d in %q: positions %v, %v; want %v", tc.pattern, tc.flags, tc.start, tc.input, pos, err, tc.want)
		}
		if re.LastIndex() != 7 {
			t.Errorf("/%s/%s: lastIndex changed to %d", tc.pattern, tc.flags, re.LastIndex())
		}
	}
}

// Without u or v a match may begin or end between the halves of a surrogate
// pair: the Index forms refuse it, the Positions forms report it as
// SetLastIndex does, at the character's first byte plus 2.
func TestPositionsBetweenSurrogates(t *testing.T) {
	const s = "😀a😀" // 😀 is 4 bytes; its halves meet at 2 and 7
	re := ecma262.MustCompile(`\uD83D`, 0)
	if _, err := re.FindStringSubmatchIndexFromErr(s, 0); !errors.Is(err, ecma262.ErrSurrogateSplit) {
		t.Errorf("Index form: error %v, want ErrSurrogateSplit", err)
	}
	if got, err := re.FindStringSubmatchPositionsFromErr(s, 0); err != nil || !reflect.DeepEqual(got, []int{0, 2}) {
		t.Errorf("positions %v, %v; want [0 2]", got, err)
	}
	if got, err := re.FindStringSubmatchPositionsFromErr(s, 1); err != nil || !reflect.DeepEqual(got, []int{5, 7}) {
		t.Errorf("positions from 1: %v, %v; want [5 7]", got, err)
	}
	// From the position between the halves, as lastIndex may be.
	low := ecma262.MustCompile(`[\uDC00-\uDFFF]`, 0)
	if got, err := low.FindStringSubmatchPositionsFromErr(s, 2); err != nil || !reflect.DeepEqual(got, []int{2, 4}) {
		t.Errorf("low half from 2: %v, %v; want [2 4]", got, err)
	}
	all, err := re.FindAllStringSubmatchPositionsErr(s, -1)
	if err != nil || !reflect.DeepEqual(all, [][]int{{0, 2}, {5, 7}}) {
		t.Errorf("all: %v, %v; want [[0 2] [5 7]]", all, err)
	}
	if _, err := re.FindAllStringSubmatchIndexErr(s, -1); !errors.Is(err, ecma262.ErrSurrogateSplit) {
		t.Errorf("FindAllStringSubmatchIndexErr: error %v, want ErrSurrogateSplit", err)
	}
	// Empty matches advance by a code unit, as in JavaScript, where
	// "😀".match(/x*/g) finds three, at 0, 1 and 2.
	empty, err := ecma262.MustCompile(`x*`, flags.Global).FindAllStringSubmatchPositionsErr("😀", -1)
	if err != nil || !reflect.DeepEqual(empty, [][]int{{0, 0}, {2, 2}, {4, 4}}) {
		t.Errorf("x* on 😀: %v, %v; want [[0 0] [2 2] [4 4]]", empty, err)
	}
	// With u, a character is a code point.
	empty, err = ecma262.MustCompile(`x*`, flags.Unicode).FindAllStringSubmatchPositionsErr("😀", -1)
	if err != nil || !reflect.DeepEqual(empty, [][]int{{0, 0}, {4, 4}}) {
		t.Errorf("x* on 😀 with u: %v, %v; want [[0 0] [4 4]]", empty, err)
	}
	if got, err := re.FindAllStringSubmatchPositionsErr(s, 1); err != nil || len(got) != 1 {
		t.Errorf("n = 1: %v, %v", got, err)
	}
	if got, err := re.FindAllStringSubmatchPositionsErr(s, 0); err != nil || got != nil {
		t.Errorf("n = 0: %v, %v", got, err)
	}
}

// Where no position falls between two halves, the new forms agree with the
// existing ones: the iteration with FindAllStringSubmatchIndexErr, and a
// search from start with the first match at or after start, which the
// sticky form finds trying each position in turn, a character at a time.
func TestSearchFromAgreesWithExistingForms(t *testing.T) {
	checked := 0
	seen := map[[3]string]bool{}
	for _, tc := range test262GeneratedCases {
		k := [3]string{tc.pattern, tc.flags, tc.input}
		if seen[k] || len(tc.input) > 200 || strings.ContainsAny(tc.flags, "y") {
			continue
		}
		seen[k] = true
		f, err := flags.Parse(strings.ReplaceAll(tc.flags, "g", ""))
		if err != nil {
			continue
		}
		re, err := ecma262.Compile(tc.pattern, f)
		if err != nil {
			continue
		}
		sticky, err := ecma262.Compile(tc.pattern, f|flags.Sticky)
		if err != nil {
			t.Fatal(err)
		}
		unicode := f.Has(flags.Unicode) || f.Has(flags.UnicodeSets)
		all, allErr := re.FindAllStringSubmatchIndexErr(tc.input, -1)
		if allErr == nil {
			pos, err := re.FindAllStringSubmatchPositionsErr(tc.input, -1)
			if err != nil || !reflect.DeepEqual(pos, all) {
				t.Fatalf("/%s/%s on %q: positions %v, %v; FindAll %v", tc.pattern, tc.flags, tc.input, pos, err, all)
			}
		}
		for start := 0; start <= len(tc.input)+1; start++ {
			got, err := re.FindStringSubmatchPositionsFromErr(tc.input, start)
			if err != nil {
				t.Fatalf("/%s/%s on %q from %d: %v", tc.pattern, tc.flags, tc.input, start, err)
			}
			var want []int
			for p := start; p <= len(tc.input) && want == nil; p = vm.NextPosition(tc.input, p, unicode) {
				if want, err = sticky.FindStringSubmatchPositionsFromErr(tc.input, p); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("/%s/%s on %q from %d: %v, but trying each position finds %v", tc.pattern, tc.flags, tc.input, start, got, want)
			}
			checked++
		}
	}
	if checked < 10_000 {
		t.Fatalf("only %d searches checked", checked)
	}
}
