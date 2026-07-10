package ecma262_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// C8: alternation-heavy patterns with no named groups must compile in linear
// time, not exponential.
func TestAudit_NoExponentialCompile(t *testing.T) {
	pat := strings.Repeat("(a|b)", 40)
	done := make(chan struct{})
	go func() {
		_, _ = ecma262.Compile(pat, flags.Flags(0))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("compile of 40x (a|b) did not finish in 2s (exponential validation)")
	}
}

// C9: nested counted quantifiers must be rejected rather than emitting a
// multi-million-instruction program.
func TestAudit_ProgramSizeBounded(t *testing.T) {
	if _, err := ecma262.Compile(`((a{100}){100}){100}`, flags.Flags(0)); err == nil {
		t.Fatal("expected compile error for oversized nested quantifiers")
	}
}

// C15: \u{} (empty) and \u{110000} (>0x10FFFF) are SyntaxErrors.
func TestAudit_UnicodeCodePointValidation(t *testing.T) {
	if _, err := ecma262.Compile(`\u{}`, flags.Unicode); err == nil {
		t.Error("expected error for empty \\u{}")
	}
	if _, err := ecma262.Compile(`\u{110000}`, flags.Unicode); err == nil {
		t.Error("expected error for out-of-range \\u{110000}")
	}
	if _, err := ecma262.Compile(`[\u{}]`, flags.Unicode); err == nil {
		t.Error("expected error for empty \\u{} in class")
	}
	// A valid one still compiles.
	if _, err := ecma262.Compile(`\u{1F600}`, flags.Unicode); err != nil {
		t.Errorf("valid \\u{1F600} should compile: %v", err)
	}
}

// C16: reversed ranges and out-of-order quantifiers are SyntaxErrors.
func TestAudit_RangeAndQuantifierOrder(t *testing.T) {
	if _, err := ecma262.Compile(`[z-a]`, flags.Flags(0)); err == nil {
		t.Error("expected error for reversed range [z-a]")
	}
	if _, err := ecma262.Compile(`a{2,1}`, flags.Flags(0)); err == nil {
		t.Error("expected error for out-of-order quantifier a{2,1}")
	}
	// Equal endpoints and equal bounds remain valid.
	if _, err := ecma262.Compile(`[a-a]`, flags.Flags(0)); err != nil {
		t.Errorf("[a-a] should compile: %v", err)
	}
	if _, err := ecma262.Compile(`a{2,2}`, flags.Flags(0)); err != nil {
		t.Errorf("a{2,2} should compile: %v", err)
	}
}

// C17: \- is a valid class escape under the u flag.
func TestAudit_EscapedHyphenInClassUnicode(t *testing.T) {
	re, err := ecma262.Compile(`[a\-z]`, flags.Unicode)
	if err != nil {
		t.Fatalf("[a\\-z] with u flag should compile: %v", err)
	}
	for _, s := range []string{"a", "-", "z"} {
		if !re.MatchString(s) {
			t.Errorf("[a\\-z] should match %q", s)
		}
	}
	if re.MatchString("b") {
		t.Error("[a\\-z] should not match 'b' (it is a 3-element set, not a range)")
	}
}
