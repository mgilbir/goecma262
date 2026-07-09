// Package vm implements the regex virtual machine
package vm

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// splitKey encodes a full backtracking state (pos, pc, groups) into a map key
// for the OpSplit failure memo. Groups are included so that states differing
// only in captured groups are treated as distinct.
func splitKey(pos, pc int, groups []int) string {
	buf := make([]byte, 0, 8+len(groups)*3)
	buf = strconv.AppendInt(buf, int64(pos), 36)
	buf = append(buf, ':')
	buf = strconv.AppendInt(buf, int64(pc), 36)
	for _, g := range groups {
		buf = append(buf, ':')
		buf = strconv.AppendInt(buf, int64(g), 36)
	}
	return string(buf)
}

// ErrStepLimit is returned when the VM exceeds its step limit (ReDoS protection)
var ErrStepLimit = errors.New("regexp execution step limit exceeded")

// DefaultMaxSteps is the default execution step limit
const DefaultMaxSteps = 1_000_000

// Opcode represents VM instructions
type Opcode byte

const (
	OpMatch Opcode = iota // Success - match found

	// Character matching
	OpChar     // Match specific character
	OpAny      // Match any character (.)
	OpDigit    // Match \d
	OpNonDigit // Match \D
	OpWord     // Match \w
	OpNonWord  // Match \W
	OpSpace    // Match \s
	OpNonSpace // Match \S

	// Character classes
	OpClass // Match character class with escapes

	// Anchors
	OpStartLine    // Match ^
	OpEndLine      // Match $
	OpWordBound    // Match \b
	OpNonWordBound // Match \B

	// Groups
	OpSaveStart // Start of capture group
	OpSaveEnd   // End of capture group

	// Control flow
	OpJmp   // Unconditional jump
	OpSplit // Split execution (for alternation and quantifiers)

	// Backreferences
	OpBackref // Match previously captured group

	// Lookarounds
	OpLookahead     // Positive lookahead
	OpNegLookahead  // Negative lookahead
	OpLookbehind    // Positive lookbehind
	OpNegLookbehind // Negative lookbehind

	// Unicode properties
	OpUnicodeProp    // Match unicode property \p{...}
	OpNotUnicodeProp // Match not unicode property \P{...}

	// Group reset for quantifier body repeats (ES2022 group-reset semantics)
	OpResetGroups // Reset groups[A..B] (inclusive, 1-indexed) to -1

	// Position anchor used in lookbehind sub-VMs: succeeds only if current pos == A.
	OpRequirePos
)

// Instruction represents a single VM instruction
type Instruction struct {
	Op     Opcode
	A      int         // First operand
	B      int         // Second operand
	Char   rune        // For character matching
	Prop   string      // For unicode properties
	Class  []ClassAtom // For OpClass
	Negate bool        // For OpClass
	AltA   []int       // Alternative group indices (for duplicate named groups in \k<name>)
}

// RuneRange represents a range of runes
type RuneRange struct {
	Start rune
	End   rune
}

// ClassAtomKind represents a class atom type
type ClassAtomKind int

const (
	ClassAtomRange ClassAtomKind = iota
	ClassAtomDigit
	ClassAtomWord
	ClassAtomSpace
	ClassAtomUnicodeProp
)

// ClassAtom represents a character class atom
type ClassAtom struct {
	Kind    ClassAtomKind
	Range   RuneRange
	Prop    string
	Negated bool
}

// String returns a human-readable representation of the instruction
func (i Instruction) String() string {
	switch i.Op {
	case OpMatch:
		return "match"
	case OpChar:
		return fmt.Sprintf("char %q", i.Char)
	case OpAny:
		return "any"
	case OpDigit:
		return "digit"
	case OpNonDigit:
		return "non-digit"
	case OpWord:
		return "word"
	case OpNonWord:
		return "non-word"
	case OpSpace:
		return "space"
	case OpNonSpace:
		return "non-space"
	case OpClass:
		return "class"
	case OpStartLine:
		return "start-line"
	case OpEndLine:
		return "end-line"
	case OpWordBound:
		return "word-boundary"
	case OpNonWordBound:
		return "non-word-boundary"
	case OpSaveStart:
		return fmt.Sprintf("save-start %d", i.A)
	case OpSaveEnd:
		return fmt.Sprintf("save-end %d", i.A)
	case OpJmp:
		return fmt.Sprintf("jmp %d", i.A)
	case OpSplit:
		return fmt.Sprintf("split %d %d", i.A, i.B)
	case OpBackref:
		return fmt.Sprintf("backref %d", i.A)
	case OpLookahead:
		return fmt.Sprintf("lookahead %d", i.A)
	case OpNegLookahead:
		return fmt.Sprintf("neg-lookahead %d", i.A)
	case OpLookbehind:
		return fmt.Sprintf("lookbehind %d %d", i.A, i.B)
	case OpNegLookbehind:
		return fmt.Sprintf("neg-lookbehind %d %d", i.A, i.B)
	case OpUnicodeProp:
		return fmt.Sprintf("unicode-prop %s", i.Prop)
	case OpNotUnicodeProp:
		return fmt.Sprintf("not-unicode-prop %s", i.Prop)
	case OpResetGroups:
		return fmt.Sprintf("reset-groups %d..%d", i.A, i.B)
	case OpRequirePos:
		return fmt.Sprintf("require-pos %d", i.A)
	default:
		return fmt.Sprintf("unknown(%d)", i.Op)
	}
}

