// Package compiler compiles AST to VM instructions
package compiler

import (
	"fmt"

	"github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// Compilation limits to prevent pathological patterns
const (
	MaxQuantifierRepeat = 10_000  // Max value of {n} / {n,m}
	MaxNestingDepth     = 200     // Max AST nesting depth during compilation
	MaxProgramSize      = 200_000 // Max total emitted instructions
)

// Compiler compiles regex AST to VM bytecode
type Compiler struct {
	code  []vm.Instruction
	depth int // current nesting depth

	// The flags a modifier group can change: those of the pattern, and
	// those in effect where code is being emitted.
	patternFlags, flags parser.Modifiers

	captureSlots int // (NumGroups+1)*2: registers are numbered from here
	registers    int // registers allocated
}

// Compile compiles a regex pattern AST to VM instructions.
// The returned group count is the parse-time total (pattern.NumGroups), so it
// includes groups that a {0} quantifier compiles zero times, and never counts
// a group more than once even inside a counted quantifier.
func Compile(pattern *parser.Pattern) ([]vm.Instruction, int, error) {
	c := &Compiler{
		code:         make([]vm.Instruction, 0),
		captureSlots: (pattern.NumGroups + 1) * 2,
	}
	if pattern.Flags.IgnoreCase {
		c.patternFlags |= parser.ModIgnoreCase
	}
	if pattern.Flags.Multiline {
		c.patternFlags |= parser.ModMultiline
	}
	if pattern.Flags.DotAll {
		c.patternFlags |= parser.ModDotAll
	}
	c.flags = c.patternFlags

	// Emit save instructions for group 0 (full match)
	c.emit(vm.Instruction{Op: vm.OpSaveStart, A: 0})

	err := c.compileNode(pattern.Body)
	if err != nil {
		return nil, 0, err
	}

	c.emit(vm.Instruction{Op: vm.OpSaveEnd, A: 0})

	// Add final match instruction
	c.emit(vm.Instruction{Op: vm.OpMatch})

	return c.code, pattern.NumGroups, nil
}

// emit appends inst, marking it with the flags that differ from the
// pattern's inside a modifier group (vm.Instruction.Mod).
func (c *Compiler) emit(inst vm.Instruction) int {
	idx := len(c.code)
	if c.flags != c.patternFlags {
		inst.Mod = c.modifiers()
	}
	c.code = append(c.code, inst)
	return idx
}

// modifiers returns the vm overrides that turn the pattern's flags into the
// current ones.
func (c *Compiler) modifiers() vm.Modifiers {
	var m vm.Modifiers
	for _, f := range []struct {
		flag       parser.Modifiers
		set, clear vm.Modifiers
	}{
		{parser.ModIgnoreCase, vm.ModIgnoreCase, vm.ModNoIgnoreCase},
		{parser.ModMultiline, vm.ModMultiline, vm.ModNoMultiline},
		{parser.ModDotAll, vm.ModDotAll, vm.ModNoDotAll},
	} {
		switch on, was := c.flags&f.flag != 0, c.patternFlags&f.flag != 0; {
		case on && !was:
			m |= f.set
		case !on && was:
			m |= f.clear
		}
	}
	return m
}

func (c *Compiler) patchJump(idx, target int) {
	c.code[idx].A = target
}

func (c *Compiler) compileNode(node parser.Node) error {
	// Bound total code size. MaxQuantifierRepeat caps a single quantifier, but
	// nested quantifiers multiply (e.g. ((a{100}){100}){100} ~ 10^6 instrs), so
	// the product must be bounded independently to prevent memory blowup.
	if len(c.code) > MaxProgramSize {
		return fmt.Errorf("compiled program too large (limit: %d instructions)", MaxProgramSize)
	}
	c.depth++
	if c.depth > MaxNestingDepth {
		return fmt.Errorf("pattern too deeply nested (limit: %d)", MaxNestingDepth)
	}
	defer func() { c.depth-- }()

	switch n := node.(type) {
	case *parser.Disjunction:
		return c.compileDisjunction(n)
	case *parser.Sequence:
		return c.compileSequence(n)
	case *parser.Literal:
		return c.compileLiteral(n)
	case *parser.CharacterClass:
		return c.compileCharacterClass(n)
	case *parser.Dot:
		c.emit(vm.Instruction{Op: vm.OpAny})
		return nil
	case *parser.Quantifier:
		return c.compileQuantifier(n)
	case *parser.Group:
		return c.compileGroup(n)
	case *parser.NamedGroup:
		return c.compileNamedGroup(n)
	case *parser.NonCapturingGroup:
		if n.Add == 0 && n.Remove == 0 {
			return c.compileNode(n.Body)
		}
		// A modifier group: its body is compiled with the flags it sets.
		outer := c.flags
		c.flags = (c.flags | n.Add) &^ n.Remove
		err := c.compileNode(n.Body)
		c.flags = outer
		return err
	case *parser.Lookahead:
		return c.compileLookahead(n)
	case *parser.NegativeLookahead:
		return c.compileNegativeLookahead(n)
	case *parser.Lookbehind:
		return c.compileLookbehind(n)
	case *parser.NegativeLookbehind:
		return c.compileNegativeLookbehind(n)
	case *parser.Backreference:
		return c.compileBackreference(n)
	case *parser.Anchor:
		return c.compileAnchor(n)
	case *parser.WordChar:
		c.emit(vm.Instruction{Op: vm.OpWord})
		return nil
	case *parser.NonWordChar:
		c.emit(vm.Instruction{Op: vm.OpNonWord})
		return nil
	case *parser.Digit:
		c.emit(vm.Instruction{Op: vm.OpDigit})
		return nil
	case *parser.NonDigit:
		c.emit(vm.Instruction{Op: vm.OpNonDigit})
		return nil
	case *parser.Whitespace:
		c.emit(vm.Instruction{Op: vm.OpSpace})
		return nil
	case *parser.NonWhitespace:
		c.emit(vm.Instruction{Op: vm.OpNonSpace})
		return nil
	case *parser.UnicodeProperty:
		return c.compileUnicodeProperty(n)
	default:
		return fmt.Errorf("unknown node type: %T", node)
	}
}

func (c *Compiler) compileDisjunction(d *parser.Disjunction) error {
	if len(d.Alternatives) == 1 {
		return c.compileNode(d.Alternatives[0])
	}

	// Correct NFA structure for alternation (a|b|c|...):
	//   split1[A=alt1_body, B=split2]
	//   alt1_body
	//   jmp end
	//   split2[A=alt2_body, B=split3]
	//   alt2_body
	//   jmp end
	//   ...
	//   last_alt_body
	//   end:
	//
	// Each split's B must point to the NEXT split, not the next body.
	// This ensures the engine tries each alternative in order.

	splitIdxs := make([]int, 0, len(d.Alternatives)-1)
	jumpIdxs := make([]int, 0, len(d.Alternatives)-1)

	for i := 0; i < len(d.Alternatives)-1; i++ {
		// Emit split: A=body (patched below), B=next split (patched below)
		splitIdx := c.emit(vm.Instruction{Op: vm.OpSplit, A: 0, B: 0})
		splitIdxs = append(splitIdxs, splitIdx)

		// Patch A to point to this alternative's body (immediately after the split)
		c.code[splitIdx].A = len(c.code)

		// Compile this alternative
		err := c.compileNode(d.Alternatives[i])
		if err != nil {
			return err
		}

		// Jump to end after this alternative succeeds
		jumpIdx := c.emit(vm.Instruction{Op: vm.OpJmp, A: 0})
		jumpIdxs = append(jumpIdxs, jumpIdx)

		// Patch previous split's B to point to this new split (or the last alt body)
		if i > 0 {
			c.code[splitIdxs[i-1]].B = splitIdx
		}
	}

	// Patch first split's B to point to second split (if >2 alternatives, already done above)
	// For exactly 2 alternatives, patch split[0].B to the last alt body
	// For >2, the last split's B needs to point to the last alt body
	lastAltStart := len(c.code)

	// Last alternative (no split, no jump needed)
	err := c.compileNode(d.Alternatives[len(d.Alternatives)-1])
	if err != nil {
		return err
	}

	endPos := len(c.code)

	// Patch the last split's B to point to the last alternative body
	c.code[splitIdxs[len(splitIdxs)-1]].B = lastAltStart

	// Patch all jumps to end
	for _, jumpIdx := range jumpIdxs {
		c.code[jumpIdx].A = endPos
	}

	return nil
}

func (c *Compiler) compileSequence(s *parser.Sequence) error {
	for _, elem := range s.Elements {
		err := c.compileNode(elem)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Compiler) compileLiteral(l *parser.Literal) error {
	c.emit(vm.Instruction{Op: vm.OpChar, Char: l.Char})
	return nil
}

func (c *Compiler) compileCharacterClass(cc *parser.CharacterClass) error {
	classAtoms := make([]vm.ClassAtom, 0, len(cc.Atoms))
	for _, atom := range cc.Atoms {
		switch a := atom.(type) {
		case *parser.ClassLiteral:
			classAtoms = append(classAtoms, vm.ClassAtom{
				Kind:  vm.ClassAtomRange,
				Range: vm.RuneRange{Start: a.Char, End: a.Char},
			})
		case *parser.ClassRange:
			classAtoms = append(classAtoms, vm.ClassAtom{
				Kind:  vm.ClassAtomRange,
				Range: vm.RuneRange{Start: a.Start, End: a.End},
			})
		case *parser.ClassEscape:
			switch a.Kind {
			case parser.ClassEscapeDigit:
				classAtoms = append(classAtoms, vm.ClassAtom{Kind: vm.ClassAtomDigit, Negated: a.Negated})
			case parser.ClassEscapeWord:
				classAtoms = append(classAtoms, vm.ClassAtom{Kind: vm.ClassAtomWord, Negated: a.Negated})
			case parser.ClassEscapeSpace:
				classAtoms = append(classAtoms, vm.ClassAtom{Kind: vm.ClassAtomSpace, Negated: a.Negated})
			case parser.ClassEscapeUnicodeProperty:
				if !vm.ValidUnicodeProperty(a.Property) {
					return fmt.Errorf("invalid unicode property escape: \\p{%s}", a.Property)
				}
				classAtoms = append(classAtoms, vm.ClassAtom{Kind: vm.ClassAtomUnicodeProp, Prop: a.Property, Negated: a.Negated})
			}
		default:
			return fmt.Errorf("unknown class atom type: %T", atom)
		}
	}

	c.emit(vm.Instruction{Op: vm.OpClass, Class: classAtoms, Negate: cc.Negated})
	return nil
}

func (c *Compiler) compileQuantifier(q *parser.Quantifier) error {
	// Enforce complexity limits
	if q.Min > MaxQuantifierRepeat {
		return fmt.Errorf("quantifier minimum %d exceeds limit %d", q.Min, MaxQuantifierRepeat)
	}
	if q.Max > MaxQuantifierRepeat {
		return fmt.Errorf("quantifier maximum %d exceeds limit %d", q.Max, MaxQuantifierRepeat)
	}

	// The layout follows ECMA-262 RepeatMatcher: the body is emitted once per
	// required iteration (q.Min times), and then either as a loop (no upper
	// bound) or as q.Max-q.Min optional iterations, each of which may be
	// declined, which ends the repetition.
	//
	// Capturing groups inside the body are reset to unset at the start of each
	// iteration (RepeatMatcher step 3). Because group indices are assigned once
	// at parse time, every repetition of the body re-emits the same save
	// instructions (targeting the same slots), so a group's value is that of
	// its last participating iteration, and a non-participating alternative in
	// a later iteration clears it. The first iteration needs no reset: nothing
	// inside the body can have set those groups before it, as an enclosing
	// quantifier resets them too.
	loGroup, hiGroup, hasGroups := groupIndexRange(q.Body)
	emitReset := func() {
		if hasGroups {
			c.emit(vm.Instruction{Op: vm.OpResetGroups, A: loGroup, B: hiGroup})
		}
	}

	// An optional iteration (one beyond the minimum) that matches the empty
	// string fails (RepeatMatcher step 2.b). A body that cannot match empty
	// can never trip that check, so it is emitted only for bodies that can:
	// OpLoopEnter records the position where the iteration starts in a
	// register, and OpLoopCheck fails if the body ended there.
	reg := -1
	if nullable(q.Body) {
		reg = c.newRegister()
	}
	// optionalBody emits one optional iteration.
	optionalBody := func() error {
		if reg >= 0 {
			c.emit(vm.Instruction{Op: vm.OpLoopEnter, A: reg})
		}
		emitReset()
		if err := c.compileNode(q.Body); err != nil {
			return err
		}
		if reg >= 0 {
			c.emit(vm.Instruction{Op: vm.OpLoopCheck, A: reg})
		}
		return nil
	}
	// split emits a choice between an optional iteration starting right after
	// it and the target patched later, in the quantifier's priority order.
	split := func() int { return c.emit(vm.Instruction{Op: vm.OpSplit}) }
	patchSplit := func(idx, body, decline int) {
		if q.Greedy {
			c.code[idx].A, c.code[idx].B = body, decline
		} else {
			c.code[idx].A, c.code[idx].B = decline, body
		}
	}

	// The required iterations. A + over a body that cannot match empty loops
	// back into its single copy (below) instead of emitting a second one.
	required := q.Min
	plusLoop := q.Min == 1 && q.Max == -1 && reg < 0
	if plusLoop {
		required = 0
	}
	for i := 0; i < required; i++ {
		if i > 0 {
			emitReset()
		}
		if err := c.compileNode(q.Body); err != nil {
			return err
		}
	}

	switch {
	case plusLoop:
		// bodyStart: body; split(loopEntry, exit); loopEntry: reset; jmp bodyStart
		bodyStart := len(c.code)
		if err := c.compileNode(q.Body); err != nil {
			return err
		}
		splitIdx := split()
		loopEntry := len(c.code)
		emitReset()
		c.emit(vm.Instruction{Op: vm.OpJmp, A: bodyStart})
		patchSplit(splitIdx, loopEntry, len(c.code))

	case q.Max == -1:
		// loop: split(body, exit); body: [enter] reset; body; [check]; jmp loop
		loopStart := split()
		bodyEntry := len(c.code)
		if err := optionalBody(); err != nil {
			return err
		}
		c.emit(vm.Instruction{Op: vm.OpJmp, A: loopStart})
		patchSplit(loopStart, bodyEntry, len(c.code))

	default:
		// Each optional iteration: split(body, end); body. Declining one
		// ends the repetition.
		var splits []int
		for i := 0; i < q.Max-q.Min; i++ {
			idx := split()
			splits = append(splits, idx)
			c.code[idx].A = len(c.code) // body entry, until patched
			if err := optionalBody(); err != nil {
				return err
			}
		}
		end := len(c.code)
		for _, idx := range splits {
			patchSplit(idx, c.code[idx].A, end)
		}
	}
	return nil
}

// newRegister allocates a slot, past the capture slots, for the start
// position of an iteration of a loop whose body can match empty.
func (c *Compiler) newRegister() int {
	r := c.captureSlots + c.registers
	c.registers++
	return r
}

// nullable reports whether e can match the empty string. It may say true for
// an expression that cannot (that only costs an unneeded check), but never
// false for one that can.
func nullable(e parser.Expression) bool {
	switch n := e.(type) {
	case *parser.Literal, *parser.CharacterClass, *parser.Dot, *parser.WordChar, *parser.NonWordChar,
		*parser.Digit, *parser.NonDigit, *parser.Whitespace, *parser.NonWhitespace, *parser.UnicodeProperty:
		return false
	case *parser.Backreference:
		// A backreference matches the empty string when its group is unset or
		// empty; an Annex B fallback is literal characters.
		return n.Fallback == nil
	case *parser.Sequence:
		for _, el := range n.Elements {
			if !nullable(el) {
				return false
			}
		}
		return true
	case *parser.Disjunction:
		for _, alt := range n.Alternatives {
			if nullable(alt) {
				return true
			}
		}
		return false
	case *parser.Group:
		return nullable(n.Body)
	case *parser.NamedGroup:
		return nullable(n.Body)
	case *parser.NonCapturingGroup:
		return nullable(n.Body)
	case *parser.Quantifier:
		return n.Min == 0 || nullable(n.Body)
	default:
		// Anchors and lookarounds are zero-width; anything else is assumed
		// nullable.
		return true
	}
}

func (c *Compiler) compileGroup(g *parser.Group) error {
	c.emit(vm.Instruction{Op: vm.OpSaveStart, A: g.Index})
	err := c.compileNode(g.Body)
	if err != nil {
		return err
	}
	c.emit(vm.Instruction{Op: vm.OpSaveEnd, A: g.Index})
	return nil
}

func (c *Compiler) compileNamedGroup(g *parser.NamedGroup) error {
	c.emit(vm.Instruction{Op: vm.OpSaveStart, A: g.Index})
	err := c.compileNode(g.Body)
	if err != nil {
		return err
	}
	c.emit(vm.Instruction{Op: vm.OpSaveEnd, A: g.Index})
	return nil
}

func (c *Compiler) compileBackreference(b *parser.Backreference) error {
	// An Annex B numeric escape that did not resolve to a real group compiles to
	// literal characters (a legacy octal escape and/or literal digits).
	if b.Fallback != nil {
		for _, r := range b.Fallback {
			c.emit(vm.Instruction{Op: vm.OpChar, Char: r})
		}
		return nil
	}
	c.emit(vm.Instruction{Op: vm.OpBackref, A: b.Index, AltA: b.AltIndices})
	return nil
}

func (c *Compiler) compileAnchor(a *parser.Anchor) error {
	switch a.Type {
	case parser.StartOfLine:
		c.emit(vm.Instruction{Op: vm.OpStartLine})
	case parser.EndOfLine:
		c.emit(vm.Instruction{Op: vm.OpEndLine})
	case parser.WordBoundary:
		c.emit(vm.Instruction{Op: vm.OpWordBound})
	case parser.NonWordBoundary:
		c.emit(vm.Instruction{Op: vm.OpNonWordBound})
	}
	return nil
}

// compileLookahead compiles a positive lookahead (?=...).
// Layout: OpJmp(afterBody) | <body code> | OpLookahead(bodyStart, bodyEnd)
// The body code is jumped over so it doesn't execute inline.
// The OpLookahead instruction references the body range for the sub-VM.
func (c *Compiler) compileLookahead(l *parser.Lookahead) error {
	// Emit jump to skip over the body
	jmpIdx := c.emit(vm.Instruction{Op: vm.OpJmp, A: 0})

	bodyStart := len(c.code)

	err := c.compileNode(l.Body)
	if err != nil {
		return err
	}

	bodyEnd := len(c.code)

	// Emit the lookahead instruction with body range
	c.emit(vm.Instruction{Op: vm.OpLookahead, A: bodyStart, B: bodyEnd})

	// Patch the jump to point past the OpLookahead
	c.code[jmpIdx].A = bodyEnd

	return nil
}

func (c *Compiler) compileNegativeLookahead(l *parser.NegativeLookahead) error {
	jmpIdx := c.emit(vm.Instruction{Op: vm.OpJmp, A: 0})

	bodyStart := len(c.code)

	err := c.compileNode(l.Body)
	if err != nil {
		return err
	}

	bodyEnd := len(c.code)

	c.emit(vm.Instruction{Op: vm.OpNegLookahead, A: bodyStart, B: bodyEnd})

	c.code[jmpIdx].A = bodyEnd

	return nil
}

func (c *Compiler) compileLookbehind(l *parser.Lookbehind) error {
	jmpIdx := c.emit(vm.Instruction{Op: vm.OpJmp, A: 0})

	bodyStart := len(c.code)

	// Compile the body in reversed order; the VM runs the lookbehind sub-VM
	// backward, so a reversed program matched right-to-left reproduces ECMA-262
	// lookbehind evaluation (including capture-group and backreference order).
	err := c.compileNode(reverseExpr(l.Body))
	if err != nil {
		return err
	}

	bodyEnd := len(c.code)

	c.emit(vm.Instruction{Op: vm.OpLookbehind, A: bodyStart, B: bodyEnd})

	c.code[jmpIdx].A = bodyEnd

	return nil
}

func (c *Compiler) compileNegativeLookbehind(l *parser.NegativeLookbehind) error {
	jmpIdx := c.emit(vm.Instruction{Op: vm.OpJmp, A: 0})

	bodyStart := len(c.code)

	err := c.compileNode(reverseExpr(l.Body))
	if err != nil {
		return err
	}

	bodyEnd := len(c.code)

	c.emit(vm.Instruction{Op: vm.OpNegLookbehind, A: bodyStart, B: bodyEnd})

	c.code[jmpIdx].A = bodyEnd

	return nil
}

// reverseExpr returns a structurally reversed copy of e for right-to-left
// lookbehind compilation: sequences are emitted last-element-first and group
// bodies are reversed, while capture indices are preserved. Single-width atoms
// (literals, classes, escapes, anchors, backreferences) are unchanged, and
// nested lookarounds are treated as atomic — they carry their own direction and
// reverse (or not) their own bodies when compiled.
func reverseExpr(e parser.Expression) parser.Expression {
	switch n := e.(type) {
	case *parser.Sequence:
		rev := make([]parser.Expression, len(n.Elements))
		for i, el := range n.Elements {
			rev[len(n.Elements)-1-i] = reverseExpr(el)
		}
		return &parser.Sequence{Elements: rev}
	case *parser.Disjunction:
		alts := make([]parser.Expression, len(n.Alternatives))
		for i, a := range n.Alternatives {
			alts[i] = reverseExpr(a)
		}
		return &parser.Disjunction{Alternatives: alts}
	case *parser.Quantifier:
		return &parser.Quantifier{Min: n.Min, Max: n.Max, Greedy: n.Greedy, Body: reverseExpr(n.Body)}
	case *parser.Group:
		return &parser.Group{Index: n.Index, Body: reverseExpr(n.Body)}
	case *parser.NamedGroup:
		return &parser.NamedGroup{Index: n.Index, Name: n.Name, Body: reverseExpr(n.Body)}
	case *parser.NonCapturingGroup:
		return &parser.NonCapturingGroup{Add: n.Add, Remove: n.Remove, Body: reverseExpr(n.Body)}
	default:
		return e
	}
}

func (c *Compiler) compileUnicodeProperty(u *parser.UnicodeProperty) error {
	if !vm.ValidUnicodeProperty(u.Property) {
		return fmt.Errorf("invalid unicode property escape: \\p{%s}", u.Property)
	}
	if u.Negated {
		c.emit(vm.Instruction{Op: vm.OpNotUnicodeProp, Prop: u.Property})
	} else {
		c.emit(vm.Instruction{Op: vm.OpUnicodeProp, Prop: u.Property})
	}
	return nil
}

// groupIndexRange returns the contiguous range [lo, hi] of capture-group indices
// defined anywhere within node, and whether any exist. Because indices are
// assigned in source order and a subtree spans a contiguous source range, the
// groups it contains always form a contiguous index range — so a single
// OpResetGroups over [lo, hi] resets exactly the body's captures.
func groupIndexRange(node parser.Expression) (lo, hi int, has bool) {
	var walk func(parser.Expression)
	walk = func(n parser.Expression) {
		switch e := n.(type) {
		case *parser.Group:
			consider(e.Index, &lo, &hi, &has)
			walk(e.Body)
		case *parser.NamedGroup:
			consider(e.Index, &lo, &hi, &has)
			walk(e.Body)
		case *parser.NonCapturingGroup:
			walk(e.Body)
		case *parser.Disjunction:
			for _, alt := range e.Alternatives {
				walk(alt)
			}
		case *parser.Sequence:
			for _, elem := range e.Elements {
				walk(elem)
			}
		case *parser.Quantifier:
			walk(e.Body)
		case *parser.Lookahead:
			walk(e.Body)
		case *parser.NegativeLookahead:
			walk(e.Body)
		case *parser.Lookbehind:
			walk(e.Body)
		case *parser.NegativeLookbehind:
			walk(e.Body)
		}
	}
	walk(node)
	return lo, hi, has
}

func consider(idx int, lo, hi *int, has *bool) {
	if !*has {
		*lo, *hi, *has = idx, idx, true
		return
	}
	if idx < *lo {
		*lo = idx
	}
	if idx > *hi {
		*hi = idx
	}
}
