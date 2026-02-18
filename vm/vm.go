// Package vm implements the regex virtual machine
package vm

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

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
	OpInRange    // Match character in range [a-z]
	OpNotInRange // Match character not in range [^a-z]
	OpClass      // Match character class with escapes

	// Anchors
	OpStartLine    // Match ^
	OpEndLine      // Match $
	OpWordBound    // Match \b
	OpNonWordBound // Match \B

	// Groups
	OpSaveStart // Start of capture group
	OpSaveEnd   // End of capture group

	// Control flow
	OpJmp         // Unconditional jump
	OpSplit       // Split execution (for alternation)
	OpGreedyLoop  // Greedy loop, 0+ iterations (matches maximum first)
	OpGreedyLoop1 // Greedy loop, 1+ iterations (matches maximum first)
	OpGreedyLoopN // Greedy loop, 0..N iterations (inst.A=body, inst.B=exit, inst.Extra=max)

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
)

// Instruction represents a single VM instruction
type Instruction struct {
	Op     Opcode
	A      int         // First operand
	B      int         // Second operand
	Extra  int         // Extra operand (e.g., max count for OpGreedyLoopN)
	Char   rune        // For character matching
	Ranges []RuneRange // For character classes
	Prop   string      // For unicode properties
	Class  []ClassAtom // For OpClass
	Negate bool        // For OpClass
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
	case OpInRange:
		return fmt.Sprintf("in-range %v", i.Ranges)
	case OpNotInRange:
		return fmt.Sprintf("not-in-range %v", i.Ranges)
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
	visitedSplits map[[2]int]bool // shared cycle detection for empty-match loops
}

// New creates a new VM with the given code
func New(code []Instruction, numGroups int) *VM {
	return &VM{
		Code:      code,
		NumGroups: numGroups,
	}
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
	return vm.matchWithInitialGroups(input, pos, nil)
}

