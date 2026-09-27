package vm

import (
	"slices"
	"sort"
	"unicode"
)

// CharSetOp is the kind of a CharSet node.
type CharSetOp uint8

const (
	// CharSetRanges holds the code points in Ranges.
	CharSetRanges CharSetOp = iota
	// CharSetEscape holds the code points Atom matches: a \d, \w, \s or \p{…}
	// escape, complemented when Atom.Negated.
	CharSetEscape
	CharSetUnion
	CharSetIntersection
	// CharSetComplement holds every code point not in Items[0].
	CharSetComplement
)

// A CharSet is the code point part of a v-mode character class
// (ClassSetExpression), kept as an expression over its operands and evaluated
// per rune, so that property escapes need not be materialized.
//
// Under IgnoreCase every node denotes a set closed under simple case folding:
// a leaf matches a rune when any member of the rune's fold orbit is in it, and
// an escape's complement is taken after that closure. Unions, intersections
// and complements of closed sets are closed, so testing membership of the
// rune alone is then exactly ECMA-262's CharacterComplement and
// MaybeSimpleCaseFolding semantics: [^a] under /vi rejects "A", and \W
// rejects "ſ".
type CharSet struct {
	Op     CharSetOp
	Ranges []RuneRange // CharSetRanges: sorted, disjoint and non-adjacent
	Atom   ClassAtom   // CharSetEscape; never ClassAtomRange
	Items  []*CharSet  // CharSetUnion, CharSetIntersection, CharSetComplement
}

// NewCharSetRanges returns the CharSetRanges node holding the union of rs.
func NewCharSetRanges(rs []RuneRange) *CharSet {
	sorted := append([]RuneRange(nil), rs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	var merged []RuneRange
	for _, r := range sorted {
		if n := len(merged); n > 0 && r.Start <= merged[n-1].End+1 {
			merged[n-1].End = max(merged[n-1].End, r.End)
			continue
		}
		merged = append(merged, r)
	}
	return &CharSet{Op: CharSetRanges, Ranges: merged}
}

// matchCharSet reports whether r is in s under vm's case sensitivity.
func (vm *VM) matchCharSet(s *CharSet, r rune) bool {
	switch s.Op {
	case CharSetRanges:
		return vm.anyFold(r, func(f rune) bool { return inRanges(s.Ranges, f) })
	case CharSetEscape:
		return vm.anyFold(r, s.Atom.matchPositive) != s.Atom.Negated
	case CharSetUnion:
		for _, it := range s.Items {
			if vm.matchCharSet(it, r) {
				return true
			}
		}
		return false
	case CharSetIntersection:
		for _, it := range s.Items {
			if !vm.matchCharSet(it, r) {
				return false
			}
		}
		return true
	case CharSetComplement:
		return !vm.matchCharSet(s.Items[0], r)
	}
	return false
}

// anyFold reports whether pred holds for r or, under IgnoreCase, for any rune
// in r's simple case folding orbit. A CharSet exists only in v mode, where
// case folding is always Unicode simple case folding.
func (vm *VM) anyFold(r rune, pred func(rune) bool) bool {
	if pred(r) {
		return true
	}
	if !vm.IgnoreCase {
		return false
	}
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if pred(f) {
			return true
		}
	}
	return false
}

func inRanges(rs []RuneRange, r rune) bool {
	i := sort.Search(len(rs), func(i int) bool { return rs[i].End >= r })
	return i < len(rs) && rs[i].Start <= r
}

// A RuneSwitch is the operand of OpRuneSwitch: it consumes one rune and
// continues at the target paired with that rune's key, failing if there is
// none. It lets one instruction choose among a trie node's children, where a
// chain of splits would cost a step per child at every input position: at
// most one child can match a rune, so no choice point is needed.
//
// Under IgnoreCase the keys are CanonicalFold values and the rune is folded
// before lookup. OpRuneSwitch is emitted only in v mode, where case folding is
// always simple case folding.
type RuneSwitch struct {
	Keys    []rune // sorted, distinct
	Targets []int  // Targets[i] is the pc to continue at after Keys[i]
}

func (s *RuneSwitch) target(key rune) (int, bool) {
	i, found := slices.BinarySearch(s.Keys, key)
	if !found {
		return 0, false
	}
	return s.Targets[i], true
}

func (vm *VM) switchKey(r rune) rune {
	if vm.IgnoreCase {
		return CanonicalFold(r)
	}
	return r
}

// CanonicalFold returns the least rune in r's simple case folding orbit, so
// runes that match each other case-insensitively have the same value.
func CanonicalFold(r rune) rune {
	m := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		m = min(m, f)
	}
	return m
}

// matchPositive reports whether r matches the escape a, ignoring a.Negated.
func (a ClassAtom) matchPositive(r rune) bool {
	switch a.Kind {
	case ClassAtomDigit:
		return isECMADigit(r)
	case ClassAtomWord:
		return isWordChar(r)
	case ClassAtomSpace:
		return isSpace(r)
	case ClassAtomUnicodeProp:
		return matchUnicodeProperty(r, a.Prop)
	case ClassAtomRange:
		return r >= a.Range.Start && r <= a.Range.End
	}
	return false
}
