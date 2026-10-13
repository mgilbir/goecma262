package vm

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"unsafe"
)

// Program is the read-only analysis of a compiled instruction slice for one
// flag configuration. The VM uses it to execute without a backtracking entry
// per character where none can matter (see docs/architecture.md, "Bounded
// execution"). Build it once with NewProgram and share it: a Program is
// immutable and safe for concurrent use by any number of VMs.
//
// Every fact recorded here is an over-approximation used only to skip work
// whose outcome is already known to be failure, so the match results
// (including capture groups) are identical to executing the instructions
// naively; only the number of steps and the memory used differ.
type Program struct {
	code                                   []Instruction
	ignoreCase, multiline, dotAll, unicode bool
	backward                               bool // direction of the top-level program
	exact                                  bool // memo keys must include capture groups
	anchored                               bool // top level begins with a non-multiline ^
	slots                                  int  // length of the slot vector the marks need
	// markLo is the first iteration-mark slot (marks occupy markLo..slots-1),
	// or noMarks when there are none or the memo must key on every slot's
	// exact value (more marks than its bitmask holds).
	markLo int
	dir    []bool
	first  []*firstSet  // indexed by pc; set for split targets and loop exits
	loops  []greedyLoop // indexed by pc; ok for recognised loops
	leaves leafCache    // only during construction

	footprintOnce sync.Once
	footprint     int64
}

// firstSet over-approximates what an execution starting at some pc can do
// next: the bytes that may begin the next rune it consumes (in its direction),
// and whether it can succeed when no rune is left to read. any means the
// analysis could not constrain it.
type firstSet struct {
	bytes  [4]uint64
	atEdge bool
	any    bool
}

var anyFirst = &firstSet{any: true}

// greedyLoop describes a greedy loop whose body is one single-rune
// instruction: split(pc) -> body -> back to split, exiting to exit. The VM
// scans such a loop without pushing a backtrack entry per iteration.
type greedyLoop struct {
	body, exit int
	ok         bool
	// possessive is true when no rune the body can consume can also start the
	// exit continuation, so giving back an iteration can never succeed.
	possessive bool
}

// noMarks is Program.markLo for a program whose slots are all keyed exactly.
const noMarks = int(^uint(0) >> 1)

// optimize enables the analysis-driven shortcuts. Tests turn it off to run the
// plain backtracking semantics (captures always in the memo key, every
// alternative explored, no loop scanning) as a reference for differential
// testing.
var optimize = true

// firstSetNodeLimit bounds the work spent computing one firstSet. A frontier
// larger than this is treated as unconstrained, which is always sound.
const firstSetNodeLimit = 64

// NewProgram analyses code for the given flags. backward is the direction the
// top-level program runs in (false for every program the compiler emits).
func NewProgram(code []Instruction, ignoreCase, multiline, dotAll, unicode, backward bool) *Program {
	p, _ := NewProgramContext(context.Background(), code, ignoreCase, multiline, dotAll, unicode, backward)
	return p
}

