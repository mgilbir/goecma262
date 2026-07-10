package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// C1: a negative lastIndex must never panic; it is treated as 0.
func TestAudit_SetLastIndexNegative_NoPanic(t *testing.T) {
	re := ecma262.MustCompile(`a`, flags.Global)
	re.SetLastIndex(-1)
	if !re.MatchString("xa") {
		t.Fatal("expected match with clamped negative lastIndex")
	}
	re.SetLastIndex(-100)
	if re.FindString("xa") != "a" {
		t.Fatal("expected FindString to work with clamped negative lastIndex")
	}
}

// C2: a lastIndex beyond the input length must yield no match, not a panic.
func TestAudit_LastIndexBeyondInput_NoPanic(t *testing.T) {
	re := ecma262.MustCompile(``, flags.Sticky)
	re.SetLastIndex(10)
	if got := re.FindStringSubmatch("ab"); got != nil {
		t.Fatalf("expected nil for out-of-range sticky lastIndex, got %q", got)
	}

	re2 := ecma262.MustCompile(`a`, flags.Global)
	re2.SetLastIndex(999)
	if re2.MatchString("aaa") {
		t.Fatal("expected no match when lastIndex is past the end")
	}
	// lastIndex exactly at len must be valid (empty match at end).
	re3 := ecma262.MustCompile(`a?`, flags.Sticky)
	re3.SetLastIndex(3)
	if re3.FindStringSubmatch("aaa") == nil {
		t.Fatal("expected empty match at end of input")
	}
}

// C11: Compile must reject u+v together, matching CompileFlags/flags.Parse.
func TestAudit_CompileRejectsUV(t *testing.T) {
	if _, err := ecma262.Compile(`a`, flags.Unicode|flags.UnicodeSets); err == nil {
		t.Fatal("expected error compiling with both u and v flags")
	}
}
