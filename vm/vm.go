// Package vm implements the regex virtual machine
package vm

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// ErrStepLimit is returned when a match operation exceeds its execution
// budget, which bounds the cost of backtracking (ReDoS protection). It means
// "no answer", never "no match": the input may or may not match. The budget has
// two parts, both charged per match operation (a whole scan over start
// positions, including every lookaround evaluated during it):
//
//   - steps: MaxSteps if set, otherwise DefaultMaxSteps plus
//     DefaultStepsPerByte for every byte of input. The default grows linearly
//     with the input, so a search the engine completes in linear time fits it
//     at any input length as long as it averages at most DefaultStepsPerByte
//     instructions per byte (simple patterns such as ^[a-z]+$ or ^(a|b)+$
//     take a handful); only super-linear searches, the ReDoS case, run out;
//   - memory held for backtracking (the backtrack stack and the failure
//     memo): MaxMemory if set, otherwise DefaultMaxMemory plus
//     DefaultMemoryPerByte for every byte of input.
//
// Exceeding the step budget yields ErrStepLimit itself; exceeding the memory
// budget yields an error wrapping it. Test with errors.Is.
var ErrStepLimit = errors.New("regexp execution step limit exceeded")

const (
	// DefaultMaxSteps is the fixed part of the default step budget.
	DefaultMaxSteps = 1_000_000
	// DefaultStepsPerByte is the per-input-byte part of the default step
	// budget: a match operation on n bytes may take
	// DefaultMaxSteps + DefaultStepsPerByte*n steps.
	DefaultStepsPerByte = 100
	// DefaultMaxMemory is the fixed part, in bytes, of the default bound on
	// the memory a match operation may hold for backtracking.
	DefaultMaxMemory = 256 << 20
	// DefaultMemoryPerByte is the per-input-byte part of the default memory
	// bound. It covers one backtrack entry per input byte; linear-time
	// patterns need far less (a failure-memo bit per loop and position).
	DefaultMemoryPerByte = 32
)

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
	OpClass      // Match character class with escapes
	OpClassSet   // Match the v-mode class set in Set
	OpRuneSwitch // Match one rune and jump by it (see RuneSwitch)

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

	// Empty-iteration check for quantifiers whose body can match empty
	// (ECMA-262 RepeatMatcher). Mark slots follow the capture slots.
	OpMark          // Slot A = current position
	OpCheckProgress // Fail if the position equals slot A
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
	Set    *CharSet    // For OpClassSet
	Switch *RuneSwitch // For OpRuneSwitch
	Mode   Mode        // i, m and s inside a modifier group; 0 means the VM's
	AltA   []int       // Alternative group indices (for duplicate named groups in \k<name>)
}

// Mode is an instruction's own i, m and s flags, set by a modifier group such
// as (?i:...) or (?-m:...). When ModeSet is clear, the VM's flags apply.
type Mode uint8

const (
	ModeSet Mode = 1 << iota
	ModeIgnoreCase
	ModeMultiline
	ModeDotAll
)

func (vm *VM) ignoreCase(inst *Instruction) bool {
	if inst.Mode&ModeSet != 0 {
		return inst.Mode&ModeIgnoreCase != 0
	}
	return vm.IgnoreCase
}

func (vm *VM) multiline(inst *Instruction) bool {
	if inst.Mode&ModeSet != 0 {
		return inst.Mode&ModeMultiline != 0
	}
	return vm.Multiline
}

func (vm *VM) dotAll(inst *Instruction) bool {
	if inst.Mode&ModeSet != 0 {
		return inst.Mode&ModeDotAll != 0
	}
	return vm.DotAll
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
	case OpClassSet:
		return "class-set"
	case OpRuneSwitch:
		return fmt.Sprintf("rune-switch %d", len(i.Switch.Keys))
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
	case OpMark:
		return fmt.Sprintf("mark %d", i.A)
	case OpCheckProgress:
		return fmt.Sprintf("check-progress %d", i.A)
	default:
		return fmt.Sprintf("unknown(%d)", i.Op)
	}
}

