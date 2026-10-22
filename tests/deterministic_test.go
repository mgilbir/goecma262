package ecma262_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// determinismRuns is how often an operation repeats in one process. Go
// randomises map iteration order per range statement, so a result that
// depends on it differs between runs almost at once.
const determinismRuns = 200

// A pattern with several duplicate names must report the same one every time:
// the name of the group defined a second time earliest in the source.
func TestDuplicateGroupNameReportIsDeterministic(t *testing.T) {
	many := func(n int, reverse bool) (string, string) {
		// n names, each defined twice back to back-ish: name_i, ..., name_i.
		var sb strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&sb, "(?<n%d>)", i)
		}
		for i := 0; i < n; i++ {
			j := i
			if reverse {
				j = n - 1 - i
			}
			fmt.Fprintf(&sb, "(?<n%d>)", j)
		}
		// The first repeated definition is the (n+1)th group.
		if reverse {
			return sb.String(), fmt.Sprintf("n%d", n-1)
		}
		return sb.String(), "n0"
	}
	manyFwd, wantFwd := many(60, false)
	manyRev, wantRev := many(60, true)

	for _, tc := range []struct {
		name, pattern, want string
	}{
		{"aster crasher", `(?<A0>)(?<A>)(((?<A0>)(?<A>))0)`, "A0"},
		{"swapped crasher", `(?<A>)(?<A0>)(((?<A0>)(?<A>))0)`, "A0"},
		{"second name repeats first", `(?<B>)(?<A>)(?<A>)(?<B>)`, "A"},
		{"first name repeats first", `(?<B>)(?<A>)(?<B>)(?<A>)`, "B"},
		{"inner duplicate after earlier cross duplicate", `(?<a>)(?:(?<a>)(?<b>)(?<b>))`, "a"},
		{"cross duplicate after earlier inner duplicate", `(?<b>)(?<b>)(?:(?<a>)(?<a>))`, "b"},
		{"lookahead", `(?<x>)(?=(?<y>)(?<y>))(?<x>)`, "y"},
		{"lookbehind", `(?<x>)(?<=(?<y>)(?<y>))(?<x>)`, "y"},
		{"negative lookahead", `(?<x>)(?!(?<y>)(?<y>))(?<x>)`, "y"},
		{"duplicate inside lookahead and outside", `(?=(?<p>))(?<q>)(?<q>)(?<p>)`, "q"},
		{"after alternatives", `(?:(?<a>x)|(?<a>y))(?<a>z)`, "a"},
		{"two after alternatives", `(?:(?<a>x)|(?<a>y))(?<b>)(?<b>)(?<a>z)`, "b"},
		{"alternative one side", `(?:(?<a>x)|(?<b>y))(?<b>z)(?<a>w)`, "b"},
		{"alternative sides repeat in later alternative", `(?<a>)(?:(?<b>)|(?<a>)(?<b>))`, "a"},
		{"nested self", `(?<a>(?<b>(?<b>))(?<a>))`, "b"},
		{"enclosing group", `(?<a>(?<b>)(?<a>)(?<b>))`, "a"},
		{"quantified", `(?<a>x){2}(?<c>)(?<c>)(?<a>)`, "c"},
		{"modifier group", `(?<m>)(?i:(?<n>)(?<n>))(?<m>)`, "n"},
		{"many forward", manyFwd, wantFwd},
		{"many reversed", manyRev, wantRev},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := "duplicate group name: " + tc.want
			for i := 0; i < determinismRuns; i++ {
				_, err := ecma262.Compile(tc.pattern, flags.Unicode)
				if err == nil {
					t.Fatalf("run %d: %q compiled; want a duplicate name error", i, tc.pattern)
				}
				if !strings.HasSuffix(err.Error(), want) {
					t.Fatalf("run %d: error %q, want one ending %q", i, err, want)
				}
			}
		})
	}
}

