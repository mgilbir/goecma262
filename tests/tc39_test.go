// Tests derived from tc39/test262 (https://github.com/tc39/test262)
// under the Test262 copyright and BSD-style license.
//
// Source: test/built-ins/RegExp/
//
// Each test function references the original test262 file(s) it was derived from.
package ecma262_test

import (
	"strings"
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// ---------------------------------------------------------------------------
// S15.10.2.10 – Character escape sequences: \t \n \r \f \v \x## \u####
// ---------------------------------------------------------------------------

// TC39: S15.10.2.10_A1.1_T1 – \t (tab) matches U+0009
func TestTC39CharEscape_Tab(t *testing.T) {
	re := ecma262.MustCompile(`\t`, flags.Flags(0))
	input := "\u0009" // TAB
	if !re.MatchString(input) {
		t.Errorf(`\t should match U+0009, got no match`)
	}
	got := re.FindString(input)
	if got != "\t" {
		t.Errorf(`\t FindString = %q, want "\t"`, got)
	}
}

// TC39: S15.10.2.10_A1.2_T1 – \n (newline) matches U+000A
func TestTC39CharEscape_Newline(t *testing.T) {
	re := ecma262.MustCompile(`\n`, flags.Flags(0))
	input := "\u000A"
	if !re.MatchString(input) {
		t.Errorf(`\n should match U+000A`)
	}
	got := re.FindString(input)
	if got != "\n" {
		t.Errorf(`\n FindString = %q, want "\n"`, got)
	}
}

// TC39: S15.10.2.10_A1.3_T1 – \r (carriage return) matches U+000D
func TestTC39CharEscape_CarriageReturn(t *testing.T) {
	re := ecma262.MustCompile(`\r`, flags.Flags(0))
	input := "\u000D"
	if !re.MatchString(input) {
		t.Errorf(`\r should match U+000D`)
	}
	got := re.FindString(input)
	if got != "\r" {
		t.Errorf(`\r FindString = %q, want "\r"`, got)
	}
}

// TC39: S15.10.2.10_A1.4_T1 – \f (form feed) matches U+000C
func TestTC39CharEscape_FormFeed(t *testing.T) {
	re := ecma262.MustCompile(`\f`, flags.Flags(0))
	input := "\u000C"
	if !re.MatchString(input) {
		t.Errorf(`\f should match U+000C`)
	}
	got := re.FindString(input)
	if got != "\f" {
		t.Errorf(`\f FindString = %q, want "\f"`, got)
	}
}

// TC39: S15.10.2.10_A1.5_T1 – \v (vertical tab) matches U+000B
func TestTC39CharEscape_VerticalTab(t *testing.T) {
	re := ecma262.MustCompile(`\v`, flags.Flags(0))
	input := "\u000B"
	if !re.MatchString(input) {
		t.Errorf(`\v should match U+000B`)
	}
	got := re.FindString(input)
	if got != "\v" {
		t.Errorf(`\v FindString = %q, want "\v"`, got)
	}
}

// TC39: S15.10.2.10_A3.1_T1 – hex escapes \x00 \x01 \x0A \xFF
// Note: ECMA-262 \xHH matches Unicode code point U+00HH. In Go strings,
// code points > 0x7F are encoded as multi-byte UTF-8 sequences, so the
// input must use the proper UTF-8 encoding of each code point.
func TestTC39CharEscape_HexEscapes(t *testing.T) {
	tests := []struct {
		pattern string
		input   string
	}{
		{`\x00`, "\x00"},   // U+0000 null
		{`\x01`, "\x01"},   // U+0001 SOH
		{`\x0A`, "\x0A"},   // U+000A LF
		{`\xFF`, "\u00FF"}, // U+00FF ÿ (UTF-8: 0xC3 0xBF)
	}
	for _, tt := range tests {
		re := ecma262.MustCompile(tt.pattern, flags.Flags(0))
		if !re.MatchString(tt.input) {
			t.Errorf("pattern %s should match %q", tt.pattern, tt.input)
		}
	}
}

// TC39: S15.10.2.10_A4.1_T1 – \uXXXX unicode escape matching
func TestTC39CharEscape_UnicodeEscape(t *testing.T) {
	// \u0041 = 'A', \u0042 = 'B'
	re := ecma262.MustCompile(`\u0041\u0042`, flags.Flags(0))
	if !re.MatchString("AB") {
		t.Errorf(`\u0041\u0042 should match "AB"`)
	}
}

// TC39: S15.10.2.10_A4.1_T3 – \uXXXX matching Cyrillic characters
func TestTC39CharEscape_UnicodeCyrillic(t *testing.T) {
	// \u0410 = Cyrillic А, \u0411 = Б, ... match actual Cyrillic runes
	re := ecma262.MustCompile(`\u0410`, flags.Flags(0))
	if !re.MatchString("\u0410") {
		t.Errorf(`\u0410 should match Cyrillic А (U+0410)`)
	}
}

// TC39: ES2015 – \u{...} code point escapes require Unicode mode
func TestTC39UnicodeCodePointEscape_RequiresUnicodeFlag(t *testing.T) {
	_, err := ecma262.Compile(`\u{1F600}`, flags.Flags(0))
	if err == nil {
		t.Fatal("expected error without unicode flag for \\u{...} escape")
	}
}

func TestTC39UnicodeCodePointEscape_WithUnicodeFlag(t *testing.T) {
	re, err := ecma262.Compile(`\u{1F600}`, flags.Unicode)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !re.MatchString("\U0001F600") {
		t.Error(`\\u{1F600} should match U+1F600 (😀)`)
	}
}

// ---------------------------------------------------------------------------
// S15.10.2.11 – Null escape \0 and backreferences
// ---------------------------------------------------------------------------

// TC39: S15.10.2.11_A1_T1 – \0 matches the null character U+0000
func TestTC39NullEscape(t *testing.T) {
	re := ecma262.MustCompile(`\0`, flags.Flags(0))
	input := "\x00"
	if !re.MatchString(input) {
		t.Errorf(`\0 should match null character U+0000`)
	}
	got := re.FindString(input)
	if got != "\x00" {
		t.Errorf(`\0 FindString = %q, want "\x00"`, got)
	}
}

// TC39: S15.10.2.11_A1_T6 – backreferences: (A)\1(B)\2 matches "AABB"
func TestTC39Backreference_Simple(t *testing.T) {
	re := ecma262.MustCompile(`(A)\1(B)\2`, flags.Flags(0))
	input := "AABB"
	if !re.MatchString(input) {
		t.Fatalf(`(A)\1(B)\2 should match "AABB"`)
	}
	m := re.FindStringSubmatch(input)
	if len(m) < 3 {
		t.Fatalf("expected at least 3 groups, got %d: %v", len(m), m)
	}
	if m[0] != "AABB" {
		t.Errorf("m[0] = %q, want %q", m[0], "AABB")
	}
	if m[1] != "A" {
		t.Errorf("m[1] = %q, want %q", m[1], "A")
	}
	if m[2] != "B" {
		t.Errorf("m[2] = %q, want %q", m[2], "B")
	}
}

// TC39: S15.10.2.11_A1_T8 – 10 nested groups, backreferences \1..\10
// Pattern: ((((((((((A))))))))))\1\2\3\4\5\6\7\8\9\10
// Input: "AAAAAAAAAAA" (11 A's)
func TestTC39Backreference_TenGroups(t *testing.T) {
	re := ecma262.MustCompile(`((((((((((A))))))))))\1\2\3\4\5\6\7\8\9\10`, flags.Flags(0))
	input := strings.Repeat("A", 11)
	if !re.MatchString(input) {
		t.Fatalf(`ten-group pattern should match %q`, input)
	}
	m := re.FindStringSubmatch(input)
	if len(m) < 11 {
		t.Fatalf("expected at least 11 elements, got %d: %v", len(m), m)
	}
	// m[0] = full match = 11 A's
	if m[0] != input {
		t.Errorf("m[0] = %q, want %q", m[0], input)
	}
	// All capture groups capture "A"
	for i := 1; i <= 10; i++ {
		if m[i] != "A" {
			t.Errorf("m[%d] = %q, want %q", i, m[i], "A")
		}
	}
}

// ---------------------------------------------------------------------------
// S15.10.2.12 – \w and \W exact membership
// ---------------------------------------------------------------------------

// TC39: S15.10.2.12_A3_T5 – \w matches exactly [a-zA-Z0-9_]
func TestTC39WordClass_Membership(t *testing.T) {
	re := ecma262.MustCompile(`\w`, flags.Flags(0))

	// All of these should match
	wordChars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	for _, c := range wordChars {
		if !re.MatchString(string(c)) {
			t.Errorf(`\w should match %q`, string(c))
		}
	}

	// None of these should match
	nonWordChars := "!@#$%^&*()-+=[]{};':\",./<>? \t\n\r"
	for _, c := range nonWordChars {
		if re.MatchString(string(c)) {
			t.Errorf(`\w should NOT match %q`, string(c))
		}
	}
}

// TC39: S15.10.2.12_A4_T5 – \W matches exactly [^a-zA-Z0-9_]
func TestTC39NonWordClass_Membership(t *testing.T) {
	re := ecma262.MustCompile(`\W`, flags.Flags(0))

	// These should NOT match \W (they are word chars)
	wordChars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	for _, c := range wordChars {
		if re.MatchString(string(c)) {
			t.Errorf(`\W should NOT match word char %q`, string(c))
		}
	}

	// These should match \W
	nonWordChars := "!@#$%^&*()-+={}|;:',.<>? \t\n\r"
	for _, c := range nonWordChars {
		if !re.MatchString(string(c)) {
			t.Errorf(`\W should match non-word char %q`, string(c))
		}
	}
}

// \w in ECMA-262 does NOT match Unicode letters (unlike \p{L})
func TestTC39WordClass_NotUnicode(t *testing.T) {
	re := ecma262.MustCompile(`\w`, flags.Flags(0))
	unicodeLetters := []string{
		"\u00E9", // é
		"\u00C0", // À
		"\u0410", // Cyrillic А
		"\u4E2D", // 中 (CJK)
	}
	for _, s := range unicodeLetters {
		if re.MatchString(s) {
			t.Errorf(`\w should NOT match Unicode letter %q`, s)
		}
	}
}

// ---------------------------------------------------------------------------
// CharacterClassEscapes – \d, \D, \s, \S, \w, \W (positive and negative cases)
// ---------------------------------------------------------------------------

// TC39: character-class-digit-class-escape-positive-cases.js
// \d matches exactly the 10 ASCII digits 0x30-0x39
func TestTC39DigitClass_PositiveCases(t *testing.T) {
	re := ecma262.MustCompile(`\d`, flags.Flags(0))
	for r := rune('0'); r <= '9'; r++ {
		if !re.MatchString(string(r)) {
			t.Errorf(`\d should match %q (U+%04X)`, string(r), r)
		}
	}
}

// TC39: character-class-digit-class-escape-negative-cases.js
// \d must NOT match non-ASCII "digit-like" Unicode code points (e.g. Arabic-Indic digits)
func TestTC39DigitClass_NegativeCases(t *testing.T) {
	re := ecma262.MustCompile(`\d`, flags.Flags(0))

	// A selection of Unicode decimal digits that are NOT in [0-9]
	nonASCIIDigits := []rune{
		'\u0660', // Arabic-Indic digit zero
		'\u0661', // Arabic-Indic digit one
		'\u06F0', // Extended Arabic-Indic digit zero
		'\u0966', // Devanagari digit zero
		'\u09E6', // Bengali digit zero
		'\uFF10', // Fullwidth digit zero
	}
	for _, r := range nonASCIIDigits {
		if re.MatchString(string(r)) {
			t.Errorf(`\d should NOT match non-ASCII digit U+%04X`, r)
		}
	}

	// Letters, punctuation etc. also must not match
	for _, s := range []string{"a", "Z", "!", " ", "\t"} {
		if re.MatchString(s) {
			t.Errorf(`\d should NOT match %q`, s)
		}
	}
}

// TC39: character-class-non-digit-class-escape-positive-cases.js
// \D matches everything that is not an ASCII digit
func TestTC39NonDigitClass_PositiveCases(t *testing.T) {
	re := ecma262.MustCompile(`\D`, flags.Flags(0))
	nonDigits := []string{"a", "Z", "_", "!", " ", "\t", "\n", "\u00E9", "\u0410"}
	for _, s := range nonDigits {
		if !re.MatchString(s) {
			t.Errorf(`\D should match %q`, s)
		}
	}
}

// TC39: character-class-non-digit-class-escape-negative-cases.js
// \D must NOT match ASCII digits
func TestTC39NonDigitClass_NegativeCases(t *testing.T) {
	re := ecma262.MustCompile(`\D`, flags.Flags(0))
	for r := rune('0'); r <= '9'; r++ {
		if re.MatchString(string(r)) {
			t.Errorf(`\D should NOT match ASCII digit %q`, string(r))
		}
	}
}

// TC39: character-class-space-class-escape-positive-cases.js
// \s matches the ECMA-262 WhiteSpace and LineTerminator set
func TestTC39SpaceClass_PositiveCases(t *testing.T) {
	re := ecma262.MustCompile(`\s`, flags.Flags(0))
	// WhiteSpace: TAB, VT, FF, SP, NBSP, BOM, ZWNBSP, and other Unicode spaces
	// LineTerminator: LF, CR, LS, PS
	whitespace := []rune{
		'\u0009', // TAB
		'\u000A', // LF
		'\u000B', // VT
		'\u000C', // FF
		'\u000D', // CR
		'\u0020', // SPACE
		'\u00A0', // NBSP
		'\u2028', // LINE SEPARATOR
		'\u2029', // PARAGRAPH SEPARATOR
		'\uFEFF', // BOM / ZWNBSP
	}
	for _, r := range whitespace {
		if !re.MatchString(string(r)) {
			t.Errorf(`\s should match U+%04X`, r)
		}
	}
}

// TC39: character-class-space-class-escape-negative-cases.js
// \s must NOT match ASCII non-whitespace characters
func TestTC39SpaceClass_NegativeCases(t *testing.T) {
	re := ecma262.MustCompile(`\s`, flags.Flags(0))
	nonSpace := []string{"a", "Z", "0", "9", "_", "!", "@", "#"}
	for _, s := range nonSpace {
		if re.MatchString(s) {
			t.Errorf(`\s should NOT match %q`, s)
		}
	}
}

// TC39: character-class-non-space-class-escape-positive-cases.js
// \S matches non-whitespace
func TestTC39NonSpaceClass_PositiveCases(t *testing.T) {
	re := ecma262.MustCompile(`\S`, flags.Flags(0))
	nonSpace := []string{"a", "Z", "0", "_", "!", "@"}
	for _, s := range nonSpace {
		if !re.MatchString(s) {
			t.Errorf(`\S should match %q`, s)
		}
	}
}

// TC39: character-class-non-space-class-escape-negative-cases.js
// \S must NOT match whitespace
func TestTC39NonSpaceClass_NegativeCases(t *testing.T) {
	re := ecma262.MustCompile(`\S`, flags.Flags(0))
	space := []rune{'\u0009', '\u000A', '\u000B', '\u000C', '\u000D', '\u0020', '\u00A0'}
	for _, r := range space {
		if re.MatchString(string(r)) {
			t.Errorf(`\S should NOT match whitespace U+%04X`, r)
		}
	}
}

// TC39: character-class-word-class-escape-positive-cases.js
// \w matches [a-zA-Z0-9_]
func TestTC39WordClassEscape_PositiveCases(t *testing.T) {
	re := ecma262.MustCompile(`\w`, flags.Flags(0))
	wordChars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	for _, c := range wordChars {
		if !re.MatchString(string(c)) {
			t.Errorf(`\w should match %q`, string(c))
		}
	}
}

// TC39: character-class-word-class-escape-negative-cases.js
// \w must NOT match non-word ASCII characters
func TestTC39WordClassEscape_NegativeCases(t *testing.T) {
	re := ecma262.MustCompile(`\w`, flags.Flags(0))
	// A broad set of non-word chars (punctuation, whitespace, control, non-ASCII)
	nonWord := []rune{
		'\u0020', '\u0021', '\u0022', '\u0023', '\u0024', '\u0025', '\u0026',
		'\u0027', '\u0028', '\u0029', '\u002A', '\u002B', '\u002C', '\u002D',
		'\u002E', '\u002F',
		// After 0-9 (0x30-0x39) and before uppercase (0x3A-0x40)
		'\u003A', '\u003B', '\u003C', '\u003D', '\u003E', '\u003F', '\u0040',
		// After uppercase Z (0x5B-0x5E), skip underscore (0x5F), after uppercase
		'\u005B', '\u005C', '\u005D', '\u005E',
		// After lowercase z (0x7B-0x7E)
		'\u007B', '\u007C', '\u007D', '\u007E',
		// Control and non-ASCII
		'\u0009', '\u000A', '\u000D', '\u00B5',
	}
	for _, r := range nonWord {
		if re.MatchString(string(r)) {
			t.Errorf(`\w should NOT match U+%04X`, r)
		}
	}
}

// TC39: character-class-non-word-class-escape-positive-cases.js
// \W matches non-word characters
func TestTC39NonWordClassEscape_PositiveCases(t *testing.T) {
	re := ecma262.MustCompile(`\W`, flags.Flags(0))
	nonWord := []string{"!", " ", "\t", "\n", "@", "#", "(", ")", "\u00B5"}
	for _, s := range nonWord {
		if !re.MatchString(s) {
			t.Errorf(`\W should match %q`, s)
		}
	}
}

// TC39: character-class-non-word-class-escape-negative-cases.js
// \W must NOT match [a-zA-Z0-9_]
func TestTC39NonWordClassEscape_NegativeCases(t *testing.T) {
	re := ecma262.MustCompile(`\W`, flags.Flags(0))
	wordChars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	for _, c := range wordChars {
		if re.MatchString(string(c)) {
			t.Errorf(`\W should NOT match word char %q`, string(c))
		}
	}
}

// ---------------------------------------------------------------------------
// named-groups – capture, SubexpNames, SubexpIndex, FindStringSubmatch
// ---------------------------------------------------------------------------

// TC39: named-groups/unicode-match.js – basic named group capture with exec
func TestTC39NamedGroups_BasicMatch(t *testing.T) {
	re, err := ecma262.Compile(`(?<year>\d{4})-(?<month>\d{2})-(?<day>\d{2})`, flags.Unicode)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	input := "2015-01-02"
	m := re.FindStringSubmatch(input)
	if m == nil {
		t.Fatal("expected match")
	}
	// full match
	if m[0] != "2015-01-02" {
		t.Errorf("m[0] = %q, want %q", m[0], "2015-01-02")
	}
	// named groups by index
	if m[1] != "2015" {
		t.Errorf("year = %q, want %q", m[1], "2015")
	}
	if m[2] != "01" {
		t.Errorf("month = %q, want %q", m[2], "01")
	}
	if m[3] != "02" {
		t.Errorf("day = %q, want %q", m[3], "02")
	}
}

// TC39: named-groups/groups-properties.js – SubexpNames and SubexpIndex
func TestTC39NamedGroups_SubexpNames(t *testing.T) {
	re, err := ecma262.Compile(`(?<foo>a)(?<bar>b)`, flags.Unicode)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	names := re.SubexpNames()
	// names[0] is always "" (full match), names[1] = "foo", names[2] = "bar"
	if len(names) < 3 {
		t.Fatalf("SubexpNames len = %d, want >= 3; names = %v", len(names), names)
	}
	if names[0] != "" {
		t.Errorf("names[0] = %q, want %q", names[0], "")
	}
	if names[1] != "foo" {
		t.Errorf("names[1] = %q, want %q", names[1], "foo")
	}
	if names[2] != "bar" {
		t.Errorf("names[2] = %q, want %q", names[2], "bar")
	}

	if re.SubexpIndex("foo") != 1 {
		t.Errorf("SubexpIndex(foo) = %d, want 1", re.SubexpIndex("foo"))
	}
	if re.SubexpIndex("bar") != 2 {
		t.Errorf("SubexpIndex(bar) = %d, want 2", re.SubexpIndex("bar"))
	}
	if re.SubexpIndex("baz") != -1 {
		t.Errorf("SubexpIndex(baz) = %d, want -1", re.SubexpIndex("baz"))
	}
}

// TC39: named-groups/non-unicode-match.js – named groups without u flag
func TestTC39NamedGroups_NonUnicode(t *testing.T) {
	re, err := ecma262.Compile(`(?<year>\d{4})-(?<month>\d{2})-(?<day>\d{2})`, flags.Flags(0))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	input := "2015-01-02"
	m := re.FindStringSubmatch(input)
	if m == nil {
		t.Fatal("expected match")
	}
	if m[0] != "2015-01-02" {
		t.Errorf("m[0] = %q, want %q", m[0], "2015-01-02")
	}
	if m[1] != "2015" {
		t.Errorf("year = %q, want %q", m[1], "2015")
	}
	if m[2] != "01" {
		t.Errorf("month = %q, want %q", m[2], "01")
	}
	if m[3] != "02" {
		t.Errorf("day = %q, want %q", m[3], "02")
	}
}

// TC39: named-groups/string-replace-get.js – $<name> replacement
func TestTC39NamedGroups_Replace(t *testing.T) {
	re, err := ecma262.Compile(`(?<year>\d{4})-(?<month>\d{2})-(?<day>\d{2})`, flags.Unicode)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	result := re.ReplaceAllString("2015-01-02", "$<day>/$<month>/$<year>")
	if result != "02/01/2015" {
		t.Errorf("ReplaceAllString = %q, want %q", result, "02/01/2015")
	}
}

// TC39: named-groups/string-replace-missing.js – $<name> for missing group → ""
func TestTC39NamedGroups_ReplaceMissingName(t *testing.T) {
	re, err := ecma262.Compile(`(?<foo>a)`, flags.Unicode)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// $<bar> refers to a group that doesn't exist; should expand to ""
	result := re.ReplaceAllString("a", "$<foo>-$<bar>")
	if result != "a-" {
		t.Errorf("ReplaceAllString = %q, want %q", result, "a-")
	}
}

// TC39: named-groups/unicode-references.js – \k<name> backreference (unicode)
func TestTC39NamedGroups_Backreference_Unicode(t *testing.T) {
	re, err := ecma262.Compile(`(?<dup>a)\k<dup>`, flags.Unicode)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !re.MatchString("aa") {
		t.Error(`(?<dup>a)\k<dup> should match "aa"`)
	}
	if re.MatchString("ab") {
		t.Error(`(?<dup>a)\k<dup> should NOT match "ab"`)
	}
}

// TC39: named-groups/non-unicode-references.js – \k<name> backreference (no u flag)
func TestTC39NamedGroups_Backreference_NonUnicode(t *testing.T) {
	re, err := ecma262.Compile(`(?<word>\w+)\s+\k<word>`, flags.Flags(0))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !re.MatchString("hello hello") {
		t.Error(`(?<word>\w+)\s+\k<word> should match "hello hello"`)
	}
	if re.MatchString("hello world") {
		t.Error(`(?<word>\w+)\s+\k<word> should NOT match "hello world"`)
	}
}

// TC39: named-groups/lookbehind.js – named groups inside lookbehind
func TestTC39NamedGroups_Lookbehind(t *testing.T) {
	// Fixed-length lookbehind with a named group
	re, err := ecma262.Compile(`(?<=(?<prefix>ab))c`, flags.Unicode)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !re.MatchString("abc") {
		t.Error(`(?<=(?<prefix>ab))c should match in "abc"`)
	}
	if re.MatchString("xc") {
		t.Error(`(?<=(?<prefix>ab))c should NOT match in "xc"`)
	}
}

// TC39: named-groups/duplicate-names-permitted.js variant – multiple alts with same group name
// ECMA-2022 allows duplicate named groups in different alternatives
func TestTC39NamedGroups_DuplicateNamesInAlternatives(t *testing.T) {
	// In ECMA-2022, (?<a>x)|(?<a>y) is valid; names are duplicated across alternatives.
	re, err := ecma262.Compile(`(?<a>x)|(?<a>y)`, flags.Unicode)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !re.MatchString("x") {
		t.Error("expected match for 'x'")
	}
	if !re.MatchString("y") {
		t.Error("expected match for 'y'")
	}
}

func TestTC39NamedGroups_DuplicateNamesSameAlternative_Error(t *testing.T) {
	_, err := ecma262.Compile(`(?<a>x)(?<a>y)`, flags.Unicode)
	if err == nil {
		t.Fatal("expected duplicate named group error in same alternative")
	}
}

func TestTC39NamedGroups_UnicodeNameRequiresUnicodeFlag(t *testing.T) {
	_, err := ecma262.Compile(`(?<π>a)`, flags.Flags(0))
	if err == nil {
		t.Fatal("expected error for unicode group name without unicode flag")
	}
}

func TestTC39NamedGroups_UnicodeNameWithUnicodeFlag(t *testing.T) {
	re, err := ecma262.Compile(`(?<π>a)`, flags.Unicode)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !re.MatchString("a") {
		t.Error("expected match for unicode group name pattern")
	}
}

// ---------------------------------------------------------------------------
// named-groups – FindAllStringSubmatch with named groups
// ---------------------------------------------------------------------------

// TC39: named-groups/groups-object.js – multiple matches preserve group names
func TestTC39NamedGroups_FindAll(t *testing.T) {
	re, err := ecma262.Compile(`(?<n>\d+)`, flags.Unicode)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	input := "a1b22c333"
	all := re.FindAllStringSubmatch(input, -1)
	if len(all) != 3 {
		t.Fatalf("FindAllStringSubmatch returned %d matches, want 3; %v", len(all), all)
	}
	expected := []string{"1", "22", "333"}
	for i, m := range all {
		if m[0] != expected[i] {
			t.Errorf("match[%d][0] = %q, want %q", i, m[0], expected[i])
		}
		if m[1] != expected[i] {
			t.Errorf("match[%d][1] = %q, want %q", i, m[1], expected[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Lookahead / Lookbehind with named groups (from named-groups tests)
// ---------------------------------------------------------------------------

// TC39: named-groups/unicode-match.js lookahead variant
func TestTC39NamedGroups_Lookahead(t *testing.T) {
	re, err := ecma262.Compile(`(?<word>\w+)(?=\s)`, flags.Unicode)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	m := re.FindStringSubmatch("hello world")
	if m == nil {
		t.Fatal("expected match")
	}
	if m[0] != "hello" {
		t.Errorf("m[0] = %q, want %q", m[0], "hello")
	}
	if m[1] != "hello" {
		t.Errorf("m[1] (word group) = %q, want %q", m[1], "hello")
	}
}

// ---------------------------------------------------------------------------
// Case-insensitive matching (i flag)
// ---------------------------------------------------------------------------

// TC39: S15.10.2.8 – case-insensitive matching
func TestTC39CaseInsensitive_BasicMatch(t *testing.T) {
	tests := []struct {
		pattern string
		input   string
		want    bool
	}{
		{`abc`, "ABC", true},
		{`ABC`, "abc", true},
		{`[a-z]`, "A", true},
		{`[A-Z]`, "a", true},
		{`\w`, "A", true},
	}
	for _, tt := range tests {
		re := ecma262.MustCompile(tt.pattern, flags.IgnoreCase)
		got := re.MatchString(tt.input)
		if got != tt.want {
			t.Errorf("/%s/i.MatchString(%q) = %v, want %v", tt.pattern, tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Multiline flag (m flag) – ^ and $ match line boundaries
// ---------------------------------------------------------------------------

// TC39: S15.10.2.8 multiline anchors
func TestTC39Multiline_Anchors(t *testing.T) {
	re := ecma262.MustCompile(`^\d+$`, flags.Multiline)

	lines := "abc\n123\ndef"
	if !re.MatchString(lines) {
		t.Errorf(`^\d+$ with m flag should match a line of digits in %q`, lines)
	}

	all := re.FindAllString(lines, -1)
	if len(all) != 1 || all[0] != "123" {
		t.Errorf("FindAllString = %v, want [\"123\"]", all)
	}
}

// ---------------------------------------------------------------------------
// Numeric backreferences with case-insensitive flag
// ---------------------------------------------------------------------------

// TC39: S15.10.2.11 – backreference case folding
func TestTC39Backreference_CaseInsensitive(t *testing.T) {
	re := ecma262.MustCompile(`(a)\1`, flags.IgnoreCase)
	// "aA" – first group captures 'a', backreference must match 'A' case-insensitively
	if !re.MatchString("aA") {
		t.Errorf(`(a)\1 /i should match "aA"`)
	}
	if !re.MatchString("Aa") {
		t.Errorf(`(a)\1 /i should match "Aa"`)
	}
}

// ---------------------------------------------------------------------------
// Dotall flag (s flag) – dot matches newline
// ---------------------------------------------------------------------------

// TC39: s flag behavior
func TestTC39DotAll_DotMatchesNewline(t *testing.T) {
	re := ecma262.MustCompile(`a.b`, flags.DotAll)
	if !re.MatchString("a\nb") {
		t.Error(`a.b with /s flag should match "a\nb"`)
	}
}

func TestTC39DotAll_WithoutFlag(t *testing.T) {
	re := ecma262.MustCompile(`a.b`, flags.Flags(0))
	if re.MatchString("a\nb") {
		t.Error(`a.b without /s flag should NOT match "a\nb"`)
	}
}

// ---------------------------------------------------------------------------
// CompileFlags convenience function
// ---------------------------------------------------------------------------

// TC39: flags parsing via string
func TestTC39CompileFlags_ValidFlags(t *testing.T) {
	tests := []struct {
		pattern   string
		flagStr   string
		input     string
		wantMatch bool
	}{
		{`abc`, "i", "ABC", true},
		{`^abc`, "m", "x\nabc", true},
		{`a.b`, "s", "a\nb", true},
		{`[a-z]+`, "g", "hello", true},
	}
	for _, tt := range tests {
		re, err := ecma262.CompileFlags(tt.pattern, tt.flagStr)
		if err != nil {
			t.Errorf("CompileFlags(%q, %q): %v", tt.pattern, tt.flagStr, err)
			continue
		}
		got := re.MatchString(tt.input)
		if got != tt.wantMatch {
			t.Errorf("CompileFlags(%q, %q).MatchString(%q) = %v, want %v",
				tt.pattern, tt.flagStr, tt.input, got, tt.wantMatch)
		}
	}
}

func TestTC39CompileFlags_InvalidFlag(t *testing.T) {
	_, err := ecma262.CompileFlags(`abc`, "z")
	if err == nil {
		t.Error("CompileFlags with invalid flag 'z' should return error")
	}
}

func TestTC39CompileFlags_DuplicateFlag(t *testing.T) {
	_, err := ecma262.CompileFlags(`abc`, "ii")
	if err == nil {
		t.Error("CompileFlags with duplicate flag 'ii' should return error")
	}
}

func TestTC39CompileFlags_IncompatibleFlags(t *testing.T) {
	_, err := ecma262.CompileFlags(`abc`, "uv")
	if err == nil {
		t.Error("CompileFlags with 'uv' should return error (incompatible)")
	}
}

// ---------------------------------------------------------------------------
// NumSubexp
// ---------------------------------------------------------------------------

func TestTC39NumSubexp(t *testing.T) {
	tests := []struct {
		pattern string
		want    int
	}{
		{`abc`, 0},
		{`(a)`, 1},
		{`(a)(b)`, 2},
		{`((a)(b))`, 3},
		{`(?:a)`, 0},
		{`(?<name>a)`, 1},
	}
	for _, tt := range tests {
		re := ecma262.MustCompile(tt.pattern, flags.Flags(0))
		got := re.NumSubexp()
		if got != tt.want {
			t.Errorf("/%s/ NumSubexp() = %d, want %d", tt.pattern, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// String method
// ---------------------------------------------------------------------------

func TestTC39String(t *testing.T) {
	pattern := `(?<foo>\w+)`
	re := ecma262.MustCompile(pattern, flags.Flags(0))
	if re.String() != pattern {
		t.Errorf("String() = %q, want %q", re.String(), pattern)
	}
}

// ---------------------------------------------------------------------------
// ReplaceAllStringFunc – function-based replacement
// ---------------------------------------------------------------------------

// TC39 covers String.prototype.replace with function argument
func TestTC39ReplaceAllStringFunc(t *testing.T) {
	re := ecma262.MustCompile(`\d+`, flags.Flags(0))
	result := re.ReplaceAllStringFunc("a1b22c333", func(s string) string {
		return "[" + s + "]"
	})
	if result != "a[1]b[22]c[333]" {
		t.Errorf("ReplaceAllStringFunc = %q, want %q", result, "a[1]b[22]c[333]")
	}
}

// ---------------------------------------------------------------------------
// $ replacement syntax (S15.5.4.11 / ECMA-262 String.prototype.replace)
// ---------------------------------------------------------------------------

func TestTC39Replace_DollarAmpersand(t *testing.T) {
	re := ecma262.MustCompile(`\d+`, flags.Flags(0))
	result := re.ReplaceAllString("abc123def", "[$&]")
	if result != "abc[123]def" {
		t.Errorf("$& replacement = %q, want %q", result, "abc[123]def")
	}
}

func TestTC39Replace_DollarDollar(t *testing.T) {
	re := ecma262.MustCompile(`\d+`, flags.Flags(0))
	result := re.ReplaceAllString("abc123def", "$$")
	if result != "abc$def" {
		t.Errorf("$$ replacement = %q, want %q", result, "abc$def")
	}
}

func TestTC39Replace_DollarBacktick(t *testing.T) {
	re := ecma262.MustCompile(`\d+`, flags.Flags(0))
	result := re.ReplaceAllString("abc123def", "$`")
	if result != "abcabcdef" {
		t.Errorf("$` replacement = %q, want %q", result, "abcabcdef")
	}
}

func TestTC39Replace_DollarQuote(t *testing.T) {
	re := ecma262.MustCompile(`\d+`, flags.Flags(0))
	result := re.ReplaceAllString("abc123def", "$'")
	if result != "abcdefdef" {
		t.Errorf("$' replacement = %q, want %q", result, "abcdefdef")
	}
}

func TestTC39Replace_DollarN(t *testing.T) {
	re := ecma262.MustCompile(`(\w+)\s+(\w+)`, flags.Flags(0))
	result := re.ReplaceAllString("hello world", "$2 $1")
	if result != "world hello" {
		t.Errorf("$n replacement = %q, want %q", result, "world hello")
	}
}

// ---------------------------------------------------------------------------
// Lookahead and negative lookahead (S15.10.2.8)
// ---------------------------------------------------------------------------

func TestTC39Lookahead_Positive(t *testing.T) {
	re := ecma262.MustCompile(`\d+(?= dollars)`, flags.Flags(0))
	m := re.FindString("100 dollars")
	if m != "100" {
		t.Errorf("positive lookahead FindString = %q, want %q", m, "100")
	}
	if re.MatchString("100 euros") {
		t.Error("positive lookahead should NOT match '100 euros'")
	}
}

func TestTC39Lookahead_Negative(t *testing.T) {
	re := ecma262.MustCompile(`\d+(?! dollars)`, flags.Flags(0))
	if !re.MatchString("100 euros") {
		t.Error("negative lookahead should match '100 euros'")
	}
}

func TestTC39Lookbehind_Positive(t *testing.T) {
	re := ecma262.MustCompile(`(?<=\$)\d+`, flags.Flags(0))
	m := re.FindString("$100")
	if m != "100" {
		t.Errorf("positive lookbehind FindString = %q, want %q", m, "100")
	}
	if re.MatchString("€100") {
		t.Error("positive lookbehind should NOT match '€100'")
	}
}

func TestTC39Lookbehind_Negative(t *testing.T) {
	re := ecma262.MustCompile(`(?<!\$)\d+`, flags.Flags(0))
	if !re.MatchString("€100") {
		t.Error("negative lookbehind should match '€100'")
	}
}

// ---------------------------------------------------------------------------
// Unicode property escapes \p{} and \P{} (u flag)
// ---------------------------------------------------------------------------

func TestTC39UnicodeProperty_Letter(t *testing.T) {
	re := ecma262.MustCompile(`\p{L}`, flags.Unicode)
	if !re.MatchString("a") {
		t.Error(`\p{L} should match ASCII letter 'a'`)
	}
	if !re.MatchString("\u0410") { // Cyrillic А
		t.Error(`\p{L} should match Cyrillic А`)
	}
	if re.MatchString("1") {
		t.Error(`\p{L} should NOT match digit '1'`)
	}
}

func TestTC39UnicodeProperty_NegLetter(t *testing.T) {
	re := ecma262.MustCompile(`\P{L}`, flags.Unicode)
	if !re.MatchString("1") {
		t.Error(`\P{L} should match digit '1'`)
	}
	if re.MatchString("a") {
		t.Error(`\P{L} should NOT match letter 'a'`)
	}
}

func TestTC39UnicodeProperty_DigitAliases(t *testing.T) {
	re := ecma262.MustCompile(`^\p{digit}+$`, flags.Unicode)
	if !re.MatchString("42") {
		t.Error(`\p{digit} should match ASCII digits`)
	}
	if !re.MatchString("\u09EA\u09E8") { // Bengali digits ৪২
		t.Error(`\p{digit} should match Bengali digits`)
	}
	if re.MatchString("-%#") {
		t.Error(`\p{digit} should NOT match punctuation`)
	}

	re = ecma262.MustCompile(`^\p{Nd}+$`, flags.Unicode)
	if !re.MatchString("\u09EA\u09E8") {
		t.Error(`\p{Nd} should match Bengali digits`)
	}

	re = ecma262.MustCompile(`^\p{General_Category=Decimal_Number}+$`, flags.Unicode)
	if !re.MatchString("\u09EA\u09E8") {
		t.Error(`\p{General_Category=Decimal_Number} should match Bengali digits`)
	}
}

func TestTC39UnicodeProperty_RequiresUnicodeFlag(t *testing.T) {
	re := ecma262.MustCompile(`^\p+$`, flags.Flags(0))
	if !re.MatchString("ppp") {
		t.Error(`\p should be a literal 'p' without unicode flag`)
	}
}

// ---------------------------------------------------------------------------
// MustCompile – panic behavior
// ---------------------------------------------------------------------------

func TestTC39MustCompile_ValidPattern(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("MustCompile panicked unexpectedly: %v", r)
		}
	}()
	_ = ecma262.MustCompile(`\w+`, flags.Flags(0))
}

func TestTC39MustCompile_InvalidPattern(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustCompile should panic on invalid pattern")
		}
	}()
	_ = ecma262.MustCompile(`(unclosed`, flags.Flags(0))
}