// NewProgramContext is NewProgram, but gives up when ctx is done, returning
// an error that wraps ctx.Err(). The analysis takes time linear in the
// program, and polls ctx as it goes.
func NewProgramContext(ctx context.Context, code []Instruction, ignoreCase, multiline, dotAll, unicode, backward bool) (*Program, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("analysing the program interrupted: %w", err)
	}
	poll := &analysisPoll{}
	if ctx.Done() != nil {
		poll.ctx = ctx
	}
	p := &Program{
		code:       code,
		ignoreCase: ignoreCase,
		multiline:  multiline,
		dotAll:     dotAll,
		unicode:    unicode,
		backward:   backward,
	}
	p.first = make([]*firstSet, len(code))
	p.loops = make([]greedyLoop, len(code))
	p.markLo = -1
	for _, inst := range code {
		if inst.Op == OpMark || inst.Op == OpCheckProgress {
			p.slots = max(p.slots, inst.A+1)
			if p.markLo < 0 || inst.A < p.markLo {
				p.markLo = inst.A
			}
		}
	}
	tooManyMarks := p.markLo >= 0 && p.slots-p.markLo > 64
	if p.markLo < 0 || tooManyMarks {
		p.markLo = noMarks
	}
	if !optimize {
		p.exact = true
		p.leaves = leafCache{}
		return p, nil
	}
	p.computeDirections()
	p.exact = hasBackref(code) || hasEpsilonCycle(code, poll) || tooManyMarks
	p.anchored = !backward && len(code) > 1 &&
		code[0].Op == OpSaveStart && code[0].A == 0 && code[1].Op == OpStartLine && !p.multilineAt(&code[1])

	walk := newFrontierWalker(len(code))
	need := func(pc int) {
		if pc >= 0 && pc < len(code) && p.first[pc] == nil {
			p.first[pc] = p.computeFirst(pc, walk)
		}
	}
	for pc := range code {
		if !poll.tick() {
			break
		}
		if code[pc].Op != OpSplit {
			continue
		}
		need(code[pc].A)
		need(code[pc].B)
		if body, exit, ok := p.greedyLoopAt(pc); ok {
			need(exit)
			leaf := p.leafBytes(body)
			fs := p.first[exit]
			possessive := !fs.any
			for i := range leaf {
				if leaf[i]&fs.bytes[i] != 0 {
					possessive = false
				}
			}
			p.loops[pc] = greedyLoop{body: body, exit: exit, ok: true, possessive: possessive}
		}
	}
	p.leaves = leafCache{} // only needed during construction; keeps Program immutable
	if poll.err != nil {
		return nil, poll.err
	}
	return p, nil
}

// analysisPoll polls a context every so many units of analysis.
type analysisPoll struct {
	ctx context.Context // nil if it cannot be cancelled
	n   int
	err error
}

// tick counts a unit of work, reporting whether the analysis may go on.
func (a *analysisPoll) tick() bool {
	if a.ctx == nil || a.err != nil {
		return a.err == nil
	}
	if a.n++; a.n%1024 == 0 {
		if err := a.ctx.Err(); err != nil {
			a.err = fmt.Errorf("analysing the program interrupted: %w", err)
		}
	}
	return a.err == nil
}

// matches reports whether p was built for exactly this configuration.
func (p *Program) matches(code []Instruction, ignoreCase, multiline, dotAll, unicode, backward bool) bool {
	return p != nil && len(p.code) == len(code) &&
		(len(code) == 0 || unsafe.SliceData(p.code) == unsafe.SliceData(code)) &&
		p.ignoreCase == ignoreCase && p.multiline == multiline && p.dotAll == dotAll &&
		p.unicode == unicode && p.backward == backward
}

// ignoreCaseAt, multilineAt and dotAllAt are inst's flags: its own inside a
// modifier group, else the program's.
func (p *Program) ignoreCaseAt(inst *Instruction) bool {
	if inst.Mode&ModeSet != 0 {
		return inst.Mode&ModeIgnoreCase != 0
	}
	return p.ignoreCase
}

func (p *Program) multilineAt(inst *Instruction) bool {
	if inst.Mode&ModeSet != 0 {
		return inst.Mode&ModeMultiline != 0
	}
	return p.multiline
}

func (p *Program) dotAllAt(inst *Instruction) bool {
	if inst.Mode&ModeSet != 0 {
		return inst.Mode&ModeDotAll != 0
	}
	return p.dotAll
}

// isSingleRune reports whether op consumes exactly one rune when it succeeds.
func isSingleRune(op Opcode) bool {
	switch op {
	case OpChar, OpAny, OpDigit, OpNonDigit, OpWord, OpNonWord, OpSpace, OpNonSpace,
		OpClass, OpClassSet, OpUnicodeProp, OpNotUnicodeProp:
		return true
	}
	return false
}

func isLookaround(op Opcode) bool {
	return op == OpLookahead || op == OpNegLookahead || op == OpLookbehind || op == OpNegLookbehind
}

