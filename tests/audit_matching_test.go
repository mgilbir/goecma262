package ecma262_test

import (
	"strings"
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/vm"
)

// C6: a valid backtracking branch reachable only with different captures must
// not be pruned by the empty-loop cycle detector.
func TestAudit_BacktrackingNotOverPruned(t *testing.T) {
	re := ecma262.MustCompile(`(?:(a)|a)(?:b|c)\1`, flags.Flags(0))
	if !re.MatchString("ab") {
		t.Fatal(`(?:(a)|a)(?:b|c)\1 should match "ab" (second alternative, \1 empty)`)
	}
}

// The cycle detector still prevents infinite loops on empty-matching quantifiers.
func TestAudit_EmptyLoopTerminates(t *testing.T) {
	re := ecma262.MustCompile(`(a*)*b`, flags.Flags(0))
	if !re.MatchString("aaab") {
		t.Fatal(`(a*)*b should match "aaab"`)
	}
	if re.MatchString("aaac") {
		t.Fatal(`(a*)*b should not match "aaac"`)
	}
}

// C18: case-insensitive ranges that span the case boundary must work.
func TestAudit_CaseInsensitiveRange(t *testing.T) {
	re := ecma262.MustCompile(`[Y-b]`, flags.IgnoreCase)
	// Y Z [ \ ] ^ _ ` a b, case-insensitively also y and z.
	for _, s := range []string{"Y", "b", "y", "z", "a"} {
		if !re.MatchString(s) {
			t.Errorf("/[Y-b]/i should match %q", s)
		}
	}
	if re.MatchString("x") {
		t.Error("/[Y-b]/i should not match 'x'")
	}
}

// C21: Unicode simple case folding under the u flag.
func TestAudit_UnicodeCaseFolding(t *testing.T) {
	re := ecma262.MustCompile(`s`, flags.IgnoreCase|flags.Unicode)
	if !re.MatchString("ſ") { // ſ (LATIN SMALL LETTER LONG S) folds to s
		t.Error(`/s/iu should match ſ (U+017F)`)
	}
	re2 := ecma262.MustCompile(`k`, flags.IgnoreCase|flags.Unicode)
	if !re2.MatchString("K") { // KELVIN SIGN folds to k
		t.Error(`/k/iu should match KELVIN SIGN (U+212A)`)
	}
}

// C10: the step budget is shared across the whole scan, so a pathological input
// exhausts it once (surfaced via the *Err API) rather than per start position.
func TestAudit_StepBudgetSharedAcrossScan(t *testing.T) {
	re := ecma262.MustCompile(`(a+)+$`, flags.Flags(0))
	re.SetMaxSteps(5000)
	input := strings.Repeat("a", 40) + "!"
	_, err := re.MatchStringErr(input)
	if err != vm.ErrStepLimit {
		t.Fatalf("expected ErrStepLimit for catastrophic pattern, got %v", err)
	}
}