// VM represents the regex virtual machine.
//
// The VM is a backtracking machine with an explicit, heap-allocated stack:
// pending alternatives and the capture-group writes they must undo are pushed
// onto it instead of being held in Go call frames, so matching never recurses
// per input character and cannot overflow the goroutine stack, whatever the
// input length. The only recursion is into lookaround bodies, bounded by the
// pattern's nesting depth. Work and memory are charged against the budget
// described at ErrStepLimit.
type VM struct {
	Code       []Instruction
	Input      string
	NumGroups  int
	IgnoreCase bool
	Multiline  bool
	DotAll     bool
	Unicode    bool
	MaxSteps   int // 0 means DefaultMaxSteps + DefaultStepsPerByte*len(Input)
	MaxMemory  int // bytes; 0 means DefaultMaxMemory + DefaultMemoryPerByte*len(Input)
	Err        error

	// Backward runs the engine right-to-left: character instructions consume the
	// rune ending at pos (pos decreases), and group save ops record the group's
	// far/near edge accordingly. Used for lookbehind, whose body is compiled in
	// reversed order so that RTL evaluation yields ECMA-262 capture semantics.
	Backward bool

	// Program optionally supplies the analysis of Code, built once with
	// NewProgram for this VM's flags and shared between VMs. If it is nil or
	// was built for a different configuration, the VM builds its own on first
	// use.
	Program *Program

	prog      *Program
	steps     int // current step count
	stepLimit int
	memLimit  int
	memoBytes int // memory held by active failure memos

	stack   []frame
	choices []int // stack indices of the choice frames, innermost last
	main    runState
	subs    []*runState // reusable lookaround states, indexed by depth-1

	// identity of the input the top-level memo was built for; see MatchAt.
	memoValid bool
	memoData  *byte
	memoLen   int
	memoProg  *Program
}

type frameKind uint8

const (
	// frameChoice resumes execution at pc with position a.
	frameChoice frameKind = iota
	// frameTrail restores groups[pc] = a and lastTrail[pc] = b when popped.
	frameTrail
	// frameGreedy holds the not-yet-tried exits of a greedy single-rune loop
	// (see greedyLoop): the loop started at a and the last exit tried was at
	// b; each pop tries the exit (pc) one rune closer to a, ending at a.
	frameGreedy
)

type frame struct {
	a, b int
	pc   int32
	kind frameKind
}

const frameBytes = int(unsafe.Sizeof(frame{}))

// errMemoryLimit is ErrStepLimit's memory-budget form.
var errMemoryLimit = fmt.Errorf("%w: backtracking memory limit exceeded", ErrStepLimit)

// runState is the per-invocation state of one run: the top-level match or one
// lookaround body.
type runState struct {
	groups     []int
	lastTrail  []int // stack index of the latest trail frame per slot, or -1
	base       int   // stack length when the run began
	choiceBase int   // len(choices) when the run began
	endPC      int   // reaching this pc is success (a lookaround body's end); -1 for none
	backward   bool
	depth      int
	memo       memo
	gid        int32 // interned id of groups, valid while gidOK
	gidOK      bool
}

func (st *runState) init(n int, from []int) {
	if cap(st.groups) < n {
		st.groups = make([]int, n)
		st.lastTrail = make([]int, n)
	}
	st.groups = st.groups[:n]
	st.lastTrail = st.lastTrail[:n]
	for i := range st.groups {
		st.groups[i] = -1
		st.lastTrail[i] = -1
	}
	copy(st.groups, from)
	st.gidOK = false
}

// readRune returns the rune to consume at pos and the position after consuming
// it in the given direction: forward reads the rune at pos and advances,
// backward reads the rune ending at pos and retreats. ok is false at the
// boundary (end of input forward, start of input backward).
func (vm *VM) readRune(pos int, backward bool) (r rune, next int, ok bool) {
	if backward {
		if pos <= 0 {
			return 0, pos, false
		}
		r, size := utf8.DecodeLastRuneInString(vm.Input[:pos])
		return r, pos - size, true
	}
	if pos >= len(vm.Input) {
		return 0, pos, false
	}
	if c := vm.Input[pos]; c < utf8.RuneSelf {
		return rune(c), pos + 1, true
	}
	r, size := utf8.DecodeRuneInString(vm.Input[pos:])
	return r, pos + size, true
}

