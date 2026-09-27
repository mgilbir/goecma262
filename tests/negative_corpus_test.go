package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// The Test262 corpus is extracted by running each test file in a JS engine and
// recording the assertions that executed. A pattern that is a SyntaxError
// throws before any assertion, so it is never captured — the generated corpus
// contains no "must not compile" cases. This hand-maintained table fills that
// gap: every pattern here must fail to compile under the given flags. Add a case
// whenever a fix makes the engine correctly reject something it used to accept.
func TestNegativeCorpus_SyntaxErrors(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		flags   flags.Flags
	}{
		// Structural
		{"unclosed group", `(abc`, flags.Flags(0)},
		{"unclosed char class", `[abc`, flags.Flags(0)},
		{"dangling backslash", `\`, flags.Flags(0)},

		// Ranges and quantifiers (C16)
		{"reversed range", `[z-a]`, flags.Flags(0)},
		{"out-of-order quantifier", `a{2,1}`, flags.Flags(0)},

		// Unicode code point escapes (C15)
		{"empty code point escape", `\u{}`, flags.Unicode},
		{"code point escape overflow", `\u{110000}`, flags.Unicode},
		{"empty code point escape in class", `[\u{}]`, flags.Unicode},

		// Unicode property escapes (C7/C19)
		{"unknown property", `\p{TotallyBogus}`, flags.Unicode},
		{"empty property", `\p{}`, flags.Unicode},
		{"unknown script", `\p{Script=Nonesuch}`, flags.Unicode},

		// Named groups
		{"duplicate name same alternative", `(?<a>x)(?<a>y)`, flags.Unicode},
		{"unknown named backreference (named groups)", `\k<missing>(?<a>x)`, flags.Flags(0)},
		{"unknown named backreference (u)", `\k<missing>`, flags.Unicode},
		{"unknown named backreference (named group before)", `(?<a>.)\k<missing>`, flags.Flags(0)},
		// \- is a ClassEscape in Unicode mode, not an IdentityEscape.
		{"escaped hyphen outside class (u)", `\-`, flags.Unicode},
		{"escaped hyphen outside class (v)", `a\-b`, flags.UnicodeSets},
		{"invalid group name", `(?<a->x)`, flags.Flags(0)},

		// Flags (C11)
		{"u and v together", `a`, flags.Unicode | flags.UnicodeSets},

		// Invalid escape in unicode mode
		{"invalid identity escape (u)", `\q`, flags.Unicode},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ecma262.Compile(tc.pattern, tc.flags); err == nil {
				t.Errorf("expected compile error for %q (flags %v)", tc.pattern, tc.flags)
			}
		})
	}

	// Errors in strict ECMA-262 that Annex B accepts (see ValidCounterparts).
	strict := []struct{ name, pattern string }{
		{"code point escape without u flag", `\u{41}`},
		{"unknown named backreference", `\k<missing>`},
	}
	for _, tc := range strict {
		t.Run(tc.name+" (strict)", func(t *testing.T) {
			if _, err := ecma262.Compile(tc.pattern, flags.Flags(0), ecma262.WithSyntax(ecma262.SyntaxStrict)); err == nil {
				t.Errorf("expected compile error for %q in strict mode", tc.pattern)
			}
		})
	}
}

// Patterns that were reported as accidentally-lenient in the audit but are in
// fact valid ECMA-262 and must keep compiling.
func TestNegativeCorpus_ValidCounterparts(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		flags   flags.Flags
	}{
		{"equal range endpoints", `[a-a]`, flags.Flags(0)},
		{"equal quantifier bounds", `a{2,2}`, flags.Flags(0)},
		{"escaped hyphen in class (u)", `[a\-z]`, flags.Unicode},
		{"valid code point escape", `\u{1F600}`, flags.Unicode},
		{"duplicate name across alternatives", `(?<a>x)|(?<a>y)`, flags.Unicode},
		{"general category Lo", `\p{Lo}`, flags.Unicode},
		{"script property", `\p{Script=Han}`, flags.Unicode},
		// Annex B: without u, \u{41} is "u" 41 times, and \k is the letter k
		// when the pattern has no named group.
		{"braces after \\u without u (Annex B)", `\u{41}`, flags.Flags(0)},
		{"\\k without named groups (Annex B)", `\k<missing>`, flags.Flags(0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ecma262.Compile(tc.pattern, tc.flags); err != nil {
				t.Errorf("expected %q to compile, got %v", tc.pattern, err)
			}
		})
	}
}
