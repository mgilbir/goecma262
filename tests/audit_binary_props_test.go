package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// #4: broaden Unicode binary-property coverage — aliases onto Go's property
// tables and computed derived properties.
func TestAudit_BinaryProperties(t *testing.T) {
	cases := []struct {
		prop  string
		char  string
		match bool
	}{
		{`\p{White_Space}`, " ", true},
		{`\p{space}`, "\t", true},       // alias
		{`\p{Hex_Digit}`, "F", true},    // canonical
		{`\p{AHex}`, "f", true},         // ASCII_Hex_Digit alias
		{`\p{Dash}`, "-", true},         // canonical table
		{`\p{Diacritic}`, "^", true},    // circumflex is a diacritic
		{`\p{Alphabetic}`, "A", true},   // computed
		{`\p{Alpha}`, "中", true},        // computed, alias
		{`\p{Math}`, "+", true},         // Sm + Other_Math
		{`\p{ID_Start}`, "x", true},     // computed
		{`\p{ID_Continue}`, "9", true},  // computed
		{`\p{Cased}`, "a", true},        // computed
		{`\p{Cased}`, "5", false},       // digit is not cased
		{`\p{White_Space}`, "x", false}, // negative
		{`\p{Ideographic}`, "中", true},  // canonical table
		// Emoji properties, from tools/genemoji (expectations from node)
		{`\p{Emoji}`, "#", true},
		{`\p{Emoji}`, "\u00a9", true},
		{`\p{Emoji}`, "a", false},
		{`\p{Emoji}`, "\U0001f600", true},
		{`\p{EPres}`, "\u00a9", false},
		{`\p{Emoji_Presentation}`, "\U0001f600", true},
		{`\p{EComp}`, "\u200d", true},
		{`\p{Emoji_Component}`, "\U0001f1e6", true},
		{`\p{EMod}`, "\U0001f3fb", true},
		{`\p{Emoji_Modifier}`, "a", false},
		{`\p{EBase}`, "\U0001f44b", true},
		{`\p{Emoji_Modifier_Base}`, "\U0001f600", false},
		{`\p{ExtPict}`, "\u00a9", true},
		{`\p{Extended_Pictographic}`, "#", false},
		{`\p{Extended_Pictographic}`, "\U0001fffd", true},
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

// \P negation and use inside a character class both work with the broader set.
func TestAudit_BinaryPropertiesNegationAndClass(t *testing.T) {
	if !ecma262.MustCompile(`\P{White_Space}`, flags.Unicode).MatchString("x") {
		t.Error(`\P{White_Space} should match "x"`)
	}
	if !ecma262.MustCompile(`[\p{Hex_Digit}]+`, flags.Unicode).MatchString("dead") {
		t.Error(`[\p{Hex_Digit}]+ should match "dead"`)
	}
}

// Unknown properties remain a compile error rather than silently matching
// nothing, and so do properties of strings outside the v flag.
func TestAudit_UnsupportedPropertyStillErrors(t *testing.T) {
	for _, p := range []string{`\p{Bogus}`, `\p{RGI_Emoji}`, `\p{Basic_Emoji}`} {
		if _, err := ecma262.Compile(p, flags.Unicode); err == nil {
			t.Errorf("expected compile error for %s", p)
		}
	}
}