// matchOne reports whether a single-rune instruction accepts r.
func (vm *VM) matchOne(inst *Instruction, r rune) bool {
	ic := vm.ignoreCase(inst)
	switch inst.Op {
	case OpChar:
		return vm.matchChar(ic, r, inst.Char)
	case OpAny:
		return vm.dotAll(inst) || !isLineTerminator(r)
	case OpDigit:
		// ECMA-262: \d matches only [0-9], not full Unicode digits
		return isECMADigit(r)
	case OpNonDigit:
		return !isECMADigit(r)
	case OpWord:
		return vm.wordChar(ic, r)
	case OpNonWord:
		return !vm.wordChar(ic, r)
	case OpSpace:
		return isSpace(r)
	case OpNonSpace:
		return !isSpace(r)
	case OpClass:
		matched := false
		for _, atom := range inst.Class {
			var atomMatch bool
			if atom.Kind == ClassAtomRange {
				atomMatch = vm.matchInRange(ic, r, atom.Range.Start, atom.Range.End)
			} else {
				// ECMA-262 CharacterSetMatcher: r matches when some rune of
				// its case folding orbit is in the escape's set, which for
				// \P{…} is the complement of the property. So under /ui,
				// [\P{Lu}] matches "A", whose orbit holds "a".
				atomMatch = vm.anyFold(ic, r, func(f rune) bool { return vm.matchEscape(ic, atom, f) != atom.Negated })
			}
			if atomMatch {
				matched = true
				break
			}
		}
		return matched != inst.Negate
	case OpClassSet:
		return vm.matchCharSet(ic, inst.Set, r)
	case OpRuneSwitch:
		_, ok := inst.Switch.target(vm.switchKey(ic, r))
		return ok
	case OpUnicodeProp:
		return vm.anyFold(ic, r, func(f rune) bool { return matchUnicodeProperty(f, inst.Prop) })
	case OpNotUnicodeProp:
		// Under u, \P{…} is the complement of the property, matched like any
		// set: /\P{Lu}/ui matches "A". (Under v the compiler emits a class set
		// instead, whose complement follows case folding.)
		return vm.anyFold(ic, r, func(f rune) bool { return !matchUnicodeProperty(f, inst.Prop) })
	}
	return false
}

// Match executes the VM against the input string starting at pos, as a single
// standalone attempt with a fresh budget. Returns (matched, endPos, groups).
func (vm *VM) Match(input string, pos int) (bool, int, []int) {
	vm.steps = 0
	vm.Err = nil
	vm.memoValid = false // force a fresh failure memo
	return vm.MatchAt(input, pos)
}

// MatchAt is like Match but does NOT reset the step counter or Err, so a caller
// scanning successive start positions shares a single budget. This keeps the
// ReDoS bound at one budget for a whole search instead of one per start
// position. Callers must inspect Err after the scan to distinguish "no match"
// from "budget exceeded".
//
// Successive MatchAt calls on the same input string also share the failure
// memo when that is sound (programs without backreferences), so states that
// already failed from an earlier start position are not explored again. The VM's configuration must not change between such calls.
func (vm *VM) MatchAt(input string, pos int) (bool, int, []int) {
	vm.prepare(input)
	if vm.Err != nil {
		return false, 0, nil
	}
	// A non-multiline ^ at the start of the program fails at any pos > 0.
	if vm.prog.anchored && pos > 0 {
		return false, 0, nil
	}

	st := &vm.main
	nCaptures := (vm.NumGroups + 1) * 2
	st.init(max(nCaptures, vm.prog.slots), nil)
	st.base, st.choiceBase = 0, 0
	st.endPC = -1
	st.backward = vm.Backward
	st.depth = 0
	vm.stack = vm.stack[:0]
	vm.choices = vm.choices[:0]

	matched, end := vm.run(st, 0, pos)
	vm.stack = vm.stack[:0]
	vm.choices = vm.choices[:0]

	// The memo may outlive this attempt only if every state it records has
	// failed, i.e. the attempt failed normally.
	if matched || vm.Err != nil || vm.prog.exact {
		vm.resetMemo(st)
		vm.memoValid = false
	}
	if !matched {
		return false, 0, nil
	}
	groups := make([]int, nCaptures)
	copy(groups, st.groups)
	return true, end, groups
}

