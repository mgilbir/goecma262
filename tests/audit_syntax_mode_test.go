package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// C20: Annex B web-compatibility leniencies are on by default and match
// JavaScript's RegExp behavior for non-Unicode patterns.
func TestAudit_AnnexBDefault(t *testing.T) {
	cases := []struct {
		pattern string
		input   string
		match   bool
	}{
		{`\5`, "\x05", true},    // legacy octal escape
		{`\5`, "5", false},      // not the literal digit
		{`\58`, "\x058", true},  // octal \5 then literal 8
		{`\8`, "8", true},       // \8 is a literal "8"
		{`\12`, "\n", true},     // octal 012 = U+000A
		{`\777`, "\x3f7", true}, // \77 = '?' (0x3f), then literal 7
		{`a{2 x}`, "a{2 x}", true}, // malformed quantifier -> literal
		{`\c1`, `\c1`, true},       // invalid \c -> literal backslash, c, 1
		{`\cA`, "\x01", true},      // valid control escape still works
	}
	for _, tc := range cases {
		re, err := ecma262.Compile(tc.pattern, flags.Flags(0))
		if err != nil {
			t.Errorf("default Compile(%q): %v", tc.pattern, err)
			continue
		}
		if got := re.MatchString(tc.input); got != tc.match {
			t.Errorf("%q.MatchString(%q) = %v, want %v", tc.pattern, tc.input, got, tc.match)
		}
	}
	// A real backreference still works under the default.
	if !ecma262.MustCompile(`(a)\1`, flags.Flags(0)).MatchString("aa") {
		t.Error(`(a)\1 should still match "aa"`)
	}
}

// SyntaxStrict rejects the Annex B constructs as compile errors.
func TestAudit_StrictModeRejects(t *testing.T) {
	strict := ecma262.WithSyntax(ecma262.SyntaxStrict)
	for _, p := range []string{`\5`, `\8`, `a{2 x}`, `\c1`} {
		if _, err := ecma262.Compile(p, flags.Flags(0), strict); err == nil {
			t.Errorf("strict mode should reject %q", p)
		}
	}
	// Valid constructs still compile under strict mode.
	if _, err := ecma262.Compile(`(a)\1`, flags.Flags(0), strict); err != nil {
		t.Errorf(`(a)\1 should compile under strict mode: %v`, err)
	}
}

// The u flag forces strict behavior regardless of the syntax option (Annex B
// does not apply in Unicode mode).
func TestAudit_UnicodeForcesStrict(t *testing.T) {
	annexB := ecma262.WithSyntax(ecma262.SyntaxAnnexB)
	for _, p := range []string{`\5`, `\8`, `a{2 x}`, `\c1`} {
		if _, err := ecma262.Compile(p, flags.Unicode, annexB); err == nil {
			t.Errorf("u flag should reject %q even with SyntaxAnnexB", p)
		}
	}
}

// Out-of-order {n,m} is an early error in every mode (unlike a malformed brace).
func TestAudit_OutOfOrderQuantifierAlwaysErrors(t *testing.T) {
	for _, opts := range [][]ecma262.Option{
		nil,
		{ecma262.WithSyntax(ecma262.SyntaxStrict)},
		{ecma262.WithSyntax(ecma262.SyntaxAnnexB)},
	} {
		if _, err := ecma262.Compile(`a{2,1}`, flags.Flags(0), opts...); err == nil {
			t.Errorf("a{2,1} should error (opts=%v)", opts)
		}
	}
}