// VM represents the regex virtual machine
type VM struct {
	Code       []Instruction
	Input      string
	NumGroups  int
	IgnoreCase bool
	Multiline  bool
	DotAll     bool
	Unicode    bool
	MaxSteps   int // 0 means use DefaultMaxSteps
	Err        error

	steps         int             // current step count
	visitedSplits map[string]bool // memoized failed split states (pos, pc, groups)
}

// matchResult holds the result of a recursive match attempt
type matchResult struct {
	matched bool
	pos     int
	groups  []int
}

// Match executes the VM against the input string starting at pos.
// Returns (matched, endPos, groups).
// Uses recursive backtracking so each branch gets its own copy of state.
func (vm *VM) Match(input string, pos int) (bool, int, []int) {
	vm.steps = 0
	vm.Err = nil
	return vm.matchWithInitialGroups(input, pos, nil)
}

// MatchAt is like Match but does NOT reset the step counter or Err, so a caller
// scanning successive start positions shares a single step budget. This keeps
// the ReDoS bound at O(MaxSteps) for a whole search instead of O(len·MaxSteps).
// Callers must inspect Err after the scan to distinguish "no match" from
// "step limit exceeded".
func (vm *VM) MatchAt(input string, pos int) (bool, int, []int) {
	return vm.matchWithInitialGroups(input, pos, nil)
}

// matchWithInitialGroups is like Match but pre-initializes the groups slice
// with values from outerGroups (used to pass captures from outer match into
// lookahead/lookbehind sub-VMs for backreference resolution).
func (vm *VM) matchWithInitialGroups(input string, pos int, outerGroups []int) (bool, int, []int) {
	vm.Input = input
	// Note: steps and Err are intentionally not reset here so that a scan over
	// successive start positions (via MatchAt) shares one step budget. Match
	// resets them for a single standalone attempt.
	vm.visitedSplits = nil // memo is per attempt (keys embed the absolute pos)

	totalGroups := vm.NumGroups + 1
	groups := make([]int, totalGroups*2)
	for i := range groups {
		groups[i] = -1
	}
	// Pre-seed with outer groups if provided (for backreference resolution in lookarounds)
	if outerGroups != nil {
		for i := 0; i < len(outerGroups) && i < len(groups); i++ {
			groups[i] = outerGroups[i]
		}
	}

	res := vm.exec(pos, 0, groups)
	if res.matched {
		return true, res.pos, res.groups
	}
	return false, 0, nil
}

