package ecma262_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/compiler"
	"github.com/mgilbir/goecma262/parser"
)

// A pattern refused by one of the library's own limits is valid ECMA-262, so
// its error must be told apart from a syntax error by its type, not its
// wording.
func TestLimitErrorsAreTyped(t *testing.T) {
	cases := []struct {
		name, pattern, flags string
		want                 error
	}{
		{"parser nesting", strings.Repeat("(", 201) + strings.Repeat(")", 201), "", ecma262.ErrNestingTooDeep},
		{"parser class set nesting", strings.Repeat("[", 201) + strings.Repeat("]", 201), "v", ecma262.ErrNestingTooDeep},
		// 150 groups pass the parser's limit, but each nests a sequence in
		// the compiler, which counts every node.
		{"compiler nesting", strings.Repeat("(?:a", 150) + strings.Repeat(")", 150), "", ecma262.ErrNestingTooDeep},
		{"quantifier minimum", `a{10001}`, "", ecma262.ErrQuantifierTooLarge},
		{"quantifier maximum", `a{0,10001}`, "", ecma262.ErrQuantifierTooLarge},
		{"program size", `(a{1000}){250}`, "", ecma262.ErrProgramTooLarge},
		{"program size, class strings", `(?:\p{RGI_Emoji}){1000}`, "v", ecma262.ErrProgramTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ecma262.CompileFlags(tc.pattern, tc.flags)
			if err == nil {
				t.Fatal("compiled; want a limit error")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("error %q does not wrap %v", err, tc.want)
			}
			if !errors.Is(err, ecma262.ErrLimit) {
				t.Errorf("error %q does not wrap ErrLimit", err)
			}
		})
	}
}

func TestSyntaxErrorsAreNotLimitErrors(t *testing.T) {
	for _, p := range []string{`(`, `a{2,1}`, `*`, `[b-a]`, `\p{Nope}`, `(?<a>x)(?<a>y)`, `a{99999999999}{1}`} {
		_, err := ecma262.CompileFlags(p, "u")
		if err == nil {
			t.Errorf("%q compiled; want a syntax error", p)
			continue
		}
		if errors.Is(err, ecma262.ErrLimit) {
			t.Errorf("%q: syntax error %q wraps ErrLimit", p, err)
		}
	}
}

func TestLimitSentinelsAgreeAcrossPackages(t *testing.T) {
	if compiler.ErrNestingTooDeep != parser.ErrNestingTooDeep || ecma262.ErrNestingTooDeep != parser.ErrNestingTooDeep {
		t.Error("the packages' ErrNestingTooDeep differ")
	}
	if compiler.ErrLimit != parser.ErrLimit || ecma262.ErrLimit != parser.ErrLimit {
		t.Error("the packages' ErrLimit differ")
	}
	for _, err := range []error{parser.ErrNestingTooDeep, compiler.ErrProgramTooLarge, compiler.ErrQuantifierTooLarge} {
		if !errors.Is(err, parser.ErrLimit) {
			t.Errorf("%v does not wrap ErrLimit", err)
		}
	}
}

// The messages are kept as they were before the errors were typed.
func TestLimitErrorMessages(t *testing.T) {
	cases := map[string]string{
		strings.Repeat("(", 201) + strings.Repeat(")", 201): "parse error: pattern too deeply nested (limit: 200)",
		`a{10001}`:       "compile error: quantifier minimum 10001 exceeds limit 10000",
		`(a{1000}){250}`: "compile error: compiled program too large (limit: 200000 instructions)",
	}
	for p, want := range cases {
		if _, err := ecma262.CompileFlags(p, ""); err == nil || err.Error() != want {
			t.Errorf("%.20q: error %v, want %q", p, err, want)
		}
	}
}
