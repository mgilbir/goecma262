package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// ECMA-262 matches Unicode property names and values exactly: a canonical
// name or an alias from its tables and PropertyValueAliases.txt, with no
// loose matching of case, spaces, hyphens or underscores, and only the binary
// properties it lists. Expectations were taken from node (V8, Unicode 17.0).
func TestPropertyNames_Exact(t *testing.T) {
	cases := []struct {
		name  string
		valid bool
	}{
		{`Lu`, true},
		{`Uppercase_Letter`, true},
		{`uppercase_letter`, false},
		{`UppercaseLetter`, false},
		{`lu`, false},
		{`LU`, false},
		{`L`, true},
		{`Letter`, true},
		{`LC`, true},
		{`Cased_Letter`, true},
		{`Cn`, true},
		{`Unassigned`, true},
		{`C`, true},
		{`Other`, true},
		{`digit`, true},
		{`Nd`, true},
		{`punct`, true},
		{`Combining_Mark`, true},
		{`cntrl`, true},
		{`gc=Lu`, true},
		{`General_Category=Lu`, true},
		{`GC=Lu`, false},
		{`gc=lu`, false},
		{`generalcategory=Lu`, false},
		{`general_category=Lu`, false},
		{`sc=Latn`, true},
		{`sc=Latin`, true},
		{`Script=Latin`, true},
		{`scx=Latn`, true},
		{`script=Latin`, false},
		{`sc=latin`, false},
		{`sc=LATIN`, false},
		{`sc=Zzzz`, true},
		{`sc=Unknown`, true},
		{`sc=Qaai`, true},
		{`sc=Zinh`, true},
		{`sc=Hrkt`, false},
		{`sc=Katakana_Or_Hiragana`, false},
		{`Script_Extensions=Greek`, true},
		{`White_Space`, true},
		{`WSpace`, true},
		{`space`, true},
		{`whitespace`, false},
		{`White-Space`, false},
		{`White Space`, false},
		{`ASCII`, true},
		{`ascii`, false},
		{`Any`, true},
		{`any`, false},
		{`Assigned`, true},
		{`assigned`, false},
		{`Alphabetic`, true},
		{`Alpha`, true},
		{`alpha`, false},
		{`ID_Start`, true},
		{`IDStart`, false},
		{`idstart`, false},
		{`Other_Math`, false},
		{`Other_Alphabetic`, false},
		{`Prepended_Concatenation_Mark`, false},
		{`Hyphen`, false},
		{`Emoji`, true},
		{`emoji`, false},
		{`EPres`, true},
		{`epres`, false},
		{`Latin`, false},
		{`Lu=Lu`, false},
		{`gc=`, false},
		{`=Lu`, false},
		{` Lu`, false},
		{`Lu `, false},
		{`gc =Lu`, false},
		{`RGI_Emoji`, false},
		{`Bidi_Mirrored`, true},
		{`Bidi_M`, true},
		{`Grapheme_Base`, true},
		{`Gr_Base`, true},
		{`Changes_When_Casefolded`, true},
		{`CWCF`, true},
		{`Changes_When_Casemapped`, true},
		{`CWCM`, true},
		{`Changes_When_NFKC_Casefolded`, true},
		{`CWKCF`, true},
	}
	for _, tc := range cases {
		_, err := ecma262.Compile(`\p{`+tc.name+`}`, flags.Unicode)
		if tc.valid && err != nil {
			t.Errorf(`\p{%s}: unexpected error %v`, tc.name, err)
		}
		if !tc.valid && err == nil {
			t.Errorf(`\p{%s}: expected a SyntaxError`, tc.name)
		}
	}
}

// Values whose resolution the exact tables changed: Cased_Letter is Lu, Ll
// and Lt (not every letter), Cn is computed, and so are Assigned and
// Script=Unknown. The rest come from the generated UCD tables, at code points
// where the approximations they replace were wrong, and Script_Extensions,
// which differs from Script where ScriptExtensions.txt lists a code point.
func TestPropertyNames_Members(t *testing.T) {
	cases := []struct {
		pattern string
		char    string
		match   bool
	}{
		{`\p{LC}`, "a", true},
		{`\p{LC}`, "\u02b0", false},
		{`\p{Cased_Letter}`, "A", true},
		{`\p{L}`, "\u02b0", true},
		{`\p{Cn}`, "\u0378", true},
		{`\p{Cn}`, "a", false},
		{`\p{Unassigned}`, "\U000e0080", true},
		{`\p{C}`, "\u0378", true},
		{`\p{C}`, "\u0000", true},
		{`\p{Assigned}`, "\u0378", false},
		{`\p{Assigned}`, "\u2028", true},
		{`\p{Assigned}`, "\U000f0000", true},
		{`\p{sc=Zzzz}`, "\u0378", true},
		{`\p{sc=Unknown}`, "a", false},
		{`\p{sc=Latn}`, "a", true},
		{`\p{sc=Qaai}`, "\u0300", true},
		{`\p{sc=Zyyy}`, "1", true},
		{`\p{White_Space}`, "\u0085", true},
		{`\p{White_Space}`, "\u180e", false},
		{`\p{gc=punct}`, "!", true},
		{`\p{digit}`, "\u0660", true},
		{`\p{Case_Ignorable}`, "'", true},
		{`\p{CI}`, "a", false},
		{`\p{Changes_When_Titlecased}`, "\u00df", true},
		{`\p{CWU}`, "\u00df", true},
		{`\p{Changes_When_Uppercased}`, "\u0149", true},
		{`\p{Default_Ignorable_Code_Point}`, "\u0600", false},
		{`\p{DI}`, "\u00ad", true},
		{`\p{ID_Start}`, "\u2e2f", false},
		{`\p{IDS}`, "a", true},
		{`\p{ID_Continue}`, "\u2e2f", false},
		{`\p{XID_Start}`, "\u037a", false},
		{`\p{XIDS}`, "\u0e33", false},
		{`\p{XID_Continue}`, "\u309b", false},
		{`\p{XIDC}`, "_", true},
		{`\p{Bidi_Mirrored}`, "(", true},
		{`\p{Bidi_M}`, "a", false},
		{`\p{Changes_When_Casefolded}`, "A", true},
		{`\p{CWCF}`, "a", false},
		{`\p{Changes_When_Casemapped}`, "a", true},
		{`\p{CWCM}`, "1", false},
		{`\p{Changes_When_NFKC_Casefolded}`, "\u00a0", true},
		{`\p{CWKCF}`, "a", false},
		{`\p{Grapheme_Base}`, "a", true},
		{`\p{Gr_Base}`, "\u0300", false},
		{`\p{scx=Latn}`, "\u0363", true},
		{`\p{sc=Latn}`, "\u0363", false},
		{`\p{scx=Arab}`, "\u060c", true},
		{`\p{sc=Arab}`, "\u060c", false},
		{`\p{scx=Syrc}`, "\u060c", true},
		{`\p{Script_Extensions=Greek}`, "\u0342", true},
		{`\p{scx=Zyyy}`, "\u060c", false},
		{`\p{scx=Zyyy}`, "1", true},
		{`\p{scx=Zinh}`, "\u0363", false},
		{`\p{scx=Zzzz}`, "\u0378", true},
	}
	for _, tc := range cases {
		re := ecma262.MustCompile(`^`+tc.pattern+`$`, flags.Unicode)
		if got := re.MatchString(tc.char); got != tc.match {
			t.Errorf("%s on %q: got %v, want %v", tc.pattern, tc.char, got, tc.match)
		}
	}
}
