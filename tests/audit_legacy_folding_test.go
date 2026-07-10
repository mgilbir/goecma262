package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// #3: non-Unicode /i now applies legacy Canonicalize (uppercase), so non-ASCII
// letters fold, matching JavaScript. Cases verified against Node.
func TestAudit_LegacyCaseFolding(t *testing.T) {
	cases := []struct {
		pattern string
		input   string
		match   bool
		note    string
	}{
		{`ä`, "Ä", true, "ä <-> Ä"},
		{`Ä`, "ä", true, "symmetric"},
		{`ç`, "Ç", true, "ç <-> Ç"},
		{`µ`, "Μ", true, "micro sign U+00B5 uppercases to Greek capital mu"},
		{`s`, "ſ", false, "ASCII guard: long s does NOT fold to s"},
		{`k`, "\u212A", false, "ASCII guard: Kelvin sign (U+212A) does NOT fold to k"},
		{`k`, "K", true, "plain ASCII K folds"},
		{`abc`, "ABC", true, "ASCII"},
	}
	for _, tc := range cases {
		re := ecma262.MustCompile(tc.pattern, flags.IgnoreCase)
		if got := re.MatchString(tc.input); got != tc.match {
			t.Errorf("/%s/i.MatchString(%q) = %v, want %v (%s)", tc.pattern, tc.input, got, tc.match, tc.note)
		}
	}
}

// Non-ASCII ranges under /i fold too, with the canonicalization guard intact.
func TestAudit_LegacyCaseFoldingRange(t *testing.T) {
	// À-Þ is uppercase Latin-1; à (lowercase) should match under /i.
	re := ecma262.MustCompile(`[À-Þ]`, flags.IgnoreCase)
	if !re.MatchString("à") {
		t.Error(`/[À-Þ]/i should match "à"`)
	}
	// ASCII mixed-case range still works (regression guard for #6/C18).
	if !ecma262.MustCompile(`[Y-b]`, flags.IgnoreCase).MatchString("y") {
		t.Error(`/[Y-b]/i should match "y"`)
	}
}

// Unicode mode keeps simple case folding (long s -> s), unaffected by this change.
func TestAudit_UnicodeFoldingUnchanged(t *testing.T) {
	if !ecma262.MustCompile(`s`, flags.IgnoreCase|flags.Unicode).MatchString("ſ") {
		t.Error(`/s/iu should match long s (U+017F)`)
	}
}
