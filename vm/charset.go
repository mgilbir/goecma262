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
func (vm *VM) matchCharSet(ic bool, s *CharSet, r rune) bool {
	switch s.Op {
	case CharSetRanges:
		return vm.anyFold(ic, r, func(f rune) bool { return inRanges(s.Ranges, f) })
	case CharSetEscape:
		return vm.anyFold(ic, r, func(f rune) bool { return vm.matchEscape(ic, s.Atom, f) }) != s.Atom.Negated
	case CharSetUnion:
		for _, it := range s.Items {
			if vm.matchCharSet(ic, it, r) {
				return true
			}
		}
		return false
	case CharSetIntersection:
		for _, it := range s.Items {
			if !vm.matchCharSet(ic, it, r) {
				return false
			}
		}
		return true
	case CharSetComplement:
		return !vm.matchCharSet(ic, s.Items[0], r)
	}
	return false
}

// anyFold reports whether pred holds for r or, under IgnoreCase in Unicode
// mode (u or v), for any rune in r's simple case folding orbit. That is how
// ECMA-262 matches a rune against a set there: by comparing Canonicalize of
// both, and Canonicalize is simple case folding. Without u or v it is the
// legacy uppercase mapping, which no escape's set is affected by (it never
// maps a non-ASCII rune to ASCII), so pred alone decides.
func (vm *VM) anyFold(ic bool, r rune, pred func(rune) bool) bool {
	if pred(r) {
		return true
	}
	if !ic || !vm.Unicode {
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

func (vm *VM) switchKey(ic bool, r rune) rune {
	if ic {
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

// wordChar reports whether r is in ECMA-262's WordCharacters: [A-Za-z0-9_],
// plus, under IgnoreCase in Unicode mode, the runes that fold to one of those
// (ſ and the Kelvin sign K). It decides \w, \W, \b and \B.
func (vm *VM) wordChar(ic bool, r rune) bool {
	return isWordChar(r) || vm.anyFold(ic, r, isWordChar)
}

// matchEscape reports whether r is in the set of the escape a, ignoring
// a.Negated.
func (vm *VM) matchEscape(ic bool, a ClassAtom, r rune) bool {
	switch a.Kind {
	case ClassAtomDigit:
		return isECMADigit(r)
	case ClassAtomWord:
		return vm.wordChar(ic, r)
	case ClassAtomSpace:
		return isSpace(r)
	case ClassAtomUnicodeProp:
		return matchUnicodeProperty(r, a.Prop)
	case ClassAtomRange:
		return r >= a.Range.Start && r <= a.Range.End
	}
	return false
}
