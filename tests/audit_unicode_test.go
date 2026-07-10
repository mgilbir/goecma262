package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// C7: general categories beyond the handful previously supported must actually
// match, not silently return false.
func TestAudit_UnicodePropertyCategories(t *testing.T) {
	cases := []struct {
		prop  string
		char  string
		match bool
	}{
		{`\p{Lo}`, "中", true},   // Other_Letter (CJK)
		{`\p{Lo}`, "a", false},  // 'a' is Ll, not Lo
		{`\p{Nl}`, "Ⅻ", true},   // Roman numeral twelve (Letter_Number)
		{`\p{Mn}`, "́", true}, // combining acute accent
		{`\p{Pd}`, "-", true},   // Dash_Punctuation
		{`\p{Sc}`, "$", true},   // Currency_Symbol
		{`\p{Lu}`, "A", true},
		{`\p{Ll}`, "a", true},
		{`\p{Zs}`, " ", true},
	}
	for _, tc := range cases {
		re, err := ecma262.Compile(tc.prop, flags.Unicode)
		if err != nil {
			t.Errorf("compile %s: %v", tc.prop, err)
			continue
		}
		if got := re.MatchString(tc.char); got != tc.match {
			t.Errorf("%s.MatchString(%q) = %v, want %v", tc.prop, tc.char, got, tc.match)
		}
	}
}

// C7/C19: unknown or empty property names are SyntaxErrors, not silent no-ops.
func TestAudit_UnicodePropertyUnknownRejected(t *testing.T) {
	for _, p := range []string{`\p{TotallyBogus}`, `\p{}`, `\P{}`, `[\p{Nope}]`, `\p{Script=Nonesuch}`} {
		if _, err := ecma262.Compile(p, flags.Unicode); err == nil {
			t.Errorf("expected compile error for %s", p)
		}
	}
}

// Scripts and the previously-supported aliases still work.
func TestAudit_UnicodePropertyScriptsAndAliases(t *testing.T) {
	han := ecma262.MustCompile(`\p{Script=Han}`, flags.Unicode)
	if !han.MatchString("中") {
		t.Error(`\p{Script=Han} should match 中`)
	}
	if han.MatchString("a") {
		t.Error(`\p{Script=Han} should not match 'a'`)
	}
	ascii := ecma262.MustCompile(`\p{ASCII}`, flags.Unicode)
	if !ascii.MatchString("a") || ascii.MatchString("中") {
		t.Error(`\p{ASCII} membership wrong`)
	}
}