// exec is the recursive backtracking engine.
// It executes from the given (pos, pc) with the given groups snapshot.
// Returns a matchResult indicating success/failure.
func (vm *VM) exec(pos, pc int, groups []int) matchResult {
	maxSteps := vm.MaxSteps
	if maxSteps == 0 {
		maxSteps = DefaultMaxSteps
	}

	for {
		if vm.Err != nil {
			return matchResult{matched: false}
		}
		vm.steps++
		if vm.steps > maxSteps {
			// Step limit exceeded — treat as no match (ReDoS protection)
			vm.Err = ErrStepLimit
			return matchResult{matched: false}
		}

		if pc >= len(vm.Code) {
			return matchResult{matched: false}
		}

		inst := vm.Code[pc]

		switch inst.Op {
		case OpMatch:
			return matchResult{matched: true, pos: pos, groups: copyGroups(groups)}

		case OpChar:
			if pos >= len(vm.Input) {
				return matchResult{matched: false}
			}
			r, size := utf8.DecodeRuneInString(vm.Input[pos:])
			if !vm.matchChar(r, inst.Char) {
				return matchResult{matched: false}
			}
			pos += size
			pc++

		case OpAny:
			if pos >= len(vm.Input) {
				return matchResult{matched: false}
			}
			r, size := utf8.DecodeRuneInString(vm.Input[pos:])
			if !vm.DotAll && isLineTerminator(r) {
				return matchResult{matched: false}
			}
			pos += size
			pc++

		case OpDigit:
			if pos >= len(vm.Input) {
				return matchResult{matched: false}
			}
			r, size := utf8.DecodeRuneInString(vm.Input[pos:])
			// ECMA-262: \d matches only [0-9], not full Unicode digits
			if !isECMADigit(r) {
				return matchResult{matched: false}
			}
			pos += size
			pc++

		case OpNonDigit:
			if pos >= len(vm.Input) {
				return matchResult{matched: false}
			}
			r, size := utf8.DecodeRuneInString(vm.Input[pos:])
			if isECMADigit(r) {
				return matchResult{matched: false}
			}
			pos += size
			pc++

		case OpWord:
			if pos >= len(vm.Input) {
				return matchResult{matched: false}
			}
			r, size := utf8.DecodeRuneInString(vm.Input[pos:])
			if !isWordChar(r) {
				return matchResult{matched: false}
			}
			pos += size
			pc++

		case OpNonWord:
			if pos >= len(vm.Input) {
				return matchResult{matched: false}
			}
			r, size := utf8.DecodeRuneInString(vm.Input[pos:])
			if isWordChar(r) {
				return matchResult{matched: false}
			}
			pos += size
			pc++

		case OpSpace:
			if pos >= len(vm.Input) {
				return matchResult{matched: false}
			}
			r, size := utf8.DecodeRuneInString(vm.Input[pos:])
			if !isSpace(r) {
				return matchResult{matched: false}
			}
			pos += size
			pc++

		case OpNonSpace:
			if pos >= len(vm.Input) {
				return matchResult{matched: false}
			}
			r, size := utf8.DecodeRuneInString(vm.Input[pos:])
			if isSpace(r) {
				return matchResult{matched: false}
			}
			pos += size
			pc++

		case OpClass:
			if pos >= len(vm.Input) {
				return matchResult{matched: false}
			}
			r, size := utf8.DecodeRuneInString(vm.Input[pos:])
			matched := false
			for _, atom := range inst.Class {
				atomMatch := false
				switch atom.Kind {
				case ClassAtomRange:
					atomMatch = vm.matchInRange(r, atom.Range.Start, atom.Range.End)
				case ClassAtomDigit:
					atomMatch = isECMADigit(r)
				case ClassAtomWord:
					atomMatch = isWordChar(r)
				case ClassAtomSpace:
					atomMatch = isSpace(r)
				case ClassAtomUnicodeProp:
					atomMatch = matchUnicodeProperty(r, atom.Prop)
				}
				if atom.Negated {
					atomMatch = !atomMatch
				}
				if atomMatch {
					matched = true
					break
				}
			}
			if inst.Negate {
				matched = !matched
			}
			if !matched {
				return matchResult{matched: false}
			}
			pos += size
			pc++

		case OpStartLine:
			if pos == 0 {
				pc++
				continue
			}
			if vm.Multiline {
				prevR, _ := utf8.DecodeLastRuneInString(vm.Input[:pos])
				if isLineTerminator(prevR) {
					pc++
					continue
				}
			}
			return matchResult{matched: false}

		case OpEndLine:
			if pos >= len(vm.Input) {
				pc++
				continue
			}
			if vm.Multiline {
				r, _ := utf8.DecodeRuneInString(vm.Input[pos:])
				if isLineTerminator(r) {
					pc++
					continue
				}
			}
			return matchResult{matched: false}

		case OpWordBound:
			leftWord := wordCharBeforePos(vm.Input, pos)
			rightWord := wordCharAtPos(vm.Input, pos)
			if leftWord == rightWord {
				return matchResult{matched: false} // Not at word boundary
			}
			pc++

		case OpNonWordBound:
			leftWord := wordCharBeforePos(vm.Input, pos)
			rightWord := wordCharAtPos(vm.Input, pos)
			if leftWord != rightWord {
				return matchResult{matched: false} // At word boundary
			}
			pc++

		case OpSaveStart:
			// Copy groups, update, then continue
			newGroups := copyGroups(groups)
			newGroups[inst.A*2] = pos
			groups = newGroups
			pc++

		case OpSaveEnd:
			newGroups := copyGroups(groups)
			newGroups[inst.A*2+1] = pos
			groups = newGroups
			pc++

		case OpJmp:
			pc = inst.A

		case OpSplit:
			// The outcome of matching from a given (pos, pc, groups) is a pure
			// function of that state, so once branch A has failed for a state we
			// can memoize it: revisiting the same state (an empty-match cycle, or
			// a different backtracking path that reconverges) skips A and takes
			// the exit B. The key MUST include groups — keying on (pos, pc) alone
			// wrongly pruned branches reachable with different captures, e.g.
			// (?:(a)|a)(?:b|c)\1 on "ab".
			key := splitKey(pos, pc, groups)
			if vm.visitedSplits == nil {
				vm.visitedSplits = make(map[string]bool)
			}
			if vm.visitedSplits[key] {
				pc = inst.B
				continue
			}
			vm.visitedSplits[key] = true

			// Try branch A first; if it fails, try branch B.
			// Each branch gets its own copy of groups.
			res := vm.exec(pos, inst.A, copyGroups(groups))
			if res.matched {
				return res
			}
			// Fall through to branch B
			pc = inst.B

		case OpBackref:
			// For duplicate named groups (ES2022), try all alternative group indices
			// to find one that participated in the current match.
			groupIndices := []int{inst.A}
			for _, altIdx := range inst.AltA {
				groupIndices = append(groupIndices, altIdx)
			}

			start, end := -1, -1
			for _, gidx := range groupIndices {
				groupIdx := gidx - 1 // 1-indexed to 0-indexed
				if groupIdx < 0 || groupIdx >= vm.NumGroups {
					continue
				}
				s := groups[groupIdx*2+2] // +2 because group 0 is full match
				e := groups[groupIdx*2+2+1]
				if s >= 0 && e >= 0 {
					start, end = s, e
					break
				}
			}
			if start < 0 || end < 0 {
				// No group with this name was captured — ECMA-262: match empty string
				pc++
				continue
			}

			refText := vm.Input[start:end]
			if vm.IgnoreCase {
				// Case-insensitive backreference matching
				ok, consumed := vm.matchStringIgnoreCaseAt(vm.Input[pos:], refText)
				if !ok {
					return matchResult{matched: false}
				}
				pos += consumed
			} else {
				if pos+len(refText) > len(vm.Input) {
					return matchResult{matched: false}
				}
				if vm.Input[pos:pos+len(refText)] != refText {
					return matchResult{matched: false}
				}
				pos += len(refText)
			}
			pc++

		case OpLookahead:
			// inst.A = start PC of lookahead body
			// inst.B = end PC of lookahead body (one past last body instruction)
			if vm.executeLookahead(inst.A, inst.B, pos, groups) {
				pc++
			} else {
				return matchResult{matched: false}
			}

		case OpNegLookahead:
			if !vm.executeLookahead(inst.A, inst.B, pos, groups) {
				pc++
			} else {
				return matchResult{matched: false}
			}

		case OpLookbehind:
			if vm.executeLookbehind(inst.A, inst.B, pos, groups) {
				pc++
			} else {
				return matchResult{matched: false}
			}

		case OpNegLookbehind:
			if !vm.executeLookbehind(inst.A, inst.B, pos, groups) {
				pc++
			} else {
				return matchResult{matched: false}
			}

		case OpUnicodeProp:
			if pos >= len(vm.Input) {
				return matchResult{matched: false}
			}
			r, size := utf8.DecodeRuneInString(vm.Input[pos:])
			if !matchUnicodeProperty(r, inst.Prop) {
				return matchResult{matched: false}
			}
			pos += size
			pc++

		case OpNotUnicodeProp:
			if pos >= len(vm.Input) {
				return matchResult{matched: false}
			}
			r, size := utf8.DecodeRuneInString(vm.Input[pos:])
			if matchUnicodeProperty(r, inst.Prop) {
				return matchResult{matched: false}
			}
			pos += size
			pc++

		case OpResetGroups:
			// Reset groups[A..B] (inclusive, 1-indexed) to -1
			// Used to implement ECMA-262 group-reset semantics for quantifier body repeats.
			newGroups := copyGroups(groups)
			for g := inst.A; g <= inst.B; g++ {
				if g*2+1 < len(newGroups) {
					newGroups[g*2] = -1
					newGroups[g*2+1] = -1
				}
			}
			groups = newGroups
			pc++

		case OpRequirePos:
			// Succeed only if the current position equals inst.A.
			// Used in lookbehind sub-VMs to anchor the match end.
			if pos != inst.A {
				return matchResult{matched: false}
			}
			pc++

		default:
			return matchResult{matched: false}
		}
	}
}

