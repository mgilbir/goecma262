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
}

// Compile compiles a regex pattern AST to VM instructions.
// The returned group count is the parse-time total (pattern.NumGroups), so it
// includes groups that a {0} quantifier compiles zero times, and never counts
// a group more than once even inside a counted quantifier.
func Compile(pattern *parser.Pattern) ([]vm.Instruction, int, error) {
	c := &Compiler{
		code: make([]vm.Instruction, 0),
	}

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

func (c *Compiler) emit(inst vm.Instruction) int {
	idx := len(c.code)
	c.code = append(c.code, inst)
	return idx
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
		return c.compileNode(n.Body)
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

	// Capturing groups inside the body are reset to unset at the start of each
	// new iteration, per ECMA-262 RepeatMatcher group-reset semantics. Because
	// group indices are assigned once at parse time, every repetition of the
	// body re-emits the same save instructions (targeting the same slots), so
	// a group's value is that of its last participating iteration, and a
	// non-participating alternative in a later iteration clears it.
	loGroup, hiGroup, hasGroups := groupIndexRange(q.Body)
	emitReset := func() int {
		if !hasGroups {
			return -1
		}
		return c.emit(vm.Instruction{Op: vm.OpResetGroups, A: loGroup, B: hiGroup})
	}

	if q.Min == 0 && q.Max == -1 {
		// * quantifier
		// Structure: loopStart: split[A=reset?body, B=exit]; body; jmp loopStart; exit:
		loopStart := len(c.code)
		splitIdx := c.emit(vm.Instruction{Op: vm.OpSplit, A: 0, B: 0})
		bodyEntry := len(c.code) // points at the reset (or body if none)
		emitReset()
		err := c.compileNode(q.Body)
		if err != nil {
			return err
		}
		c.emit(vm.Instruction{Op: vm.OpJmp, A: loopStart})
		exitPos := len(c.code)
		if q.Greedy {
			c.code[splitIdx].A = bodyEntry
			c.code[splitIdx].B = exitPos
		} else {
			c.code[splitIdx].A = exitPos
			c.code[splitIdx].B = bodyEntry
		}

	} else if q.Min == 1 && q.Max == -1 {
		// + quantifier: body once (mandatory), then loop back with a reset.
		bodyStart := len(c.code)
		err := c.compileNode(q.Body)
		if err != nil {
			return err
		}
		splitIdx := c.emit(vm.Instruction{Op: vm.OpSplit, A: 0, B: 0})
		loopEntry := len(c.code)
		emitReset()
		c.emit(vm.Instruction{Op: vm.OpJmp, A: bodyStart})
		exitPos := len(c.code)
		if q.Greedy {
			c.code[splitIdx].A = loopEntry
			c.code[splitIdx].B = exitPos
		} else {
			c.code[splitIdx].A = exitPos
			c.code[splitIdx].B = loopEntry
		}

	} else if q.Min == 0 && q.Max == 1 {
		// ? quantifier: split L1, L2; L1: body; L2:
		splitIdx := c.emit(vm.Instruction{Op: vm.OpSplit, A: 0, B: 0})
		bodyStart := len(c.code)
		err := c.compileNode(q.Body)
		if err != nil {
			return err
		}
		c.code[splitIdx].A = bodyStart   // Try body
		c.code[splitIdx].B = len(c.code) // Or skip
		if !q.Greedy {
			c.code[splitIdx].A, c.code[splitIdx].B = c.code[splitIdx].B, c.code[splitIdx].A
		}

	} else {
		// {n,m} quantifier.
		// Emit the body q.Min times (the required minimum), resetting the body's
		// groups before every repetition after the first.
		for i := 0; i < q.Min; i++ {
			if i > 0 {
				emitReset()
			}
			if err := c.compileNode(q.Body); err != nil {
				return err
			}
		}

		if q.Max == -1 {
			// {n,} - unlimited tail: loop like * using OpSplit.
			loopStart := len(c.code)
			splitIdx := c.emit(vm.Instruction{Op: vm.OpSplit, A: 0, B: 0})
			bodyEntry := len(c.code)
			emitReset()
			if err := c.compileNode(q.Body); err != nil {
				return err
			}
			c.emit(vm.Instruction{Op: vm.OpJmp, A: loopStart})
			exitPos := len(c.code)
			if q.Greedy {
				c.code[splitIdx].A = bodyEntry
				c.code[splitIdx].B = exitPos
			} else {
				c.code[splitIdx].A = exitPos
				c.code[splitIdx].B = bodyEntry
			}
		} else if q.Max > q.Min {
			// {n,m}: emit the optional part as nested optionals, resetting the
			// body's groups before each optional repetition.
			optionalCount := q.Max - q.Min
			for i := 0; i < optionalCount; i++ {
				splitIdx := c.emit(vm.Instruction{Op: vm.OpSplit, A: 0, B: 0})
				bodyEntry := len(c.code)
				emitReset()
				if err := c.compileNode(q.Body); err != nil {
					return err
				}
				if q.Greedy {
					c.code[splitIdx].A = bodyEntry
					c.code[splitIdx].B = len(c.code)
				} else {
					c.code[splitIdx].A = len(c.code)
					c.code[splitIdx].B = bodyEntry
				}
			}
		}
	}

	return nil
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

	err := c.compileNode(l.Body)
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

	err := c.compileNode(l.Body)
	if err != nil {
		return err
	}

	bodyEnd := len(c.code)

	c.emit(vm.Instruction{Op: vm.OpNegLookbehind, A: bodyStart, B: bodyEnd})

	c.code[jmpIdx].A = bodyEnd

	return nil
}

func (c *Compiler) compileUnicodeProperty(u *parser.UnicodeProperty) error {
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

// fixedLength returns the fixed length of an expression if it is fixed-length.
// If the expression can match variable lengths, ok will be false.
func fixedLength(expr parser.Expression) (length int, ok bool) {
	switch e := expr.(type) {
	case *parser.Literal:
		return 1, true
	case *parser.Dot:
		return 1, true
	case *parser.CharacterClass:
		return 1, true
	case *parser.Digit, *parser.NonDigit, *parser.WordChar, *parser.NonWordChar, *parser.Whitespace, *parser.NonWhitespace, *parser.UnicodeProperty:
		return 1, true
	case *parser.Anchor:
		return 0, true
	case *parser.Group:
		return fixedLength(e.Body)
	case *parser.NamedGroup:
		return fixedLength(e.Body)
	case *parser.NonCapturingGroup:
		return fixedLength(e.Body)
	case *parser.Sequence:
		total := 0
		for _, elem := range e.Elements {
			l, ok := fixedLength(elem)
			if !ok {
				return 0, false
			}
			total += l
		}
		return total, true
	case *parser.Disjunction:
		if len(e.Alternatives) == 0 {
			return 0, true
		}
		l0, ok := fixedLength(e.Alternatives[0])
		if !ok {
			return 0, false
		}
		for i := 1; i < len(e.Alternatives); i++ {
			li, ok := fixedLength(e.Alternatives[i])
			if !ok || li != l0 {
				return 0, false
			}
		}
		return l0, true
	case *parser.Quantifier:
		if e.Min != e.Max {
			return 0, false
		}
		l, ok := fixedLength(e.Body)
		if !ok {
			return 0, false
		}
		return l * e.Min, true
	case *parser.Lookahead, *parser.NegativeLookahead, *parser.Lookbehind, *parser.NegativeLookbehind:
		// Lookarounds are zero-width, but they can be variable internally
		return 0, true
	case *parser.Backreference:
		return 0, false
	default:
		return 0, false
	}
}
