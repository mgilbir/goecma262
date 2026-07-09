package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// C5: a zero-width match ahead of the cursor must not duplicate text or
// produce phantom matches in the iterating methods.
func TestAudit_ZeroWidthMatchIteration(t *testing.T) {
	re := ecma262.MustCompile(`(?=x)`, flags.Global)

	if got := re.ReplaceAllString("abx", "-"); got != "ab-x" {
		t.Errorf("ReplaceAllString = %q, want %q", got, "ab-x")
	}
	if got := re.FindAllString("abx", -1); len(got) != 1 {
		t.Errorf("FindAllString len = %d (%q), want 1", len(got), got)
	}
	if got := re.Split("abx", -1); len(got) != 2 || got[0] != "ab" || got[1] != "x" {
		t.Errorf("Split = %q, want [ab x]", got)
	}
}

// Zero-width at every position: /a*/g on "aa" yields the trailing empty match.
func TestAudit_ZeroWidthTrailingMatch(t *testing.T) {
	re := ecma262.MustCompile(`a*`, flags.Global)
	got := re.FindAllString("aa", -1)
	if len(got) != 2 || got[0] != "aa" || got[1] != "" {
		t.Errorf("FindAllString(/a*/g, \"aa\") = %q, want [aa \"\"]", got)
	}
}

// C12: ReplaceAllStringFunc must respect the global flag like ReplaceAllString.
func TestAudit_ReplaceFuncRespectsGlobal(t *testing.T) {
	nonGlobal := ecma262.MustCompile(`\d`, flags.Flags(0))
	if got := nonGlobal.ReplaceAllStringFunc("a1b2", func(string) string { return "X" }); got != "aXb2" {
		t.Errorf("non-global ReplaceAllStringFunc = %q, want %q", got, "aXb2")
	}
	global := ecma262.MustCompile(`\d`, flags.Global)
	if got := global.ReplaceAllStringFunc("a1b2", func(string) string { return "X" }); got != "aXbX" {
		t.Errorf("global ReplaceAllStringFunc = %q, want %q", got, "aXbX")
	}
}

// C13: $0 is a literal, not a whole-match reference; $00 likewise.
func TestAudit_DollarZeroLiteral(t *testing.T) {
	re := ecma262.MustCompile(`\d+`, flags.Flags(0))
	if got := re.ReplaceAllString("a1", "$0"); got != "a$0" {
		t.Errorf("$0 replacement = %q, want %q", got, "a$0")
	}
	// $1 is a real reference; $12 with only one group is group 1 + literal "2".
	re2 := ecma262.MustCompile(`(\d)`, flags.Flags(0))
	if got := re2.ReplaceAllString("a1", "$12"); got != "a12" {
		t.Errorf("$12 (one group) = %q, want %q", got, "a12")
	}
}