// Names may repeat across alternatives, and only there.
func TestDuplicateGroupNameVerdicts(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		ok      bool
	}{
		{`(?<a>x)|(?<a>y)`, true},
		{`(?:(?<a>x)|(?<a>y))`, true},
		{`(?<a>x)|(?<b>y)|(?<a>z)`, true},
		{`(?:(?<a>x)|(?<a>y))(?<a>z)`, false},
		{`(?<a>x)(?:(?<a>y)|z)`, false},
		{`(?<a>x)(?:y|(?<a>z))`, false},
		{`(?<a>(?<a>x))`, false},
		{`(?<a>x)(?<a>y)`, false},
		{`(?<a>x)(?=(?<a>y))`, false},
		{`(?<a>x)|(?=(?<a>y))`, true},
		{`(?:(?<a>x)|(?<b>y))(?:(?<a>z)|(?<b>w))`, false},
		{`(?:(?<a>x)|(?<b>y))(?:(?<c>z)|(?<d>w))`, true},
		{`(?<a>x)|(?<b>y)(?<a>z)`, true},
		{`(?<a>x){2}`, true},
	} {
		_, err := ecma262.Compile(tc.pattern, flags.Unicode)
		if (err == nil) != tc.ok {
			t.Errorf("%q: err = %v, want accepted = %v", tc.pattern, err, tc.ok)
		}
	}
}

// Everything a caller can observe of a compiled pattern, or of failing to
// compile it, must be the same on every compile in one process.
func TestCompileIsDeterministic(t *testing.T) {
	patterns := []string{
		`(?<A0>)(?<A>)(((?<A0>)(?<A>))0)`,
		`(?<b>)(?<a>)(?<b>)(?<a>)`,
		`(?<a>x)|(?<a>y)|(?<b>z)|(?<b>w)`,
		`(?:(?<a>x)|(?<a>y))(?<a>z)`,
		`(?<year>\d{4})-(?<month>\d{2})-(?<day>\d{2})`,
		`(?<a>.)(?<b>.)(?<c>.)\k<c>\k<b>\k<a>`,
		`(?:(?<a>x)|(?<a>y))\k<a>`,
		`(?<a>x)\k<a>|(?<a>y)\k<a>|(?<b>z)`,
		`(?=(?<p>.))(?<q>.)(?<=(?<r>.))`,
		`(?<a>[\p{L}--[a-z]]|\p{RGI_Emoji})+(?<b>[\p{Emoji}&&\p{L}])`,
		`(?<n0>)(?<n1>)(?<n2>)(?<n3>)(?<n4>)(?<n5>)(?<n6>)(?<n7>)(?<n8>)(?<n9>)(?<n3>)(?<n7>)`,
		`(?<a>)(?<b>)(?<a>)(?<c>)(?<b>)(?<c>)`,
		`(?<ä>x)(?<\u{61}>y)`,
		`(?<a>(?<a>x))`,
		`(?<a>x)(?<b>y)\k<c>`,
	}
	inputs := []string{"2026-10-22", "xyzw", "abccba", "xx yy", "ÄÖ😀a"}
	flagSets := []flags.Flags{flags.Unicode, flags.UnicodeSets, flags.Flags(0)}

	observe := func(p string, f flags.Flags) string {
		var sb strings.Builder
		re, err := ecma262.Compile(p, f)
		if err != nil {
			fmt.Fprintf(&sb, "error: %v\n", err)
			return sb.String()
		}
		fmt.Fprintf(&sb, "string=%s\nnumsub=%d\nnames=%q\nsize=%d\nfootprint=%d\n",
			re.String(), re.NumSubexp(), re.SubexpNames(), re.ProgramSize(), re.MemoryFootprint())
		for _, n := range re.SubexpNames() {
			if n != "" {
				fmt.Fprintf(&sb, "index(%s)=%d\n", n, re.SubexpIndex(n))
			}
		}
		for _, in := range inputs {
			fmt.Fprintf(&sb, "%q: %v %q %v\n", in, re.MatchString(in), re.FindStringSubmatch(in), re.FindStringSubmatchIndex(in))
			fmt.Fprintf(&sb, "  replace=%q\n", re.ReplaceAllString(in, "<$&>"))
		}
		return sb.String()
	}

	for _, p := range patterns {
		for _, f := range flagSets {
			first := observe(p, f)
			for i := 1; i < determinismRuns; i++ {
				if got := observe(p, f); !reflect.DeepEqual(got, first) {
					t.Fatalf("pattern %q flags %v: run %d differs from run 0:\n--- run 0\n%s--- run %d\n%s", p, f, i, first, i, got)
				}
			}
		}
	}
}