// prepare binds the VM to input and its program, and computes the budget.
func (vm *VM) prepare(input string) {
	vm.Input = input
	if !vm.prog.matches(vm.Code, vm.IgnoreCase, vm.Multiline, vm.DotAll, vm.Unicode, vm.Backward) {
		if vm.Program.matches(vm.Code, vm.IgnoreCase, vm.Multiline, vm.DotAll, vm.Unicode, vm.Backward) {
			vm.prog = vm.Program
		} else {
			vm.prog = NewProgram(vm.Code, vm.IgnoreCase, vm.Multiline, vm.DotAll, vm.Unicode, vm.Backward)
		}
	}
	if !vm.memoValid || vm.memoData != unsafe.StringData(input) || vm.memoLen != len(input) || vm.memoProg != vm.prog {
		vm.resetMemo(&vm.main)
		vm.memoValid = true
		vm.memoData, vm.memoLen, vm.memoProg = unsafe.StringData(input), len(input), vm.prog
	}
	vm.stepLimit = vm.MaxSteps
	if vm.stepLimit <= 0 {
		vm.stepLimit = scaledLimit(DefaultMaxSteps, DefaultStepsPerByte, len(input))
	}
	vm.memLimit = vm.MaxMemory
	if vm.memLimit <= 0 {
		vm.memLimit = scaledLimit(DefaultMaxMemory, DefaultMemoryPerByte, len(input))
	}
}

// scaledLimit returns base + perByte*n, saturating instead of overflowing int
// (which is 32 bits on some platforms).
func scaledLimit(base, perByte, n int) int {
	const maxInt = int(^uint(0) >> 1)
	if n > (maxInt-base)/perByte {
		return maxInt
	}
	return base + perByte*n
}

func (vm *VM) resetMemo(st *runState) {
	vm.memoBytes -= st.memo.bytes
	st.memo.reset()
	st.gidOK = false
}

// step charges one step to the budget.
func (vm *VM) step() bool {
	vm.steps++
	if vm.steps > vm.stepLimit {
		vm.Err = ErrStepLimit
		return false
	}
	return true
}

// push appends f to the backtrack stack, charging its memory to the budget.
func (vm *VM) push(f frame) bool {
	vm.stack = append(vm.stack, f)
	if vm.memUsed() > vm.memLimit {
		vm.Err = errMemoryLimit
		return false
	}
	return true
}

// memUsed is the memory held for backtracking: the allocated backtrack stacks
// and the failure memos (whose map overhead is estimated).
func (vm *VM) memUsed() int {
	return cap(vm.stack)*frameBytes + cap(vm.choices)*8 + vm.memoBytes
}

func (vm *VM) pushChoice(f frame) bool {
	vm.choices = append(vm.choices, len(vm.stack))
	return vm.push(f)
}

// setGroup writes a capture slot, first trailing the old value if a choice
// point of this run could need it restored: that is, unless the slot was
// already trailed since the innermost choice point was pushed.
func (vm *VM) setGroup(st *runState, slot, val int) bool {
	old := st.groups[slot]
	if old == val {
		return true
	}
	if n := len(vm.choices); n > st.choiceBase && st.lastTrail[slot] < vm.choices[n-1] {
		if !vm.push(frame{kind: frameTrail, pc: int32(slot), a: old, b: st.lastTrail[slot]}) {
			return false
		}
		st.lastTrail[slot] = len(vm.stack) - 1
	}
	st.groups[slot] = val
	st.gidOK = false
	return true
}

