package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// C14: with the g flag, MatchString advances lastIndex like JS test(), so
// repeated calls iterate and eventually fail (resetting lastIndex to 0).
func TestAudit_GlobalMatchAdvancesLastIndex(t *testing.T) {
	re := ecma262.MustCompile(`a`, flags.Global)
	if !re.MatchString("aa") {
		t.Fatal("first MatchString should match")
	}
	if re.LastIndex() != 1 {
		t.Fatalf("lastIndex after first match = %d, want 1", re.LastIndex())
	}
	if !re.MatchString("aa") {
		t.Fatal("second MatchString should match at index 1")
	}
	if re.LastIndex() != 2 {
		t.Fatalf("lastIndex after second match = %d, want 2", re.LastIndex())
	}
	if re.MatchString("aa") {
		t.Fatal("third MatchString should fail past the end")
	}
	if re.LastIndex() != 0 {
		t.Fatalf("lastIndex should reset to 0 after a miss, got %d", re.LastIndex())
	}
}

// A non-global regexp is stateless: repeated calls behave identically and
// lastIndex is untouched.
func TestAudit_NonGlobalMatchIsStateless(t *testing.T) {
	re := ecma262.MustCompile(`a`, flags.Flags(0))
	for i := 0; i < 3; i++ {
		if !re.MatchString("aa") {
			t.Fatalf("call %d should match", i)
		}
	}
	if re.LastIndex() != 0 {
		t.Fatalf("non-global lastIndex should stay 0, got %d", re.LastIndex())
	}
}
