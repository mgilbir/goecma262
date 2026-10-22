package ecma262_test

import (
	"testing"
	"unicode"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// targetUnicodeVersion is the Unicode version goecma262 implements: the one
// ES2025 engines such as V8 in node 24 ship. The library takes its case
// folding and its general category and script tables from Go's unicode
// package, which has this version from Go 1.27 (Go 1.26 has Unicode 15).
const targetUnicodeVersion = "17.0.0"

func TestUnicodeVersion(t *testing.T) {
	if unicode.Version != targetUnicodeVersion {
		t.Fatalf("Go's unicode package is Unicode %s, but goecma262 implements Unicode %s: build with Go 1.27 or later and keep go.mod's go directive at 1.27", unicode.Version, targetUnicodeVersion)
	}
}

// Characters added in Unicode 16 and 17, which Go's tables lack before 1.27.
// Expectations were taken from node 24 (V8, Unicode 17.0).
func TestUnicodeVersion_NewCharacters(t *testing.T) {
	cases := []struct {
		pattern string
		flags   flags.Flags
		input   string
		want    bool
	}{
		// U+A7CB LATIN CAPITAL LETTER RAMS HORN (Unicode 16) folds to U+0264.
		{`ɤ`, flags.Unicode | flags.IgnoreCase, "Ɤ", true},
		{"Ɤ", flags.Unicode | flags.IgnoreCase, "ɤ", true},
		{`\p{Lu}`, flags.Unicode, "Ɤ", true},
		// U+1C89 and U+1C8A, the Cyrillic Tje pair (Unicode 16).
		{`\u{1C89}`, flags.Unicode | flags.IgnoreCase, "ᲊ", true},
		// U+10D50 GARAY CAPITAL LETTER A (Unicode 16).
		{`\p{L}`, flags.Unicode, "\U00010D50", true},
		{`\p{Script=Garay}`, flags.Unicode, "\U00010D50", true},
		{`\P{Script=Garay}`, flags.Unicode, "\U00010D50", false},
		// U+11DB0 TOLONG SIKI LETTER A (Unicode 16).
		{`\p{Script=Tolong_Siki}`, flags.Unicode, "\U00011DB0", true},
	}
	for _, tc := range cases {
		re, err := ecma262.Compile(tc.pattern, tc.flags)
		if err != nil {
			t.Errorf("/%s/ (flags %v): %v", tc.pattern, tc.flags, err)
			continue
		}
		if got := re.MatchString(tc.input); got != tc.want {
			t.Errorf("/%s/ (flags %v) on %q: got %v, want %v (unicode.Version %s)", tc.pattern, tc.flags, tc.input, got, tc.want, unicode.Version)
		}
	}
}