// executeLookahead executes a lookahead sub-pattern using only the body code [startPC, endPC).
// The lookahead does not consume input.
func (vm *VM) executeLookahead(startPC, endPC, pos int, groups []int) bool {
	// Build a sub-VM with only the lookahead body + OpMatch
	bodyLen := endPC - startPC
	subCode := make([]Instruction, bodyLen+1)
	copy(subCode, vm.Code[startPC:endPC])
	subCode[bodyLen] = Instruction{Op: OpMatch}

	// Fix jump targets: adjust all PCs relative to startPC
	for i := range subCode[:bodyLen] {
		switch subCode[i].Op {
		case OpJmp:
			subCode[i].A -= startPC
		case OpSplit:
			subCode[i].A -= startPC
			subCode[i].B -= startPC
		case OpLookahead, OpNegLookahead, OpLookbehind, OpNegLookbehind:
			subCode[i].A -= startPC
			subCode[i].B -= startPC
		}
	}

	subVM := &VM{
		Code:       subCode,
		Input:      vm.Input,
		NumGroups:  vm.NumGroups,
		IgnoreCase: vm.IgnoreCase,
		Multiline:  vm.Multiline,
		DotAll:     vm.DotAll,
		Unicode:    vm.Unicode,
		MaxSteps:   vm.MaxSteps,
	}

	// Pass outer groups so backreferences inside lookahead can see prior captures
	matched, _, subGroups := subVM.matchWithInitialGroups(vm.Input, pos, groups)
	if subVM.Err != nil {
		vm.Err = subVM.Err
		return false
	}
	if matched && subGroups != nil {
		// Propagate captures from lookahead body back into outer groups
		for i := 0; i < len(subGroups) && i < len(groups); i++ {
			if subGroups[i] >= 0 {
				groups[i] = subGroups[i]
			}
		}
	}
	return matched
}