// memoVisit reports whether the split state (pc, pos, the captures when the
// program needs them, and its iteration marks) was already visited, marking it
// if not. ok is false if marking exceeded the memory budget.
//
// An iteration mark enters the key only as whether it equals pos. That is all
// the rest of the run can observe of it: a mark is read only by its loop's
// progress check, positions move only one way within a run, and any mark the
// run can still read before overwriting it is at or behind pos in that
// direction — so one that differs from pos now differs from every later
// position its check can run at. (A mark the run cannot read, such as an
// enclosing run's, only splits states further, which is sound.) Keying on the
// exact value instead would make the states of an empty-matching loop
// quadratic in the input.
func (vm *VM) memoVisit(st *runState, pc, pos int) (visited, ok bool) {
	before := st.memo.bytes
	key := memoKey{pc: pc}
	lo := min(vm.prog.markLo, len(st.groups))
	if vm.prog.exact {
		if !st.gidOK {
			st.gid = st.memo.internGroups(st.groups[:lo])
			st.gidOK = true
		}
		key.gid = st.gid
	}
	for i, v := range st.groups[lo:] {
		if v == pos {
			key.marks |= 1 << i
		}
	}
	visited = st.memo.visit(key, pos)
	if d := st.memo.bytes - before; d != 0 {
		vm.memoBytes += d
		if vm.memUsed() > vm.memLimit {
			vm.Err = errMemoryLimit
			return visited, false
		}
	}
	return visited, true
}

// canStart reports whether execution from pc could possibly succeed given the
// next input byte in direction backward (see firstSet). false means it is
// certain to fail without consuming anything.
func (vm *VM) canStart(pc, pos int, backward bool) bool {
	if pc < 0 || pc >= len(vm.prog.first) {
		return true
	}
	fs := vm.prog.first[pc]
	if fs == nil || fs.any {
		return true
	}
	var c byte
	if backward {
		if pos <= 0 {
			return fs.atEdge
		}
		c = vm.Input[pos-1]
	} else {
		if pos >= len(vm.Input) {
			return fs.atEdge
		}
		c = vm.Input[pos]
	}
	return fs.bytes[c>>6]&(1<<(c&63)) != 0
}

// retreat returns the loop position one rune before cur on the path a greedy
// single-rune loop took from start, in the loop's direction. Decoding in the
// opposite direction reproduces that path except where start fell inside a
// multi-byte sequence, in which case the loop advanced byte by byte.
func (vm *VM) retreat(start, cur int, backward bool) int {
	if !backward {
		_, size := utf8.DecodeLastRuneInString(vm.Input[:cur])
		if p := cur - size; p >= start {
			return p
		}
		return cur - 1
	}
	_, size := utf8.DecodeRuneInString(vm.Input[cur:])
	if p := cur + size; p <= start {
		return p
	}
	return cur + 1
}

// backtrack pops the stack down to the innermost alternative of st, undoing
// capture writes on the way, and returns where to resume. found is false when
// st has no alternatives left (or the budget ran out; see Err).
func (vm *VM) backtrack(st *runState) (pc, pos int, found bool) {
	for len(vm.stack) > st.base {
		top := len(vm.stack) - 1
		f := vm.stack[top]
		switch f.kind {
		case frameTrail:
			st.groups[f.pc] = f.a
			st.lastTrail[f.pc] = f.b
			st.gidOK = false
			vm.stack = vm.stack[:top]
		case frameChoice:
			vm.stack = vm.stack[:top]
			vm.choices = vm.choices[:len(vm.choices)-1]
			return int(f.pc), f.a, true
		case frameGreedy:
			if !vm.step() {
				return 0, 0, false
			}
			p := vm.retreat(f.a, f.b, st.backward)
			if p == f.a {
				vm.stack = vm.stack[:top]
				vm.choices = vm.choices[:len(vm.choices)-1]
			} else {
				vm.stack[top].b = p
			}
			if vm.canStart(int(f.pc), p, st.backward) {
				return int(f.pc), p, true
			}
		default:
			panic("vm: corrupt backtrack stack")
		}
	}
	return 0, 0, false
}

