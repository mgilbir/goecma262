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
		{`\p{WSpace}`, "\t", true},      // alias
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

// Still-unsupported properties (e.g. Emoji, which Go has no table for) remain a
// compile error rather than silently matching nothing.
func TestAudit_UnsupportedPropertyStillErrors(t *testing.T) {
	for _, p := range []string{`\p{Emoji}`, `\p{Bogus}`} {
		if _, err := ecma262.Compile(p, flags.Unicode); err == nil {
			t.Errorf("expected compile error for %s", p)
		}
	}
}
