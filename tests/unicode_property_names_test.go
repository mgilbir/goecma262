package ecma262_test

import (
	"reflect"
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// TestUnicodePropertyNames pins which \p{...} expressions are valid and what
// they match. ECMA-262 names properties and values exactly as its binary
// property table and PropertyValueAliases.txt spell them: no loose matching
// of case, spaces, underscores or hyphens, no Is/In prefixes, and only the
// listed properties (so Unicode's Hyphen or Other_Alphabetic are errors). The
// verdicts and matches are V8's (Node.js 26.9.0) except where noted; the
// inputs include an unassigned code point (U+0378), a private-use one
// (U+E000), U+FFFD and U+2028.
func TestUnicodePropertyNames(t *testing.T) {
	inputs := []string{"a", "A", "\u00aa", "\u0378", "\ufffd", "\u2028", "\ue000", " ", "5", "\u0661", "!", "\u0007", "\u0301", "\u03b1", "\u2c80"}
	cases := []struct {
		prop    string
		valid   bool
		matches []string
	}{
		{`\p{lowercase}`, false, nil},
		// V8 and JavaScriptCore accept WSpace (a Unicode alias of White_Space), but
		// ECMA-262 lists only White_Space and space.
		{`\p{WSpace}`, false, nil},
		{`\p{ascii}`, false, nil},
		{`\p{Script=greek}`, false, nil},
		{`\p{gc=lu}`, false, nil},
		{`\p{Hyphen}`, false, nil},
		{`\p{Other_Alphabetic}`, false, nil},
		{`\p{Prepended_Concatenation_Mark}`, false, nil},
		{`\p{General_Category = Lu}`, false, nil},
		{`\p{Lowercase_letter}`, false, nil},
		{`\p{isLu}`, false, nil},
		{`\p{Block=Basic_Latin}`, false, nil},
		{`\p{InBasic_Latin}`, false, nil},
		{`\p{Lu=Lu}`, false, nil},
		{`\p{ASCII=Y}`, false, nil},
		{`\p{Any=Y}`, false, nil},
		{`\p{sc=Grek}`, true, []string{"\u03b1"}},
		// Script_Extensions is approximated by Script (Go has no
		// Script_Extensions data), so only validity is checked: V8 also
		// matches U+0301, whose extensions include Latin.
		{`\p{Script_Extensions=Latn}`, true, nil},
		{`\p{scx=Zyyy}`, true, []string{"\ufffd", "\u2028", " ", "5", "!", "\u0007"}},
		{`\p{sc=Qaac}`, true, []string{"\u2c80"}},
		{`\p{sc=Zinh}`, true, []string{"\u0301"}},
		{`\p{space}`, true, []string{"\u2028", " "}},
		{`\p{digit}`, true, []string{"5", "\u0661"}},
		{`\p{punct}`, true, []string{"!"}},
		{`\p{cntrl}`, true, []string{"\u0007"}},
		{`\p{Combining_Mark}`, true, []string{"\u0301"}},
		{`\p{LC}`, true, []string{"a", "A", "\u03b1", "\u2c80"}},
		{`\p{Cased_Letter}`, true, []string{"a", "A", "\u03b1", "\u2c80"}},
		{`\p{Cn}`, true, []string{"\u0378"}},
		{`\p{Unassigned}`, true, []string{"\u0378"}},
		{`\p{C}`, true, []string{"\u0378", "\ue000", "\u0007"}},
		{`\p{Other}`, true, []string{"\u0378", "\ue000", "\u0007"}},
		{`\p{Assigned}`, true, []string{"a", "A", "\u00aa", "\ufffd", "\u2028", "\ue000", " ", "5", "\u0661", "!", "\u0007", "\u0301", "\u03b1", "\u2c80"}},
		{`\p{sc=Unknown}`, true, []string{"\u0378", "\ue000"}},
		{`\p{sc=Zzzz}`, true, []string{"\u0378", "\ue000"}},
		{`\p{Lower}`, true, []string{"a", "\u00aa", "\u03b1"}},
		{`\p{Upper}`, true, []string{"A", "\u2c80"}},
		{`\p{White_Space}`, true, []string{"\u2028", " "}},
		{`\p{AHex}`, true, []string{"a", "A", "5"}},
		{`\p{Gr_Ext}`, true, []string{"\u0301"}},
		{`\p{IDS}`, true, []string{"a", "A", "\u00aa", "\u03b1", "\u2c80"}},
		{`\p{XIDC}`, true, []string{"a", "A", "\u00aa", "5", "\u0661", "\u0301", "\u03b1", "\u2c80"}},
		{`\p{Pat_Syn}`, true, []string{"!"}},
		{`\p{Join_C}`, true, []string{}},
	}
	for _, tc := range cases {
		re, err := ecma262.Compile("^"+tc.prop+"$", flags.Unicode)
		if (err == nil) != tc.valid {
			t.Errorf("%s: compile error %v, want valid %v", tc.prop, err, tc.valid)
			continue
		}
		if err != nil || tc.matches == nil {
			continue
		}
		got := []string{}
		for _, in := range inputs {
			if re.MatchString(in) {
				got = append(got, in)
			}
		}
		if !reflect.DeepEqual(got, tc.matches) {
			t.Errorf("%s matches %+q, want %+q", tc.prop, got, tc.matches)
		}
	}
}
