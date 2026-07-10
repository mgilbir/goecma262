package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// C3: a capturing group inside a counted quantifier must not create new groups
// or shift the numbering of later groups.
func TestAudit_CountedQuantifierGroupNumbering(t *testing.T) {
	re := ecma262.MustCompile(`(a){2}(b)\2`, flags.Flags(0))
	if re.NumSubexp() != 2 {
		t.Fatalf("NumSubexp = %d, want 2", re.NumSubexp())
	}
	if !re.MatchString("aabb") {
		t.Fatal(`(a){2}(b)\2 should match "aabb"`)
	}
	m := re.FindStringSubmatch("aabb")
	if len(m) != 3 || m[1] != "a" || m[2] != "b" {
		t.Fatalf("submatch = %q, want [aabb a b]", m)
	}
}

// A group captured across iterations keeps its last-iteration value.
func TestAudit_CountedQuantifierLastValue(t *testing.T) {
	re := ecma262.MustCompile(`(a){2}`, flags.Flags(0))
	m := re.FindStringSubmatch("aa")
	if len(m) != 2 || m[0] != "aa" || m[1] != "a" {
		t.Fatalf("submatch = %q, want [aa a]", m)
	}
}

// {0} still defines the group (JS: group exists, value undefined -> "").
func TestAudit_ZeroQuantifierGroupExists(t *testing.T) {
	re := ecma262.MustCompile(`(a){0}`, flags.Flags(0))
	if re.NumSubexp() != 1 {
		t.Fatalf("NumSubexp = %d, want 1", re.NumSubexp())
	}
	if len(re.SubexpNames()) != 2 {
		t.Fatalf("len(SubexpNames) = %d, want 2", len(re.SubexpNames()))
	}
}

// Group-reset across iterations: a non-participating alternative clears the slot.
func TestAudit_CountedQuantifierResetSemantics(t *testing.T) {
	re := ecma262.MustCompile(`(?:(a)|b){2}`, flags.Flags(0))
	m := re.FindStringSubmatch("ab") // last iteration matched 'b' -> group 1 unset
	if len(m) != 2 || m[0] != "ab" || m[1] != "" {
		t.Fatalf("submatch = %q, want [ab \"\"]", m)
	}
}

// C4: SubexpNames must stay aligned with FindStringSubmatch, including for
// captures inside lookarounds.
func TestAudit_NamesAlignedWithSubmatch(t *testing.T) {
	// The lookahead asserts 'a' ahead, which the following literal then consumes.
	re := ecma262.MustCompile(`(?=(?<x>a))a(?<y>b)`, flags.Flags(0))
	names := re.SubexpNames()
	if len(names) != re.NumSubexp()+1 {
		t.Fatalf("len(SubexpNames)=%d, want NumSubexp+1=%d", len(names), re.NumSubexp()+1)
	}
	if re.SubexpIndex("x") != 1 || re.SubexpIndex("y") != 2 {
		t.Fatalf("SubexpIndex x=%d y=%d, want 1 and 2", re.SubexpIndex("x"), re.SubexpIndex("y"))
	}
	// The capture inside the lookahead participates and is counted.
	sub := re.FindStringSubmatch("ab")
	if len(sub) != 3 || sub[1] != "a" || sub[2] != "b" {
		t.Fatalf("submatch = %q, want [ab a b]", sub)
	}
}

// Invariant: for any pattern, len(SubexpNames) == len(FindStringSubmatch) on a match.
func TestAudit_NamesLengthInvariant(t *testing.T) {
	patterns := []string{`(a)(b)`, `(a){3}`, `(?=(a))b`, `((a)(b))+`, `(?<n>x){2}`, `(a){0}(b)`}
	for _, p := range patterns {
		re := ecma262.MustCompile(p, flags.Flags(0))
		if len(re.SubexpNames()) != re.NumSubexp()+1 {
			t.Errorf("/%s/ len(SubexpNames)=%d, want %d", p, len(re.SubexpNames()), re.NumSubexp()+1)
		}
	}
}