// executeLookbehind tries matching the lookbehind body ending at the current position.
// It tries progressively longer prefixes ending at pos using a sub-VM anchored at pos.
func (vm *VM) executeLookbehind(startPC, endPC, pos int, groups []int) bool {
	// Build a sub-VM with only the lookbehind body.
	// We append OpRequirePos (checks pos==target) then OpMatch at the end.
	// This allows the VM backtracker to naturally find a match ending at pos.
	bodyLen := endPC - startPC
	subCode := make([]Instruction, bodyLen+2)
	copy(subCode, vm.Code[startPC:endPC])
	subCode[bodyLen] = Instruction{Op: OpRequirePos, A: pos}
	subCode[bodyLen+1] = Instruction{Op: OpMatch}

	for i := range subCode[:bodyLen] {
		switch subCode[i].Op {
		case OpJmp:
			subCode[i].A -= startPC
		case OpSplit:
			subCode[i].A -= startPC
			subCode[i].B -= startPC
		case OpLookahead, OpNegLookahead, OpLookbehind, OpNegLookbehind:
			subCode[i].A -= startPC
			subCode[i].B -= startPC
		}
	}

	subVM := &VM{
		Code:       subCode,
		Input:      vm.Input,
		NumGroups:  vm.NumGroups,
		IgnoreCase: vm.IgnoreCase,
		Multiline:  vm.Multiline,
		DotAll:     vm.DotAll,
		Unicode:    vm.Unicode,
		MaxSteps:   vm.MaxSteps,
	}

	// Try every possible start position before pos.
	// The sub-VM will backtrack to find an execution path that ends exactly at pos.
	for tryPos := 0; tryPos <= pos; tryPos++ {
		// Pass outer groups so backreferences inside lookbehind can see prior captures
		matched, _, subGroups := subVM.matchWithInitialGroups(vm.Input, tryPos, groups)
		if subVM.Err != nil {
			vm.Err = subVM.Err
			return false
		}
		if matched {
			// Propagate captures from lookbehind body back into outer groups
			if subGroups != nil {
				for i := 0; i < len(subGroups) && i < len(groups); i++ {
					if subGroups[i] >= 0 {
						groups[i] = subGroups[i]
					}
				}
			}
			return true
		}
	}
	return false
}

// matchStringIgnoreCaseAt compares a prefix of s against ref and returns
// whether it matches and how many bytes were consumed in s.
func (vm *VM) matchStringIgnoreCaseAt(s, ref string) (bool, int) {
	si, ri := 0, 0
	consumed := 0
	for ri < len(ref) {
		if si >= len(s) {
			return false, 0
		}
		sr, sSize := utf8.DecodeRuneInString(s[si:])
		rr, rSize := utf8.DecodeRuneInString(ref[ri:])
		if !vm.matchChar(sr, rr) {
			return false, 0
		}
		si += sSize
		consumed += sSize
		ri += rSize
	}
	return true, consumed
}

func (vm *VM) matchChar(input, pattern rune) bool {
	if input == pattern {
		return true
	}
	if !vm.IgnoreCase {
		return false
	}
	if vm.Unicode {
		// Unicode simple case folding: input matches pattern if they share a
		// fold orbit (so ſ folds to s, Kelvin sign K to k, etc.).
		return foldEqual(input, pattern)
	}
	// Non-unicode mode: legacy Canonicalize (uppercase, with the ASCII guard).
	return canonicalizeLegacy(input) == canonicalizeLegacy(pattern)
}

func (vm *VM) matchInRange(ch, start, end rune) bool {
	if ch >= start && ch <= end {
		return true
	}
	if !vm.IgnoreCase {
		return false
	}
	// Case-insensitive membership: a character matches the range if any of its
	// case-fold variants falls within the raw [start, end] range. Folding the
	// range endpoints instead (the old approach) breaks ranges that span the
	// case boundary, e.g. /[Y-b]/i failing to match "y".
	if vm.Unicode {
		for f := unicode.SimpleFold(ch); f != ch; f = unicode.SimpleFold(f) {
			if f >= start && f <= end {
				return true
			}
		}
		return false
	}
	// Non-unicode: a character matches the range if one of its case variants
	// falls within the raw range AND canonicalizes to the same character (the
	// canonicalization guard keeps e.g. ſ from matching an [R-T] range).
	ci := canonicalizeLegacy(ch)
	for _, v := range [2]rune{unicode.ToLower(ch), unicode.ToUpper(ch)} {
		if v >= start && v <= end && canonicalizeLegacy(v) == ci {
			return true
		}
	}
	return false
}

// foldEqual reports whether a and b belong to the same Unicode simple-fold orbit.
func foldEqual(a, b rune) bool {
	if a == b {
		return true
	}
	for f := unicode.SimpleFold(a); f != a; f = unicode.SimpleFold(f) {
		if f == b {
			return true
		}
	}
	return false
}

