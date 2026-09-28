package ecma262_test

import (
	"reflect"
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// TestModifiersSyntax pins the grammar and early errors of pattern modifiers
// (ES2025), (?ims:...) and (?ims-ims:...): only i, m and s, each at most once
// on a side, never on both sides, and not both sides empty. The grammar is the
// same in every mode; the verdicts are V8's (Node.js 26.9.0) and
// JavaScriptCore's (Bun 1.3.0), which agree on all of them.
func TestModifiersSyntax(t *testing.T) {
	cases := []struct {
		pattern      string
		annexB, u, v bool
	}{
		{"(?i:a)", true, true, true},
		{"(?m:a)", true, true, true},
		{"(?s:a)", true, true, true},
		{"(?ims:a)", true, true, true},
		{"(?smi:a)", true, true, true},
		{"(?-i:a)", true, true, true},
		{"(?-ims:a)", true, true, true},
		{"(?i-m:a)", true, true, true},
		{"(?i-:a)", true, true, true},
		{"(?im-s:a)", true, true, true},
		{"(?s-mi:a)", true, true, true},
		{"(?i:)", true, true, true},
		{"(?-s:)", true, true, true},
		{"(?-:a)", false, false, false},
		{"(?ii:a)", false, false, false},
		{"(?imi:a)", false, false, false},
		{"(?-ii:a)", false, false, false},
		{"(?i-i:a)", false, false, false},
		{"(?im-si:a)", false, false, false},
		{"(?i-m-s:a)", false, false, false},
		{"(?i--m:a)", false, false, false},
		{"(?x:a)", false, false, false},
		{"(?I:a)", false, false, false},
		{"(?u:a)", false, false, false},
		{"(?g:a)", false, false, false},
		{"(?d:a)", false, false, false},
		{"(?y:a)", false, false, false},
		{"(?i)", false, false, false},
		{"(?i)a", false, false, false},
		{"(?i-m)a", false, false, false},
		{"(?i", false, false, false},
		{"(?i-", false, false, false},
		{"(?i:a", false, false, false},
		{"(?\\u0069:a)", false, false, false},
		{"(?i\\u0307:a)", false, false, false},
		{"(?\u0130:a)", false, false, false},
		{"(?\u212a:a)", false, false, false},
		{"(?i\u200d:a)", false, false, false},
		{"(?i m:a)", false, false, false},
		{"(?i-\u017f:a)", false, false, false},
		{"(?i:a)*", true, true, true},
		{"(?-i:a){2}", true, true, true},
		{"(?i:(?-i:a))", true, true, true},
		{"(?i:(?i:a))", true, true, true},
		{"(?-i:(?-i:a))", true, true, true},
		{"(?i:(?m:(?s:a)))", true, true, true},
		{"(?i:a)|(?-i:b)", true, true, true},
		{"(?<=(?i:a))b", true, true, true},
		{"(?=(?-s:.))", true, true, true},
		{"((?i:a))\\1", true, true, true},
		{"(?i:\\k<n>(?<n>a))", true, true, true},
	}
	strict := ecma262.WithSyntax(ecma262.SyntaxStrict)
	for _, tc := range cases {
		for _, m := range []struct {
			name string
			ok   bool
			want bool
		}{
			{"Annex B", compiles(tc.pattern, 0), tc.annexB},
			{"strict", compiles(tc.pattern, 0, strict), tc.annexB},
			{"u", compiles(tc.pattern, flags.Unicode), tc.u},
			{"v", compiles(tc.pattern, flags.UnicodeSets), tc.v},
		} {
			if m.ok != m.want {
				t.Errorf("%s: /%s/ compiles = %v, want %v", m.name, tc.pattern, m.ok, m.want)
			}
		}
	}
}

// TestModifiersSemantics pins what modifier groups mean: inside the group the
// added flags are on and the removed ones off, for every construct the flag
// affects (characters, classes, \w \W \b \B and property escapes with
// Unicode case folding, backreferences, ^ and $, and .), in lookarounds,
// captures and quantified groups, and nowhere outside the group — including
// in the alternatives and terms that follow it.
//
// The expected submatches are V8's (Node.js 26.9.0) except where marked: V8
// lets a (?-i:...) group turn off case-insensitivity in the class that
// follows it under u or v. There JavaScriptCore (Bun 1.3.0) gives the
// expected result, which follows from the spec; JavaScriptCore in turn does
// not case-fold property escapes, (?i:[r-t]) for U+017F or the Kelvin sign
// under u, where V8 is right.
func TestModifiersSemantics(t *testing.T) {
	cases := []struct {
		pattern, flags, input string
		want                  []string // FindStringSubmatch; nil for no match
	}{
		{`(?i:a)b`, "", "Ab", []string{"Ab"}},
		{`(?i:a)b`, "", "AB", nil},
		{`(?i:a)b`, "", "ab", []string{"ab"}},
		{`(?-i:a)b`, "i", "aB", []string{"aB"}},
		{`(?-i:a)b`, "i", "AB", nil},
		{`(?-i:a)b`, "i", "ab", []string{"ab"}},
		{`(?i-:a)b`, "", "Ab", []string{"Ab"}},
		{`(?i-:a)b`, "", "AB", nil},
		{`(?m:^b)`, "", "a\nb", []string{"b"}},
		{`(?m:^b)`, "", "b", []string{"b"}},
		{`(?-m:^b)`, "m", "a\nb", nil},
		{`(?-m:^b)`, "m", "b", []string{"b"}},
		{`a(?m:$)`, "", "a\nb", []string{"a"}},
		{`a(?-m:$)`, "m", "a\nb", nil},
		{`a(?-m:$)`, "m", "ba", []string{"a"}},
		{`(?s:.)`, "", "\n", []string{"\n"}},
		{`(?s:.)`, "", "\u2028", []string{"\u2028"}},
		{`(?-s:.)`, "s", "\n", nil},
		{`(?-s:.)`, "s", "\u000d", nil},
		{`(?-s:.)`, "s", "x", []string{"x"}},
		{`(?s:.)+`, "", "a\nb", []string{"a\nb"}},
		{`(?-s:.)+`, "s", "a\nb", []string{"a"}},
		{`(?i:a(?-i:b)c)`, "", "AbC", []string{"AbC"}},
		{`(?i:a(?-i:b)c)`, "", "ABC", nil},
		{`(?i:a(?-i:b)c)`, "", "abc", []string{"abc"}},
		{`(?-i:a(?i:b)c)`, "i", "aBc", []string{"aBc"}},
		{`(?-i:a(?i:b)c)`, "i", "ABc", nil},
		{`(?-i:a(?i:b)c)`, "i", "abC", nil},
		{`(?i:a(?-i:b(?i:c)))`, "", "AbC", []string{"AbC"}},
		{`(?i:a(?-i:b(?i:c)))`, "", "Abc", []string{"Abc"}},
		{`(?i:a(?-i:b(?i:c)))`, "", "ABc", nil},
		{`(?im-s:^a.)`, "s", "x\nA\n", nil},
		{`(?im-s:^a.)`, "s", "x\nAb", []string{"Ab"}},
		{`(?s-im:^A.)`, "im", "x\nA\n", nil},
		{`(?s-im:^A.)`, "im", "A\n", []string{"A\n"}},
		{`(?s-im:^A.)`, "im", "a\n", nil},
		{`(?i:(a))\1`, "", "AA", []string{"AA", "A"}},
		{`(?i:(a))\1`, "", "Aa", nil},
		{`(?i:(a))\1`, "", "aa", []string{"aa", "a"}},
		{`(a)(?i:\1)`, "", "aA", []string{"aA", "a"}},
		{`(a)(?i:\1)`, "", "AA", nil},
		{`(a)(?i:\1)`, "", "aa", []string{"aa", "a"}},
		{`(a)(?-i:\1)`, "i", "Aa", nil},
		{`(a)(?-i:\1)`, "i", "AA", []string{"AA", "A"}},
		{`(a)(?-i:\1)`, "i", "aA", nil},
		{`(?i:(a+))b`, "", "xAAb", []string{"AAb", "AA"}},
		{`(?<n>(?i:a))\k<n>`, "", "AA", []string{"AA", "A"}},
		{`(?<n>(?i:a))\k<n>`, "", "Aa", nil},
		{`(?i:(?<n>a)\k<n>)`, "", "Aa", []string{"Aa", "A"}},
		{`(?i:(?<n>a)\k<n>)`, "", "aA", []string{"aA", "a"}},
		{`(?<=(?i:a))b`, "", "Ab", []string{"b"}},
		{`(?<=(?i:a))b`, "", "ab", []string{"b"}},
		{`(?<=(?i:a))b`, "", "Bb", nil},
		{`(?<=(?i:ab))c`, "", "ABc", []string{"c"}},
		{`(?<=(?i:ab))c`, "", "Abc", []string{"c"}},
		{`(?<!(?i:a))b`, "", "Ab", nil},
		{`(?<!(?i:a))b`, "", "xb", []string{"b"}},
		{`(?<=(?m:^))b`, "", "a\nb", []string{"b"}},
		{`(?<=(?i:(a))\1)x`, "", "AAx", []string{"x", "A"}},
		{`(?<=(?i:(a))\1)x`, "", "Aax", []string{"x", "a"}},
		{`(?<=\1(?i:(a)))x`, "", "aAx", nil},
		{`(?<=\1(?i:(a)))x`, "", "AAx", []string{"x", "A"}},
		{`(?<=\1(?i:(a)))x`, "", "Aax", nil},
		{`a(?=(?i:B))`, "", "aB", []string{"a"}},
		{`a(?=(?i:B))`, "", "ab", []string{"a"}},
		{`a(?!(?i:B))`, "", "aB", nil},
		{`a(?!(?i:B))`, "", "ac", []string{"a"}},
		{`(?=(?i:(A)))a`, "", "a", []string{"a", "a"}},
		{`(?i:a)+`, "", "aAaB", []string{"aAa"}},
		{`(?i:a){2}`, "", "aA", []string{"aA"}},
		{`(?:(?i:a)b)+`, "", "AbabAB", []string{"Abab"}},
		{`(?i:[a-c])+`, "", "AbCd", []string{"AbC"}},
		{`(?-i:[a-c])+`, "i", "AbCd", []string{"b"}},
		{`(?i:[^a])`, "", "A", nil},
		{`(?i:[^a])`, "", "b", []string{"b"}},
		{`(?-i:[^a])`, "i", "A", []string{"A"}},
		{`(?i:\w)`, "u", "\u017f", []string{"\u017f"}},
		{`(?i:\w)`, "u", "\u212a", []string{"\u212a"}},
		{`(?i:\W)`, "u", "\u017f", nil},
		{`(?-i:\w)`, "iu", "\u017f", nil},
		{`(?-i:\w)`, "iu", "\u212a", nil},
		{`(?-i:\W)`, "iu", "\u017f", []string{"\u017f"}},
		{`(?-i:\W)`, "iu", "\u212a", []string{"\u212a"}},
		{`(?i:\b)`, "u", "\u017f", []string{""}},
		{`(?-i:\b)`, "iu", "\u017f", nil},
		{`(?i:\p{Lu})`, "u", "a", []string{"a"}},
		{`(?i:\P{Lu})`, "u", "A", []string{"A"}},
		{`(?-i:\p{Lu})`, "iu", "a", nil},
		{`(?i:\P{Ll})`, "v", "a", nil},
		{`(?i:\P{Ll})`, "v", "1", []string{"1"}},
		{`(?i:ſ)`, "u", "s", []string{"s"}},
		{`(?i:ſ)`, "u", "S", []string{"S"}},
		{`(?i:ſ)`, "", "s", nil},
		{`(?i:ſ)`, "", "S", nil},
		{`(?i:k)`, "u", "\u212a", []string{"\u212a"}},
		{`(?i:k)`, "", "\u212a", nil},
		{`(?i:é)`, "", "\u00c9", []string{"\u00c9"}},
		{`(?i:[é-ë])`, "", "\u00c9", []string{"\u00c9"}},
		{`(?i:[r-t])`, "u", "\u017f", []string{"\u017f"}},
		{`(?i:[r-t])`, "", "\u017f", nil},
		{`^(?i:a)$`, "m", "x\nA\ny", []string{"A"}},
		{`(?i:^a$)`, "", "A", []string{"A"}},
		{`(?m:^)`, "g", "a\nb", []string{""}},
		{`(?i:x)|y`, "", "X", []string{"X"}},
		{`(?i:x)|y`, "", "Y", nil},
		{`x|(?i:y)`, "", "X", nil},
		{`x|(?i:y)`, "", "Y", []string{"Y"}},
		{`(?-i:x)|y`, "i", "X", nil},
		{`(?-i:x)|y`, "i", "Y", []string{"Y"}},
		{`(?-i:x)|[a-z]`, "iu", "A", []string{"A"}}, // V8 26.9 gets this wrong; JavaScriptCore and the spec agree
		{`(?-i:a)[b]`, "iu", "aB", []string{"aB"}},  // V8 26.9 gets this wrong; JavaScriptCore and the spec agree
		{`(?-i:a)\p{Lu}`, "iu", "ab", []string{"ab"}},
	}
	for _, tc := range cases {
		re, err := ecma262.CompileFlags(tc.pattern, tc.flags)
		if err != nil {
			t.Errorf("/%s/%s: %v", tc.pattern, tc.flags, err)
			continue
		}
		if got := re.FindStringSubmatch(tc.input); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("/%s/%s.FindStringSubmatch(%+q) = %+q, want %+q", tc.pattern, tc.flags, tc.input, got, tc.want)
		}
	}
}