// computeDirections records, for every pc, whether it executes right-to-left:
// lookbehind bodies do, lookahead bodies do not, and the innermost enclosing
// lookaround decides. Bodies nest properly, so painting outer bodies before
// inner ones (larger ranges first) leaves each pc with its innermost direction.
func (p *Program) computeDirections() {
	p.dir = make([]bool, len(p.code))
	for i := range p.dir {
		p.dir[i] = p.backward
	}
	type span struct {
		a, b int
		back bool
	}
	var spans []span
	for _, inst := range p.code {
		if isLookaround(inst.Op) && inst.A >= 0 && inst.B <= len(p.code) && inst.A <= inst.B {
			spans = append(spans, span{inst.A, inst.B, inst.Op == OpLookbehind || inst.Op == OpNegLookbehind})
		}
	}
	// By decreasing length. A counted quantifier can make tens of thousands
	// of lookarounds, so this must not be quadratic (an insertion sort took
	// a second over (?:(?:(?=a)){5000}){5}(?:(?:(?=ab)){5000}){5}). Spans of
	// equal length are disjoint, so their order does not matter.
	slices.SortStableFunc(spans, func(x, y span) int { return (y.b - y.a) - (x.b - x.a) })
	for _, s := range spans {
		for pc := s.a; pc < s.b; pc++ {
			p.dir[pc] = s.back
		}
	}
}

// hasBackref reports whether any instruction is a backreference. With
// backreferences, whether a state can succeed depends on the captures, so the
// failure memo must key on them.
func hasBackref(code []Instruction) bool {
	for _, inst := range code {
		if inst.Op == OpBackref {
			return true
		}
	}
	return false
}

// epsilonSuccs appends the successors of pc reachable without consuming input.
func epsilonSuccs(code []Instruction, pc int, out []int) []int {
	inst := code[pc]
	switch {
	case inst.Op == OpJmp:
		return append(out, inst.A)
	case inst.Op == OpSplit:
		return append(out, inst.A, inst.B)
	case inst.Op == OpMatch || isSingleRune(inst.Op) || inst.Op == OpRuneSwitch:
		return out
	case inst.Op == OpCheckProgress:
		// Every path back to a loop's split passes through its iteration's
		// mark and then this check, so one that consumed nothing fails here:
		// an empty-matching loop body is not a cycle that can be executed.
		return out
	default:
		// Saves, resets, anchors, word boundaries, lookarounds and
		// backreferences (which may match the empty string) fall through.
		return append(out, pc+1)
	}
}

// hasEpsilonCycle reports whether control can return to an instruction
// without consuming input. Only then can a state be revisited while it is
// still being explored, which is the one case where the failure memo's
// "already visited" answer changes results rather than merely pruning a
// subtree known to fail. The compiler guards every loop whose body can match
// empty with a progress check (see epsilonSuccs), so this holds only for
// hand-built programs.
func hasEpsilonCycle(code []Instruction, poll *analysisPoll) bool {
	const (
		white = iota
		grey
		black
	)
	color := make([]uint8, len(code))
	type item struct {
		pc    int
		succs []int
		next  int
	}
	for root := range code {
		if color[root] != white {
			continue
		}
		stack := []item{{pc: root, succs: epsilonSuccs(code, root, nil)}}
		color[root] = grey
		for len(stack) > 0 {
			if !poll.tick() {
				return false
			}
			top := &stack[len(stack)-1]
			if top.next == len(top.succs) {
				color[top.pc] = black
				stack = stack[:len(stack)-1]
				continue
			}
			s := top.succs[top.next]
			top.next++
			if s < 0 || s >= len(code) {
				continue
			}
			switch color[s] {
			case grey:
				return true
			case white:
				color[s] = grey
				stack = append(stack, item{pc: s, succs: epsilonSuccs(code, s, nil)})
			}
		}
	}
	return false
}