// subState returns the reusable run state for a lookaround at depth.
func (vm *VM) subState(depth int) *runState {
	for len(vm.subs) < depth {
		vm.subs = append(vm.subs, &runState{})
	}
	return vm.subs[depth-1]
}

// run executes from (pc, pos) until success, returning the end position, or
// until every alternative of st is exhausted. The outcome of a run is that of
// the ECMA-262 backtracking semantics: alternatives are explored depth-first
// in priority order, exactly as a recursive backtracker would.
func (vm *VM) run(st *runState, pc, pos int) (bool, int) {
	code := vm.prog.code
	back := st.backward
	for {
		if pc == st.endPC {
			return true, pos
		}
		if !vm.step() {
			return false, 0
		}
		ok := true
		if pc < 0 || pc >= len(code) {
			ok = false
		} else {
			inst := &code[pc]
			switch inst.Op {
			case OpMatch:
				return true, pos

			case OpChar, OpAny, OpDigit, OpNonDigit, OpWord, OpNonWord, OpSpace, OpNonSpace,
				OpClass, OpClassSet, OpUnicodeProp, OpNotUnicodeProp:
				r, next, rok := vm.readRune(pos, back)
				if rok && vm.matchOne(inst, r) {
					pos = next
					pc++
				} else {
					ok = false
				}

			case OpRuneSwitch:
				r, next, rok := vm.readRune(pos, back)
				target, found := 0, false
				if rok {
					target, found = inst.Switch.target(vm.switchKey(vm.ignoreCase(inst), r))
				}
				if found {
					pos = next
					pc = target
				} else {
					ok = false
				}

			case OpStartLine:
				if pos == 0 {
					pc++
				} else if prevR, _ := utf8.DecodeLastRuneInString(vm.Input[:pos]); vm.multiline(inst) && isLineTerminator(prevR) {
					pc++
				} else {
					ok = false
				}

			case OpEndLine:
				if pos >= len(vm.Input) {
					pc++
				} else if r, _ := utf8.DecodeRuneInString(vm.Input[pos:]); vm.multiline(inst) && isLineTerminator(r) {
					pc++
				} else {
					ok = false
				}

			case OpWordBound:
				if ic := vm.ignoreCase(inst); vm.wordCharBefore(ic, pos) != vm.wordCharAt(ic, pos) {
					pc++
				} else {
					ok = false
				}

			case OpNonWordBound:
				if ic := vm.ignoreCase(inst); vm.wordCharBefore(ic, pos) == vm.wordCharAt(ic, pos) {
					pc++
				} else {
					ok = false
				}

			case OpSaveStart, OpSaveEnd:
				// When matching backward, OpSaveStart is reached at the group's
				// far (right) edge, so it records the end; OpSaveEnd, reached at
				// the near (left) edge, records the start. A group's stored range
				// is always [start, end].
				slot := inst.A * 2
				if (inst.Op == OpSaveEnd) != back {
					slot++
				}
				if !vm.setGroup(st, slot, pos) {
					return false, 0
				}
				pc++

			case OpJmp:
				pc = inst.A

			case OpSplit:
				if lp := &vm.prog.loops[pc]; lp.ok {
					// Greedy single-rune loop: consume as far as possible,
					// recording the exits given back on backtracking as one
					// frame rather than one choice per rune. Each position is
					// still a visit of this split, as in the expanded loop.
					start := pos
					body := &code[lp.body]
					for {
						visited, mok := vm.memoVisit(st, pc, pos)
						if !mok {
							return false, 0
						}
						if visited {
							break
						}
						r, next, rok := vm.readRune(pos, back)
						if !rok || !vm.matchOne(body, r) {
							break
						}
						if !vm.step() {
							return false, 0
						}
						pos = next
					}
					if pos != start && !lp.possessive {
						if !vm.pushChoice(frame{kind: frameGreedy, pc: int32(lp.exit), a: start, b: pos}) {
							return false, 0
						}
					}
					pc = lp.exit
					ok = vm.canStart(pc, pos, back)
					break
				}

				// The outcome of matching from a given state is a function of
				// that state, so once a split state has been visited its branch
				// A needs no second exploration: a revisit takes the exit B.
				// The state includes the captures when the program has
				// backreferences (which make outcomes depend on them); keying
				// on (pos, pc) alone there wrongly prunes branches reachable
				// with different captures, e.g. (?:(a)|a)(?:b|c)\1 on "ab". It
				// includes the iteration marks of loops whose body can match
				// empty, since those decide whether an iteration may end here.
				visited, mok := vm.memoVisit(st, pc, pos)
				if !mok {
					return false, 0
				}
				if visited {
					pc = inst.B
					break
				}
				// A branch that cannot start at this input is not explored (or
				// kept as an alternative): it would fail without consuming.
				aOK := vm.canStart(inst.A, pos, back)
				bOK := vm.canStart(inst.B, pos, back)
				switch {
				case aOK && bOK:
					if !vm.pushChoice(frame{kind: frameChoice, pc: int32(inst.B), a: pos}) {
						return false, 0
					}
					pc = inst.A
				case aOK:
					pc = inst.A
				case bOK:
					pc = inst.B
				default:
					ok = false
				}

			case OpBackref:
				var matched bool
				pos, matched = vm.matchBackref(inst, st.groups, pos, back)
				if matched {
					pc++
				} else {
					ok = false
				}

			case OpLookahead, OpNegLookahead, OpLookbehind, OpNegLookbehind:
				// inst.A..inst.B is the body; reaching inst.B (this
				// instruction's own pc) inside the body is success.
				sub := vm.subState(st.depth + 1)
				sub.init(len(st.groups), st.groups) // outer captures feed backreferences
				sub.base, sub.choiceBase = len(vm.stack), len(vm.choices)
				sub.endPC = inst.B
				sub.backward = inst.Op == OpLookbehind || inst.Op == OpNegLookbehind
				sub.depth = st.depth + 1
				matched, _ := vm.run(sub, inst.A, pos)
				// Lookarounds are atomic: the body's alternatives are dropped.
				vm.stack = vm.stack[:sub.base]
				vm.choices = vm.choices[:sub.choiceBase]
				vm.resetMemo(sub)
				if vm.Err != nil {
					return false, 0
				}
				positive := inst.Op == OpLookahead || inst.Op == OpLookbehind
				if matched != positive {
					ok = false
					break
				}
				if positive {
					// Propagate captures from the body back into outer groups.
					// Iteration marks stay behind: they belong to loops in the
					// body, and only the body's own runs may read them.
					for i, v := range sub.groups[:(vm.NumGroups+1)*2] {
						if v >= 0 && !vm.setGroup(st, i, v) {
							return false, 0
						}
					}
				}
				pc++

			case OpResetGroups:
				// Reset groups[A..B] (inclusive, 1-indexed) to -1: ECMA-262
				// group-reset semantics for quantifier body repeats.
				for g := inst.A; g <= inst.B; g++ {
					if g*2+1 < len(st.groups) {
						if !vm.setGroup(st, g*2, -1) || !vm.setGroup(st, g*2+1, -1) {
							return false, 0
						}
					}
				}
				pc++

			case OpMark:
				if !vm.setGroup(st, inst.A, pos) {
					return false, 0
				}
				pc++

			case OpCheckProgress:
				// An optional iteration that consumed nothing fails.
				if st.groups[inst.A] == pos {
					ok = false
				} else {
					pc++
				}

			default:
				ok = false
			}
		}
		if !ok {
			if vm.Err != nil {
				return false, 0
			}
			var found bool
			pc, pos, found = vm.backtrack(st)
			if !found {
				return false, 0
			}
		}
	}
}

