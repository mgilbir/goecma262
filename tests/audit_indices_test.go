package ecma262_test

import (
	"reflect"
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// #2: match indices are exposed through the full set of *Index methods,
// including a byte FindSubmatchIndex and the FindAll* index variants.
func TestAudit_SubmatchIndices(t *testing.T) {
	re := ecma262.MustCompile(`(\d{4})-(\d{2})`, flags.Flags(0))
	idx := re.FindStringSubmatchIndex("x 2024-03 y")
	want := []int{2, 9, 2, 6, 7, 9} // whole, group1, group2 (byte offsets)
	if !reflect.DeepEqual(idx, want) {
		t.Fatalf("FindStringSubmatchIndex = %v, want %v", idx, want)
	}
	// Byte variant agrees.
	if b := re.FindSubmatchIndex([]byte("x 2024-03 y")); !reflect.DeepEqual(b, want) {
		t.Fatalf("FindSubmatchIndex = %v, want %v", b, want)
	}
}

// A non-participating group is reported as -1/-1.
func TestAudit_UnmatchedGroupIndexIsMinusOne(t *testing.T) {
	re := ecma262.MustCompile(`(a)|(b)`, flags.Flags(0))
	idx := re.FindStringSubmatchIndex("b")
	// group 1 did not participate -> -1,-1; group 2 matched "b" at 0..1
	if idx[2] != -1 || idx[3] != -1 {
		t.Errorf("group 1 indices = %d,%d, want -1,-1", idx[2], idx[3])
	}
	if idx[4] != 0 || idx[5] != 1 {
		t.Errorf("group 2 indices = %d,%d, want 0,1", idx[4], idx[5])
	}
}

// FindAll*Index variants return one entry per match, handling zero-width matches.
func TestAudit_FindAllIndex(t *testing.T) {
	re := ecma262.MustCompile(`\w+`, flags.Global)
	got := re.FindAllStringIndex("ab cd", -1)
	want := [][]int{{0, 2}, {3, 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindAllStringIndex = %v, want %v", got, want)
	}

	sub := re.FindAllStringSubmatchIndex("ab cd", -1)
	if len(sub) != 2 || sub[0][0] != 0 || sub[0][1] != 2 || sub[1][0] != 3 || sub[1][1] != 5 {
		t.Fatalf("FindAllStringSubmatchIndex = %v", sub)
	}
}

// Named-group indices are obtained by combining SubexpIndex with the index pairs
// (the equivalent of JavaScript's d-flag indices.groups).
func TestAudit_NamedGroupIndices(t *testing.T) {
	re := ecma262.MustCompile(`(?<y>\d{4})-(?<m>\d{2})`, flags.Flags(0))
	idx := re.FindStringSubmatchIndex("2024-03")
	yi := re.SubexpIndex("y")
	if idx[yi*2] != 0 || idx[yi*2+1] != 4 {
		t.Fatalf("named group y indices = %d,%d, want 0,4", idx[yi*2], idx[yi*2+1])
	}
}

// The d flag is accepted (parsed) and does not affect index availability.
func TestAudit_HasIndicesFlagAccepted(t *testing.T) {
	re, err := ecma262.CompileFlags(`(a)`, "d")
	if err != nil {
		t.Fatalf("compile with d flag: %v", err)
	}
	if idx := re.FindStringSubmatchIndex("a"); idx == nil || idx[0] != 0 {
		t.Fatalf("indices should be available regardless of d flag, got %v", idx)
	}
}
