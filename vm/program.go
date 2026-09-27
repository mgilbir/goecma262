package vm

import "unsafe"

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
	markLo    int
	dir       []bool
	first     []*firstSet  // indexed by pc; set for split targets and loop exits
	loops     []greedyLoop // indexed by pc; ok for recognised loops
	leafCache map[int]*[4]uint64
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
	p := &Program{
		code:       code,
		ignoreCase: ignoreCase,
		multiline:  multiline,
		dotAll:     dotAll,
		unicode:    unicode,
		backward:   backward,
		leafCache:  make(map[int]*[4]uint64),
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
		p.leafCache = nil
		return p
	}
	p.computeDirections()
	p.exact = hasBackref(code) || hasEpsilonCycle(code) || tooManyMarks
	p.anchored = !backward && !multiline && len(code) > 1 &&
		code[0].Op == OpSaveStart && code[0].A == 0 && code[1].Op == OpStartLine

	walk := newFrontierWalker(len(code))
	need := func(pc int) {
		if pc >= 0 && pc < len(code) && p.first[pc] == nil {
			p.first[pc] = p.computeFirst(pc, walk)
		}
	}
	for pc := range code {
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
	p.leafCache = nil // only needed during construction; keeps Program immutable
	return p
}

// matches reports whether p was built for exactly this configuration.
func (p *Program) matches(code []Instruction, ignoreCase, multiline, dotAll, unicode, backward bool) bool {
	return p != nil && len(p.code) == len(code) &&
		(len(code) == 0 || unsafe.SliceData(p.code) == unsafe.SliceData(code)) &&
		p.ignoreCase == ignoreCase && p.multiline == multiline && p.dotAll == dotAll &&
		p.unicode == unicode && p.backward == backward
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
	// Insertion sort by decreasing length; lookaround counts are small.
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].b-spans[j].a > spans[j-1].b-spans[j-1].a; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
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
func hasEpsilonCycle(code []Instruction) bool {
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
	case OpDigit, OpWord:
		return true
	case OpChar:
		return inst.Char < 0x80 && !p.ignoreCase
	case OpClass:
		if inst.Negate || p.ignoreCase {
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
func (p *Program) leafBytes(pc int) *[4]uint64 {
	if s, ok := p.leafCache[pc]; ok {
		return s
	}
	inst := &p.code[pc]
	var s [4]uint64
	if lo, ok := p.asciiSetFast(inst); ok {
		s[0], s[1] = lo[0], lo[1]
	} else {
		s[0], s[1] = p.asciiSetSlow(inst)
	}
	if !p.asciiOnly(inst) {
		s[2], s[3] = ^uint64(0), ^uint64(0)
	}
	p.leafCache[pc] = &s
	return &s
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
		if p.ignoreCase {
			return s, false
		}
		if inst.Char >= 0 && inst.Char < 0x80 {
			s[inst.Char>>6] |= 1 << (inst.Char & 63)
		}
		return s, true
	case OpAny:
		if p.dotAll {
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
		if p.ignoreCase {
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
			if p.multiline {
				lt := lineTerminatorBytes()
				for i := range fs.bytes {
					fs.bytes[i] |= lt[i]
				}
			}
		case inst.Op == OpStartLine && p.dir[pc]:
			// Backward, ^ holds only at the start of input or, with m, after a
			// line terminator (which is then the next rune read).
			fs.atEdge = true
			if p.multiline {
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