// matchBackref matches the text of the referenced group at pos in the given
// direction, returning the new position.
func (vm *VM) matchBackref(inst *Instruction, groups []int, pos int, backward bool) (int, bool) {
	// For duplicate named groups (ES2022), try all alternative group indices
	// to find one that participated in the current match.
	start, end := -1, -1
	for i := -1; i < len(inst.AltA); i++ {
		gidx := inst.A
		if i >= 0 {
			gidx = inst.AltA[i]
		}
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
		return pos, true
	}

	refText := vm.Input[start:end]
	ic := vm.ignoreCase(inst)
	if backward {
		// Match the referenced text ending at pos, consuming backward.
		if ic {
			ok, consumed := vm.matchStringIgnoreCaseBackward(vm.Input[:pos], refText)
			return pos - consumed, ok
		}
		if pos < len(refText) || vm.Input[pos-len(refText):pos] != refText {
			return pos, false
		}
		return pos - len(refText), true
	}
	if ic {
		ok, consumed := vm.matchStringIgnoreCaseAt(vm.Input[pos:], refText)
		return pos + consumed, ok
	}
	if pos+len(refText) > len(vm.Input) || vm.Input[pos:pos+len(refText)] != refText {
		return pos, false
	}
	return pos + len(refText), true
}

// matchStringIgnoreCaseBackward matches ref against the end of sBefore
// (= vm.Input[:pos]) case-insensitively, consuming from the end, and returns
// how many bytes of sBefore were consumed.
func (vm *VM) matchStringIgnoreCaseBackward(sBefore, ref string) (bool, int) {
	si, ri := len(sBefore), len(ref)
	consumed := 0
	for ri > 0 {
		if si <= 0 {
			return false, 0
		}
		sr, sSize := utf8.DecodeLastRuneInString(sBefore[:si])
		rr, rSize := utf8.DecodeLastRuneInString(ref[:ri])
		if !vm.matchChar(true, sr, rr) {
			return false, 0
		}
		si -= sSize
		consumed += sSize
		ri -= rSize
	}
	return true, consumed
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
		if !vm.matchChar(true, sr, rr) {
			return false, 0
		}
		si += sSize
		consumed += sSize
		ri += rSize
	}
	return true, consumed
}