// canonicalizeLegacy implements the ECMA-262 non-Unicode Canonicalize used for
// case-insensitive matching: map ch to its simple uppercase, except keep the
// original when uppercasing a non-ASCII character would produce an ASCII one
// (so ſ (U+017F) does not fold to "s", matching JavaScript's legacy behavior).
func canonicalizeLegacy(ch rune) rune {
	up := unicode.ToUpper(ch)
	if ch >= 128 && up < 128 {
		return ch
	}
	return up
}

// copyGroups returns a copy of the groups slice
func copyGroups(g []int) []int {
	cp := make([]int, len(g))
	copy(cp, g)
	return cp
}

// isECMADigit matches ECMA-262 \d: only [0-9]
func isECMADigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_'
}

// wordCharBeforePos returns true if the character before pos is a word char.
// Handles multi-byte UTF-8 correctly using DecodeLastRuneInString.
func wordCharBeforePos(s string, pos int) bool {
	if pos <= 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:pos])
	if r == utf8.RuneError {
		return false
	}
	return isWordChar(r)
}

// wordCharAtPos returns true if the character at pos is a word char.
func wordCharAtPos(s string, pos int) bool {
	if pos >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[pos:])
	if r == utf8.RuneError {
		return false
	}
	return isWordChar(r)
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' ||
		r == '\f' || r == '\v' || r == '\u00a0' ||
		r == '\u1680' || r == '\u2000' || r == '\u2001' ||
		r == '\u2002' || r == '\u2003' || r == '\u2004' ||
		r == '\u2005' || r == '\u2006' || r == '\u2007' ||
		r == '\u2008' || r == '\u2009' || r == '\u200a' ||
		r == '\u2028' || r == '\u2029' || r == '\u202f' ||
		r == '\u205f' || r == '\u3000' || r == '\ufeff'
}

func isLineTerminator(r rune) bool {
	return r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029'
}

// matchUnicodeProperty reports whether r has the given Unicode property. An
// unrecognized property matches nothing, but callers should reject unknown
// property names at compile time via ValidUnicodeProperty.
func matchUnicodeProperty(r rune, prop string) bool {
	m, ok := resolveUnicodePropertyCached(prop)
	if !ok {
		return false
	}
	return m(r)
}

// ValidUnicodeProperty reports whether prop names a Unicode property/value
// expression this engine understands. The compiler uses it to make \p{Unknown}
// (and \p{}) a SyntaxError instead of a construct that silently matches nothing.
func ValidUnicodeProperty(prop string) bool {
	_, ok := resolveUnicodePropertyCached(prop)
	return ok
}

// propCache memoizes property resolution so a \p{...} instruction resolves its
// predicate once (not per matched character). A nil entry records an unknown
// property. It is safe for concurrent use.
var propCache sync.Map // string -> propEntry

type propEntry struct {
	fn func(rune) bool
	ok bool
}

func resolveUnicodePropertyCached(prop string) (func(rune) bool, bool) {
	if v, ok := propCache.Load(prop); ok {
		e := v.(propEntry)
		return e.fn, e.ok
	}
	fn, ok := resolveUnicodeProperty(prop)
	propCache.Store(prop, propEntry{fn: fn, ok: ok})
	return fn, ok
}

// resolveUnicodeProperty maps a property expression to a rune predicate. It
// accepts "Name=Value" (general category or script) and lone names (a general
// category value or a binary property). It returns ok=false for anything it
// does not recognize.
func resolveUnicodeProperty(prop string) (func(rune) bool, bool) {
	if eq := strings.IndexByte(prop, '='); eq >= 0 {
		name := normalizeUnicodeProperty(prop[:eq])
		value := prop[eq+1:]
		switch name {
		case "gc", "generalcategory":
			if t := categoryTable(value); t != nil {
				return func(r rune) bool { return unicode.Is(t, r) }, true
			}
		case "sc", "script", "scx", "scriptextensions":
			if t := scriptTable(value); t != nil {
				return func(r rune) bool { return unicode.Is(t, r) }, true
			}
		}
		return nil, false
	}
	if t := categoryTable(prop); t != nil {
		return func(r rune) bool { return unicode.Is(t, r) }, true
	}
	return binaryProperty(prop)
}

// categoryTable resolves a general-category name or alias (short or long, in any
// case, with underscores/hyphens/spaces ignored) to its unicode.RangeTable.
func categoryTable(name string) *unicode.RangeTable {
	short, ok := categoryAliases[normalizeUnicodeProperty(name)]
	if !ok {
		return nil
	}
	return unicode.Categories[short]
}

// scriptTable resolves a script name (ignoring case and separators) to its table.
func scriptTable(name string) *unicode.RangeTable {
	norm := normalizeUnicodeProperty(name)
	for scriptName, table := range unicode.Scripts {
		if normalizeUnicodeProperty(scriptName) == norm {
			return table
		}
	}
	return nil
}