// matchWithInitialGroups is like Match but pre-initializes the groups slice
// with values from outerGroups (used to pass captures from outer match into
// lookahead/lookbehind sub-VMs for backreference resolution).
func (vm *VM) matchWithInitialGroups(input string, pos int, outerGroups []int) (bool, int, []int) {
	vm.Input = input
	vm.steps = 0
	vm.Err = nil
	vm.visitedSplits = nil // reset per match attempt

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

		case OpInRange:
			if pos >= len(vm.Input) {
				return matchResult{matched: false}
			}
			r, size := utf8.DecodeRuneInString(vm.Input[pos:])
			found := false
			for _, rng := range inst.Ranges {
				if vm.matchInRange(r, rng.Start, rng.End) {
					found = true
					break
				}
			}
			if !found {
				return matchResult{matched: false}
			}
			pos += size
			pc++

		case OpNotInRange:
			if pos >= len(vm.Input) {
				return matchResult{matched: false}
			}
			r, size := utf8.DecodeRuneInString(vm.Input[pos:])
			found := false
			for _, rng := range inst.Ranges {
				if vm.matchInRange(r, rng.Start, rng.End) {
					found = true
					break
				}
			}
			if found {
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
			// Cycle detection: if we've visited this same (pos, pc) before,
			// the loop body is matching empty strings — exit via branch B.
			key := [2]int{pos, pc}
			if vm.visitedSplits == nil {
				vm.visitedSplits = make(map[[2]int]bool)
			}
			if vm.visitedSplits[key] {
				// Empty-match cycle detected — skip A, take exit (B)
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

		case OpGreedyLoop, OpGreedyLoop1, OpGreedyLoopN:
			// ECMA-262 greedy quantifier semantics: match maximum iterations
			// inst.A = body start PC
			// inst.B = body end PC (one past last body instruction) = exit point
			// inst.Extra = max iterations (OpGreedyLoopN only; 0 = unlimited)
			//
			// OpGreedyLoop  = 0+ iterations (try exit first at current pos)
			// OpGreedyLoop1 = 1+ iterations (must match body at least once first)
			// OpGreedyLoopN = 0..N iterations (bounded, try exit first)
			//
			// The body is a CLOSED sub-range [A, B). We run the body via execBody
			// which treats pc=B as OpMatch (stop and report success/pos).
			// This prevents the body from falling through into the rest of the pattern.

			var bestResult matchResult
			visited := make(map[string]bool)
			maxIter := inst.Extra // 0 means unlimited for OpGreedyLoop/Loop1

			if inst.Op == OpGreedyLoop || inst.Op == OpGreedyLoopN {
				// 0+ iterations: try exiting immediately (zero body matches)
				resExit := vm.exec(pos, inst.B, copyGroups(groups))
				if resExit.matched {
					bestResult = resExit
				}
			}

			// Match iterations one by one, greedily
			currentPos := pos
			currentGroups := copyGroups(groups)
			iter := 0
			for {
				if maxIter > 0 && iter >= maxIter {
					break
				}

				stateKey := fmt.Sprintf("%d:%v", currentPos, currentGroups)
				if visited[stateKey] {
					break
				}
				visited[stateKey] = true

				// Run the body as a sub-expression bounded by [A, B)
				resBody := vm.execBody(currentPos, inst.A, inst.B, copyGroups(currentGroups))
				if !resBody.matched {
					break
				}

				// Body matched — try exiting and running the rest of the pattern from B
				resExit := vm.exec(resBody.pos, inst.B, copyGroups(resBody.groups))
				if resExit.matched {
					if !bestResult.matched || resExit.pos > bestResult.pos {
						bestResult = resExit
					}
				}

				// Continue to try another iteration
				currentPos = resBody.pos
				currentGroups = copyGroups(resBody.groups)
				iter++
			}

			if bestResult.matched {
				return bestResult
			}
			return matchResult{matched: false}

		case OpBackref:
			groupIdx := inst.A - 1 // 1-indexed to 0-indexed
			if groupIdx < 0 || groupIdx >= vm.NumGroups {
				return matchResult{matched: false}
			}
			start := groups[groupIdx*2+2] // +2 because group 0 is full match
			end := groups[groupIdx*2+2+1]
			if start < 0 || end < 0 {
				// Group hasn't been captured yet — ECMA-262: match empty string
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

		default:
			return matchResult{matched: false}
		}
	}
}

// execBody runs the body code in [startPC, endPC) as a bounded sub-VM.
// Unlike exec, it treats reaching endPC as a successful match (OpMatch sentinel).
// This isolates the body from the rest of the pattern so the body cannot
// fall through into later instructions.
func (vm *VM) execBody(pos, startPC, endPC int, groups []int) matchResult {
	bodyLen := endPC - startPC
	subCode := make([]Instruction, bodyLen+1)
	copy(subCode, vm.Code[startPC:endPC])
	subCode[bodyLen] = Instruction{Op: OpMatch}

	// Adjust internal jump targets relative to startPC
	for i := range subCode[:bodyLen] {
		switch subCode[i].Op {
		case OpJmp:
			subCode[i].A -= startPC
		case OpSplit:
			subCode[i].A -= startPC
			subCode[i].B -= startPC
		case OpGreedyLoop, OpGreedyLoop1, OpGreedyLoopN:
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

	totalGroups := vm.NumGroups + 1
	subGroups := make([]int, totalGroups*2)
	copy(subGroups, groups)

	res := subVM.exec(pos, 0, subGroups)
	if subVM.Err != nil {
		vm.Err = subVM.Err
	}
	return res
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
		case OpGreedyLoop, OpGreedyLoop1, OpGreedyLoopN:
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
// It tries progressively longer prefixes ending at pos.
func (vm *VM) executeLookbehind(startPC, endPC, pos int, groups []int) bool {
	// Build a sub-VM with only the lookbehind body + OpMatch
	bodyLen := endPC - startPC
	subCode := make([]Instruction, bodyLen+1)
	copy(subCode, vm.Code[startPC:endPC])
	subCode[bodyLen] = Instruction{Op: OpMatch}

	for i := range subCode[:bodyLen] {
		switch subCode[i].Op {
		case OpJmp:
			subCode[i].A -= startPC
		case OpSplit:
			subCode[i].A -= startPC
			subCode[i].B -= startPC
		case OpGreedyLoop, OpGreedyLoop1, OpGreedyLoopN:
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

	// Try every possible start position before pos
	for tryPos := 0; tryPos <= pos; tryPos++ {
		// Pass outer groups so backreferences inside lookbehind can see prior captures
		matched, endPos, subGroups := subVM.matchWithInitialGroups(vm.Input, tryPos, groups)
		if subVM.Err != nil {
			vm.Err = subVM.Err
			return false
		}
		if matched && endPos == pos {
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
	if vm.IgnoreCase {
		if vm.Unicode {
			// Full Unicode case folding
			return unicode.ToLower(input) == unicode.ToLower(pattern)
		}
		// Non-unicode mode: simple ASCII-ish case folding
		return toASCIILower(input) == toASCIILower(pattern)
	}
	return false
}

func (vm *VM) matchInRange(ch, start, end rune) bool {
	if vm.IgnoreCase {
		if vm.Unicode {
			lower := unicode.ToLower(ch)
			return lower >= unicode.ToLower(start) && lower <= unicode.ToLower(end)
		}
		lower := toASCIILower(ch)
		return lower >= toASCIILower(start) && lower <= toASCIILower(end)
	}
	return ch >= start && ch <= end
}

// toASCIILower does simple ASCII-range lower-casing (ECMA-262 non-unicode mode).
func toASCIILower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
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

func matchUnicodeProperty(r rune, prop string) bool {
	name, value := splitUnicodeProperty(prop)
	if name != "" {
		if isGeneralCategoryName(name) {
			return matchUnicodePropertyValue(r, value)
		}
		if isScriptName(name) {
			return matchUnicodeScript(r, value)
		}
		if isPropertyOfStrings(name) {
			// Property of strings (e.g. Emoji_Presentation) — treat as false for single char
			return false
		}
		return false
	}
	// Binary property or general category shorthand
	return matchUnicodeBinaryOrCategory(r, prop)
}

func matchUnicodeScript(r rune, script string) bool {
	// Normalize script name
	norm := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(script, "_", ""), " ", ""))
	// Look up in Go's unicode.Scripts table
	for name, rangeTable := range unicode.Scripts {
		if strings.ToLower(strings.ReplaceAll(name, "_", "")) == norm {
			return unicode.Is(rangeTable, r)
		}
	}
	// Also check unicode.Categories as some scripts are there
	return false
}

func isScriptName(name string) bool {
	switch name {
	case "script", "sc":
		return true
	}
	return false
}

func isPropertyOfStrings(name string) bool {
	return false // simplified
}

func matchUnicodeBinaryOrCategory(r rune, prop string) bool {
	norm := normalizeUnicodeProperty(prop)
	switch norm {
	// Binary properties
	case "ascii":
		return r <= 0x7F
	case "alphanumeric", "alnum":
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	case "alpha":
		return unicode.IsLetter(r)
	case "any":
		return true
	case "assigned":
		return r != unicode.ReplacementChar && unicode.IsGraphic(r)
	case "id_continue", "idcontinue":
		return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == 0x200C || r == 0x200D
	case "id_start", "idstart":
		return unicode.IsLetter(r) || r == '_'
	case "emoji":
		return unicode.Is(unicode.So, r) || r == 0x23 || r == 0x2A || (r >= 0x30 && r <= 0x39)
	// General categories (same as matchUnicodePropertyValue)
	default:
		return matchUnicodePropertyValue(r, prop)
	}
}

func matchUnicodePropertyValue(r rune, prop string) bool {
	norm := normalizeUnicodeProperty(prop)
	switch norm {
	case "l", "letter":
		return unicode.IsLetter(r)
	case "n", "number":
		return unicode.IsNumber(r)
	case "p", "punctuation":
		return unicode.IsPunct(r)
	case "s", "symbol":
		return unicode.IsSymbol(r)
	case "z", "separator":
		return unicode.IsSpace(r)
	case "c", "other":
		return unicode.IsControl(r)
	case "ll", "lowercaseletter":
		return unicode.IsLower(r)
	case "lu", "uppercaseletter":
		return unicode.IsUpper(r)
	case "lt", "titlecaseletter":
		return unicode.IsTitle(r)
	case "nd", "decimalnumber", "digit":
		// Decimal_Number (Nd) includes all Unicode decimal digits
		return unicode.IsDigit(r)
	case "whitespace":
		return unicode.IsSpace(r)
	default:
		return false
	}
}

func splitUnicodeProperty(prop string) (string, string) {
	parts := strings.SplitN(prop, "=", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return normalizeUnicodeProperty(parts[0]), parts[1]
}

func isGeneralCategoryName(name string) bool {
	switch normalizeUnicodeProperty(name) {
	case "gc", "generalcategory":
		return true
	default:
		return false
	}
}

func normalizeUnicodeProperty(prop string) string {
	prop = strings.TrimSpace(prop)
	prop = strings.ReplaceAll(prop, "_", "")
	prop = strings.ReplaceAll(prop, "-", "")
	prop = strings.ReplaceAll(prop, " ", "")
	return strings.ToLower(prop)
}