func (vm *VM) matchChar(ic bool, input, pattern rune) bool {
	if input == pattern {
		return true
	}
	if !ic {
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

func (vm *VM) matchInRange(ic bool, ch, start, end rune) bool {
	if ch >= start && ch <= end {
		return true
	}
	if !ic {
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

// wordCharBefore reports whether the character before pos is a word char.
// Handles multi-byte UTF-8 correctly using DecodeLastRuneInString.
func (vm *VM) wordCharBefore(ic bool, pos int) bool {
	if pos <= 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(vm.Input[:pos])
	if r == utf8.RuneError {
		return false
	}
	return vm.wordChar(ic, r)
}

// wordCharAt reports whether the character at pos is a word char.
func (vm *VM) wordCharAt(ic bool, pos int) bool {
	if pos >= len(vm.Input) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(vm.Input[pos:])
	if r == utf8.RuneError {
		return false
	}
	return vm.wordChar(ic, r)
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
	// Emoji properties: Go's unicode package has no tables for these, so
	// they come from tools/genemoji (emoji_props.go).
	"emoji":                     emojiProperty("Emoji"),
	"emojicomponent":            emojiProperty("Emoji_Component"),
	"ecomp":                     emojiProperty("Emoji_Component"),
	"emojimodifier":             emojiProperty("Emoji_Modifier"),
	"emod":                      emojiProperty("Emoji_Modifier"),
	"emojimodifierbase":         emojiProperty("Emoji_Modifier_Base"),
	"ebase":                     emojiProperty("Emoji_Modifier_Base"),
	"emojipresentation":         emojiProperty("Emoji_Presentation"),
	"epres":                     emojiProperty("Emoji_Presentation"),
	"extendedpictographic":      emojiProperty("Extended_Pictographic"),
	"extpict":                   emojiProperty("Extended_Pictographic"),
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