// binaryProperty resolves a lone \p{Name} to a predicate, covering the ECMA-262
// binary properties: computed derivations, aliases onto Go's unicode.Properties
// tables, and the general/canonical names directly.
func binaryProperty(prop string) (func(rune) bool, bool) {
	norm := normalizeUnicodeProperty(prop)
	if fn, ok := binaryPropertyPredicates[norm]; ok {
		return fn, true
	}
	if canonical, ok := binaryPropertyAliases[norm]; ok {
		if t := unicode.Properties[canonical]; t != nil {
			return func(r rune) bool { return unicode.Is(t, r) }, true
		}
	}
	// Canonical long names directly (e.g. \p{White_Space}, \p{Dash}).
	for name, table := range unicode.Properties {
		if normalizeUnicodeProperty(name) == norm {
			t := table
			return func(r rune) bool { return unicode.Is(t, r) }, true
		}
	}
	return nil, false
}

// binaryPropertyPredicates holds the ECMA-262 binary properties that are
// computed from Go's categories/case mappings rather than a single table.
// Some (Case_Ignorable, Default_Ignorable_Code_Point, XID_*) are close
// approximations of the full Unicode definitions.
var binaryPropertyPredicates = map[string]func(rune) bool{
	"ascii": func(r rune) bool { return r <= 0x7F },
	"any":   func(r rune) bool { return true },
	"assigned": func(r rune) bool {
		return r != unicode.ReplacementChar && (unicode.IsGraphic(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r))
	},
	"alphabetic":                isAlphabetic,
	"alpha":                     isAlphabetic,
	"lowercase":                 func(r rune) bool { return unicode.IsLower(r) || unicode.Is(unicode.Other_Lowercase, r) },
	"lower":                     func(r rune) bool { return unicode.IsLower(r) || unicode.Is(unicode.Other_Lowercase, r) },
	"uppercase":                 func(r rune) bool { return unicode.IsUpper(r) || unicode.Is(unicode.Other_Uppercase, r) },
	"upper":                     func(r rune) bool { return unicode.IsUpper(r) || unicode.Is(unicode.Other_Uppercase, r) },
	"cased":                     isCased,
	"caseignorable":             isCaseIgnorable,
	"ci":                        isCaseIgnorable,
	"whitespace":                unicode.IsSpace,
	"space":                     unicode.IsSpace,
	"wspace":                    unicode.IsSpace,
	"math":                      func(r rune) bool { return unicode.Is(unicode.Sm, r) || unicode.Is(unicode.Other_Math, r) },
	"idstart":                   isIDStart,
	"ids":                       isIDStart,
	"idcontinue":                isIDContinue,
	"idc":                       isIDContinue,
	"xidstart":                  isIDStart,    // NFKC-closure approximation
	"xids":                      isIDStart,    // NFKC-closure approximation
	"xidcontinue":               isIDContinue, // NFKC-closure approximation
	"xidc":                      isIDContinue, // NFKC-closure approximation
	"graphemeextend":            isGraphemeExtend,
	"grext":                     isGraphemeExtend,
	"changeswhenuppercased":     func(r rune) bool { return unicode.ToUpper(r) != r },
	"cwu":                       func(r rune) bool { return unicode.ToUpper(r) != r },
	"changeswhenlowercased":     func(r rune) bool { return unicode.ToLower(r) != r },
	"cwl":                       func(r rune) bool { return unicode.ToLower(r) != r },
	"changeswhentitlecased":     func(r rune) bool { return unicode.ToTitle(r) != r },
	"cwt":                       func(r rune) bool { return unicode.ToTitle(r) != r },
	"defaultignorablecodepoint": isDefaultIgnorable,
	"di":                        isDefaultIgnorable,
	"digit":                     func(r rune) bool { return unicode.Is(unicode.Nd, r) }, // non-standard alias for Nd
}