// greedyLoopAt recognises a greedy loop at split pc whose only work per
// iteration is one single-rune instruction: the A branch reaches that
// instruction through jumps only, and the instruction's successor reaches pc
// again through jumps only. This covers x*, x+ and the tail of x{n,} for any
// single-rune x without capture groups.
func (p *Program) greedyLoopAt(pc int) (body, exit int, ok bool) {
	code := p.code
	follow := func(t int) int {
		for i := 0; i <= len(code) && t >= 0 && t < len(code) && code[t].Op == OpJmp; i++ {
			t = code[t].A
		}
		return t
	}
	b := follow(code[pc].A)
	if b < 0 || b >= len(code) || !isSingleRune(code[b].Op) {
		return 0, 0, false
	}
	if follow(b+1) != pc {
		return 0, 0, false
	}
	return b, code[pc].B, true
}

// asciiOnly reports whether a single-rune instruction can only ever match
// ASCII runes, mirroring the matching code in vm.go exactly: under IgnoreCase
// ASCII letters fold to non-ASCII runes (e.g. k and KELVIN SIGN), and negated
// forms match non-ASCII by definition.
func (p *Program) asciiOnly(inst *Instruction) bool {
	switch inst.Op {
	case OpDigit:
		return true
	case OpWord:
		// Under IgnoreCase in Unicode mode \w also matches ſ and K.
		return !(p.ignoreCaseAt(inst) && p.unicode)
	case OpChar:
		return inst.Char < 0x80 && !p.ignoreCaseAt(inst)
	case OpClass:
		if inst.Negate || p.ignoreCaseAt(inst) {
			return false
		}
		for _, a := range inst.Class {
			if a.Negated {
				return false
			}
			switch a.Kind {
			case ClassAtomRange:
				if a.Range.End >= 0x80 {
					return false
				}
			case ClassAtomDigit, ClassAtomWord:
			default:
				return false
			}
		}
		return true
	}
	return false
}

// leafBytes returns the bytes that can begin (forward) or end (backward) a
// rune accepted by the single-rune instruction at pc. An ASCII byte is its own
// rune in either direction, so those bits are exact; any other byte belongs to
// a non-ASCII rune (or decodes to U+FFFD), so those bits are set unless the
// instruction is ASCII-only.
func (p *Program) leafBytes(pc int) [4]uint64 {
	inst := &p.code[pc]
	var s [4]uint64
	if lo, ok := p.asciiSetFast(inst); ok {
		s[0], s[1] = lo[0], lo[1]
	} else {
		// Matching every ASCII rune is the costly part, which the copies of
		// an instruction share.
		key := p.leafKeyOf(inst)
		set, cached := p.leaves.get(key)
		if !cached {
			set[0], set[1] = p.asciiSetSlow(inst)
			p.leaves.put(key, set)
		}
		s[0], s[1] = set[0], set[1]
	}
	if !p.asciiOnly(inst) {
		s[2], s[3] = ^uint64(0), ^uint64(0)
	}
	return s
}

// leafCache holds the ASCII sets worked out by matching every ASCII rune.
// Few instructions need that unless a program is large, so the first few are
// kept in a slice searched in order, and a map is made only past them: a map
// of these keys from the start cost every small compile about a kilobyte.
type leafCache struct {
	few  []leafEntry
	many map[leafKey][2]uint64
}

type leafEntry struct {
	key leafKey
	set [2]uint64
}

const leafFew = 8

func (c *leafCache) get(k leafKey) ([2]uint64, bool) {
	for i := range c.few {
		if c.few[i].key == k {
			return c.few[i].set, true
		}
	}
	s, ok := c.many[k]
	return s, ok
}

func (c *leafCache) put(k leafKey, s [2]uint64) {
	if len(c.few) < leafFew {
		if c.few == nil {
			c.few = make([]leafEntry, 0, leafFew)
		}
		c.few = append(c.few, leafEntry{k, s})
		return
	}
	if c.many == nil {
		c.many = make(map[leafKey][2]uint64)
	}
	c.many[k] = s
}