// binaryPropertyAliases maps ECMA-262 binary-property aliases to the canonical
// names keying Go's unicode.Properties table.
var binaryPropertyAliases = map[string]string{
	"asciihexdigit":         "ASCII_Hex_Digit",
	"ahex":                  "ASCII_Hex_Digit",
	"bidicontrol":           "Bidi_Control",
	"bidic":                 "Bidi_Control",
	"dash":                  "Dash",
	"deprecated":            "Deprecated",
	"dep":                   "Deprecated",
	"diacritic":             "Diacritic",
	"dia":                   "Diacritic",
	"extender":              "Extender",
	"ext":                   "Extender",
	"hexdigit":              "Hex_Digit",
	"hex":                   "Hex_Digit",
	"hyphen":                "Hyphen",
	"idsbinaryoperator":     "IDS_Binary_Operator",
	"idsb":                  "IDS_Binary_Operator",
	"idstrinaryoperator":    "IDS_Trinary_Operator",
	"idst":                  "IDS_Trinary_Operator",
	"ideographic":           "Ideographic",
	"ideo":                  "Ideographic",
	"joincontrol":           "Join_Control",
	"joinc":                 "Join_Control",
	"logicalorderexception": "Logical_Order_Exception",
	"loe":                   "Logical_Order_Exception",
	"noncharactercodepoint": "Noncharacter_Code_Point",
	"nchar":                 "Noncharacter_Code_Point",
	"patternsyntax":         "Pattern_Syntax",
	"patsyn":                "Pattern_Syntax",
	"patternwhitespace":     "Pattern_White_Space",
	"patws":                 "Pattern_White_Space",
	"quotationmark":         "Quotation_Mark",
	"qmark":                 "Quotation_Mark",
	"radical":               "Radical",
	"regionalindicator":     "Regional_Indicator",
	"ri":                    "Regional_Indicator",
	"sentenceterminal":      "Sentence_Terminal",
	"sterm":                 "Sentence_Terminal",
	"softdotted":            "Soft_Dotted",
	"sd":                    "Soft_Dotted",
	"terminalpunctuation":   "Terminal_Punctuation",
	"term":                  "Terminal_Punctuation",
	"unifiedideograph":      "Unified_Ideograph",
	"uideo":                 "Unified_Ideograph",
	"variationselector":     "Variation_Selector",
	"vs":                    "Variation_Selector",
}

func isAlphabetic(r rune) bool {
	return unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_Alphabetic, r)
}

func isCased(r rune) bool {
	return unicode.IsLower(r) || unicode.IsUpper(r) || unicode.Is(unicode.Lt, r) ||
		unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

func isCaseIgnorable(r rune) bool {
	// Approximation of Case_Ignorable: the combining/format/modifier categories
	// (the Word_Break refinements are omitted).
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) ||
		unicode.Is(unicode.Lm, r) || unicode.Is(unicode.Sk, r)
}

func isIDStart(r rune) bool {
	return unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_ID_Start, r)
}

func isIDContinue(r rune) bool {
	return isIDStart(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) ||
		unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Pc, r) || unicode.Is(unicode.Other_ID_Continue, r)
}

func isGraphemeExtend(r rune) bool {
	return unicode.Is(unicode.Me, r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Other_Grapheme_Extend, r)
}

func isDefaultIgnorable(r rune) bool {
	// Approximation of Default_Ignorable_Code_Point.
	return unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) ||
		unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Variation_Selector, r)
}

func normalizeUnicodeProperty(prop string) string {
	prop = strings.TrimSpace(prop)
	prop = strings.ReplaceAll(prop, "_", "")
	prop = strings.ReplaceAll(prop, "-", "")
	prop = strings.ReplaceAll(prop, " ", "")
	return strings.ToLower(prop)
}

// categoryAliases maps normalized general-category names (short codes and long
// names/aliases) to the short code keying unicode.Categories.
var categoryAliases = map[string]string{
	"l": "L", "letter": "L",
	"lu": "Lu", "uppercaseletter": "Lu",
	"ll": "Ll", "lowercaseletter": "Ll",
	"lt": "Lt", "titlecaseletter": "Lt",
	"lm": "Lm", "modifierletter": "Lm",
	"lo": "Lo", "otherletter": "Lo",
	"lc": "L", "casedletter": "L",
	"m": "M", "mark": "M", "combiningmark": "M",
	"mn": "Mn", "nonspacingmark": "Mn",
	"mc": "Mc", "spacingcombiningmark": "Mc", "spacingmark": "Mc",
	"me": "Me", "enclosingmark": "Me",
	"n": "N", "number": "N",
	"nd": "Nd", "decimalnumber": "Nd",
	"nl": "Nl", "letternumber": "Nl",
	"no": "No", "othernumber": "No",
	"p": "P", "punctuation": "P",
	"pc": "Pc", "connectorpunctuation": "Pc",
	"pd": "Pd", "dashpunctuation": "Pd",
	"ps": "Ps", "openpunctuation": "Ps",
	"pe": "Pe", "closepunctuation": "Pe",
	"pi": "Pi", "initialpunctuation": "Pi",
	"pf": "Pf", "finalpunctuation": "Pf",
	"po": "Po", "otherpunctuation": "Po",
	"s": "S", "symbol": "S",
	"sm": "Sm", "mathsymbol": "Sm",
	"sc": "Sc", "currencysymbol": "Sc",
	"sk": "Sk", "modifiersymbol": "Sk",
	"so": "So", "othersymbol": "So",
	"z": "Z", "separator": "Z",
	"zs": "Zs", "spaceseparator": "Zs",
	"zl": "Zl", "lineseparator": "Zl",
	"zp": "Zp", "paragraphseparator": "Zp",
	"c": "C", "other": "C",
	"cc": "Cc", "control": "Cc", "cntrl": "Cc",
	"cf": "Cf", "format": "Cf",
	"cs": "Cs", "surrogate": "Cs",
	"co": "Co", "privateuse": "Co",
}