// leafKey identifies an instruction's ASCII set: the copies of a
// quantifier's body share their class data, so a program of many copies of a
// class works out its set once, where matching every ASCII rune for each copy
// dominated compiling such a program. The operand that owns data is keyed by
// its identity, which identifies its content, since none is ever modified.
type leafKey struct {
	op    Opcode
	flags uint8 // leafIgnoreCase (in effect at the instruction) and leafNegate
	char  rune
	ptr   unsafe.Pointer // the class atoms, class set, rune switch or property name
	n     int            // the number of atoms, or the property name's length
}

// Only . reads the s flag, and its set is never matched out, so the key has
// no need of it.
const (
	leafIgnoreCase = 1 << iota
	leafNegate
)

func (p *Program) leafKeyOf(inst *Instruction) leafKey {
	k := leafKey{op: inst.Op, char: inst.Char}
	if p.ignoreCaseAt(inst) {
		k.flags |= leafIgnoreCase
	}
	if inst.Negate {
		k.flags |= leafNegate
	}
	switch {
	case inst.Class != nil:
		k.ptr, k.n = unsafe.Pointer(unsafe.SliceData(inst.Class)), len(inst.Class)
	case inst.Set != nil:
		k.ptr = unsafe.Pointer(inst.Set)
	case inst.Switch != nil:
		k.ptr = unsafe.Pointer(inst.Switch)
	case inst.Prop != "":
		k.ptr, k.n = unsafe.Pointer(unsafe.StringData(inst.Prop)), len(inst.Prop)
	}
	return k
}

// asciiSetSlow returns the ASCII runes the single-rune instruction accepts, by
// evaluating the matcher on each of them.
func (p *Program) asciiSetSlow(inst *Instruction) (lo, hi uint64) {
	m := &VM{IgnoreCase: p.ignoreCase, Multiline: p.multiline, DotAll: p.dotAll, Unicode: p.unicode}
	for b := 0; b < 0x80; b++ {
		if m.matchOne(inst, rune(b)) {
			if b < 64 {
				lo |= 1 << b
			} else {
				hi |= 1 << (b - 64)
			}
		}
	}
	return lo, hi
}

// ASCII sets of the predicates single-rune instructions use, derived from the
// predicates themselves.
var asciiDigit, asciiWord, asciiSpace, asciiLineTerm = asciiSetOf(isECMADigit), asciiSetOf(isWordChar), asciiSetOf(isSpace), asciiSetOf(isLineTerminator)

func asciiSetOf(pred func(rune) bool) (s [2]uint64) {
	for b := 0; b < 0x80; b++ {
		if pred(rune(b)) {
			s[b>>6] |= 1 << (b & 63)
		}
	}
	return s
}

var asciiAll = [2]uint64{^uint64(0), ^uint64(0)}

func asciiNot(s [2]uint64) [2]uint64 { return [2]uint64{^s[0], ^s[1]} }

// asciiRange returns the ASCII part of [lo, hi].
func asciiRange(lo, hi rune) (s [2]uint64) {
	if hi > 0x7f {
		hi = 0x7f
	}
	if lo < 0 {
		lo = 0
	}
	for b := lo; b <= hi; b++ {
		s[b>>6] |= 1 << (b & 63)
	}
	return s
}

// asciiSetFast computes asciiSetSlow's result by set arithmetic for the
// instructions whose ASCII behaviour does not depend on case folding. ok is
// false for the rest, which asciiSetSlow handles. TestASCIISetFastMatchesSlow
// checks the two agree.
func (p *Program) asciiSetFast(inst *Instruction) (s [2]uint64, ok bool) {
	switch inst.Op {
	case OpChar:
		if p.ignoreCaseAt(inst) {
			return s, false
		}
		if inst.Char >= 0 && inst.Char < 0x80 {
			s[inst.Char>>6] |= 1 << (inst.Char & 63)
		}
		return s, true
	case OpAny:
		if p.dotAllAt(inst) {
			return asciiAll, true
		}
		return asciiNot(asciiLineTerm), true
	case OpDigit:
		return asciiDigit, true
	case OpNonDigit:
		return asciiNot(asciiDigit), true
	case OpWord:
		return asciiWord, true
	case OpNonWord:
		return asciiNot(asciiWord), true
	case OpSpace:
		return asciiSpace, true
	case OpNonSpace:
		return asciiNot(asciiSpace), true
	case OpClass:
		if p.ignoreCaseAt(inst) {
			return s, false
		}
		for _, a := range inst.Class {
			var as [2]uint64
			switch a.Kind {
			case ClassAtomRange:
				as = asciiRange(a.Range.Start, a.Range.End)
			case ClassAtomDigit:
				as = asciiDigit
			case ClassAtomWord:
				as = asciiWord
			case ClassAtomSpace:
				as = asciiSpace
			default:
				return s, false
			}
			if a.Negated {
				as = asciiNot(as)
			}
			s[0] |= as[0]
			s[1] |= as[1]
		}
		if inst.Negate {
			s = asciiNot(s)
		}
		return s, true
	}
	return s, false
}

// lineTerminatorBytes are the bytes that can begin (or end) a line
// terminator rune: \n, \r, and the non-ASCII U+2028/U+2029.
func lineTerminatorBytes() [4]uint64 {
	var s [4]uint64
	s[0] |= 1<<'\n' | 1<<'\r'
	s[2], s[3] = ^uint64(0), ^uint64(0)
	return s
}

type frontierWalker struct {
	seen  []uint32
	gen   uint32
	stack []int
}

func newFrontierWalker(n int) *frontierWalker {
	return &frontierWalker{seen: make([]uint32, n)}
}

// computeFirst walks the instructions reachable from pc without consuming
// input and unions the constraints of the first input-dependent instruction on
// every path. Any path it cannot bound (a backreference, a lookaround — whose
// instruction also marks the end of a lookaround body — the match itself, or a
// frontier too large to explore) makes the result unconstrained.
func (p *Program) computeFirst(start int, w *frontierWalker) *firstSet {
	w.gen++
	w.stack = append(w.stack[:0], start)
	fs := &firstSet{}
	visited := 0
	for len(w.stack) > 0 {
		pc := w.stack[len(w.stack)-1]
		w.stack = w.stack[:len(w.stack)-1]
		if pc < 0 || pc >= len(p.code) {
			return anyFirst
		}
		if w.seen[pc] == w.gen {
			continue
		}
		w.seen[pc] = w.gen
		visited++
		if visited > firstSetNodeLimit {
			return anyFirst
		}
		inst := &p.code[pc]
		switch {
		case isSingleRune(inst.Op) || inst.Op == OpRuneSwitch:
			// A rune switch consumes one rune too, though it continues at a
			// target of its own rather than at pc+1.
			leaf := p.leafBytes(pc)
			for i := range fs.bytes {
				fs.bytes[i] |= leaf[i]
			}
		case inst.Op == OpEndLine && !p.dir[pc]:
			// Forward, $ holds only at the end of input or, with m, before a
			// line terminator (which is then the next rune read).
			fs.atEdge = true
			if p.multilineAt(inst) {
				lt := lineTerminatorBytes()
				for i := range fs.bytes {
					fs.bytes[i] |= lt[i]
				}
			}
		case inst.Op == OpStartLine && p.dir[pc]:
			// Backward, ^ holds only at the start of input or, with m, after a
			// line terminator (which is then the next rune read).
			fs.atEdge = true
			if p.multilineAt(inst) {
				lt := lineTerminatorBytes()
				for i := range fs.bytes {
					fs.bytes[i] |= lt[i]
				}
			}
		case inst.Op == OpJmp:
			w.stack = append(w.stack, inst.A)
		case inst.Op == OpSplit:
			w.stack = append(w.stack, inst.B, inst.A)
		case inst.Op == OpSaveStart, inst.Op == OpSaveEnd, inst.Op == OpResetGroups,
			inst.Op == OpMark, inst.Op == OpCheckProgress,
			inst.Op == OpStartLine, inst.Op == OpEndLine,
			inst.Op == OpWordBound, inst.Op == OpNonWordBound:
			w.stack = append(w.stack, pc+1)
		default:
			return anyFirst
		}
	}
	return fs
}
