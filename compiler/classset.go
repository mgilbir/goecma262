package compiler

import (
	"fmt"
	"maps"
	"slices"

	"github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// classSetValue is an evaluated ClassSetExpression: its code points, and its
// strings that are not single code points (those are folded into chars).
type classSetValue struct {
	chars *vm.CharSet
	// strings is keyed by the string, case-canonicalized under IgnoreCase so
	// that set operations compare strings as the matcher will. A value's
	// strings are never modified once it is built, so values may share them.
	strings map[string]bool
	trie    *trieNode // of strings, if any (see setValue)
	// prop is set on the value of a property of strings, shared by every
	// compile (see propertyValue).
	prop *propertyKey
}

// sameProperty reports whether v and o are the value of the same property
// of strings.
func (v classSetValue) sameProperty(o classSetValue) bool {
	return v.prop != nil && o.prop != nil && *v.prop == *o.prop
}

// compileClassSet compiles a v-mode character class.
//
// A class holding strings compiles to an alternation tried longest first
// (ECMA-262 CompileAtom), then its single characters, then the empty string:
// [\q{ab|a}]b matches "ab" by falling back from "ab" to "a". The strings go
// into a trie whose nodes each dispatch on the next rune with one
// OpRuneSwitch, so a class such as \p{RGI_Emoji} (nearly 4,000 strings, 382
// possible first characters) costs a step per character read rather than one
// per candidate. The order is unchanged: two strings can both match at one
// position only if one is a prefix of the other, and a trie node tries its
// children, the longer strings, before ending the string there.
func (c *Compiler) compileClassSet(e *parser.ClassSetExpression) error {
	v := c.setValue(e)
	if err := c.poll.stopped(); err != nil {
		return err
	}
	if len(v.strings) == 0 {
		c.emit(vm.Instruction{Op: vm.OpClassSet, Set: v.chars})
		return nil
	}
	root := v.trie
	// At the root, the trie's children begin the strings of two or more code
	// points; its terminal is the empty string, tried after the characters.
	hasStrings := len(root.children) > 0
	alts := 1
	if hasStrings {
		alts++
	}
	if root.terminal {
		alts++
	}
	return c.emitAlternation(alts, func(i int) error {
		if hasStrings {
			if i == 0 {
				c.emitTrieChildren(root)
				return nil
			}
			i--
		}
		if i == 0 {
			c.emit(vm.Instruction{Op: vm.OpClassSet, Set: v.chars})
		}
		return nil // the empty string emits nothing
	})
}

// setValue returns e evaluated, with the trie of its strings if it holds
// any, once per expression: a counted quantifier compiles e once per copy.
func (c *Compiler) setValue(e *parser.ClassSetExpression) *classSetValue {
	if v, ok := c.classSets[e]; ok {
		return v
	}
	v := c.evalClassSet(e)
	if len(v.strings) > 0 {
		v.trie = &trieNode{}
		for s := range v.strings {
			v.trie.insert([]rune(s))
		}
	}
	if c.classSets == nil {
		c.classSets = make(map[*parser.ClassSetExpression]*classSetValue)
	}
	c.classSets[e] = &v
	return &v
}

type trieNode struct {
	char     rune
	children []*trieNode // sorted by char
	terminal bool        // a string ends here
}

func (n *trieNode) insert(s []rune) {
	for _, r := range s {
		i, found := slices.BinarySearchFunc(n.children, r, func(t *trieNode, r rune) int { return int(t.char - r) })
		if !found {
			n.children = slices.Insert(n.children, i, &trieNode{char: r})
		}
		n = n.children[i]
	}
	n.terminal = true
}

// emitTrieChildren emits code that reads the character of one of n's
// children and then the rest of a string through it, continuing after the
// emitted code.
func (c *Compiler) emitTrieChildren(n *trieNode) {
	sw := &vm.RuneSwitch{Keys: make([]rune, len(n.children)), Targets: make([]int, len(n.children))}
	c.emit(vm.Instruction{Op: vm.OpRuneSwitch, Switch: sw})
	var exits []int
	for i, child := range n.children {
		sw.Keys[i] = child.char
		sw.Targets[i] = len(c.code)
		c.emitTrieContinuation(child)
		if i < len(n.children)-1 {
			exits = append(exits, c.emit(vm.Instruction{Op: vm.OpJmp}))
		}
	}
	for _, j := range exits {
		c.code[j].A = len(c.code)
	}
}

// emitTrieContinuation emits the code after n's character has been read: a
// longer string through n's children if there is one, else the end of the
// string at n, when a string ends there.
func (c *Compiler) emitTrieContinuation(n *trieNode) {
	switch {
	case len(n.children) == 0:
		// A leaf is always a terminal: the string ends here.
	case !n.terminal:
		c.emitTrieChildren(n)
	default:
		_ = c.emitAlternation(2, func(i int) error {
			if i == 0 {
				c.emitTrieChildren(n)
			}
			return nil
		})
	}
}

func (c *Compiler) evalClassSet(e *parser.ClassSetExpression) classSetValue {
	v := c.evalClassSetNode(e.Body)
	if e.Negated {
		// The parser rejects a negated class that may contain strings.
		return classSetValue{chars: complementSet(v.chars)}
	}
	return v
}

func (c *Compiler) evalClassSetNode(n parser.ClassSetNode) classSetValue {
	switch n := n.(type) {
	case *parser.ClassSetCharacter:
		return classSetValue{chars: vm.NewCharSetRanges([]vm.RuneRange{{Start: n.Char, End: n.Char}})}
	case *parser.ClassSetRange:
		return classSetValue{chars: vm.NewCharSetRanges([]vm.RuneRange{{Start: n.Start, End: n.End}})}
	case *parser.ClassEscape:
		return classSetValue{chars: &vm.CharSet{Op: vm.CharSetEscape, Atom: classEscapeAtom(n)}}
	case *parser.ClassStrings:
		if backward, ok := sharedTable(n); ok {
			return propertyValue(propertyKey{n.Property, c.ignoreCase, backward})
		}
		return c.evalStrings(n)
	case *parser.ClassSetExpression:
		return c.evalClassSet(n)
	case *parser.ClassSetOperation:
		return c.evalClassSetOperation(n)
	}
	panic(fmt.Sprintf("unknown class set node %T", n))
}

// evalStrings evaluates a \q{...} or the members of a property of strings.
func (c *Compiler) evalStrings(n *parser.ClassStrings) classSetValue {
	var singles []vm.RuneRange
	strs := map[string]bool{}
	for _, s := range n.Strings {
		if c.poll.tick() != nil {
			break // the caller stops on poll.stopped()
		}
		if len(s) == 1 {
			singles = append(singles, vm.RuneRange{Start: s[0], End: s[0]})
			continue
		}
		strs[c.canonicalString(s)] = true
	}
	return classSetValue{chars: vm.NewCharSetRanges(singles), strings: strs}
}

func (c *Compiler) evalClassSetOperation(n *parser.ClassSetOperation) classSetValue {
	values := make([]classSetValue, 0, len(n.Items))
	for i, it := range n.Items {
		v := c.evalClassSetNode(it)
		if i > 0 && n.Kind != parser.SetSubtraction && v.prop != nil && slices.ContainsFunc(values, v.sameProperty) {
			// A ∪ A and A ∩ A are A: a property named again adds nothing,
			// and its strings need not be gone through again.
			continue
		}
		values = append(values, v)
	}
	switch n.Kind {
	case parser.SetIntersection:
		if len(values) == 1 {
			return values[0]
		}
		sets := make([]*vm.CharSet, len(values))
		strs := values[0].strings
		for i, v := range values {
			sets[i] = v.chars
			if i > 0 {
				strs = intersectStrings(strs, v.strings)
			}
		}
		return classSetValue{chars: &vm.CharSet{Op: vm.CharSetIntersection, Items: sets}, strings: strs}
	case parser.SetSubtraction:
		removed := unionValues(values[1:])
		strs := map[string]bool{}
		if values[0].prop == nil || !slices.ContainsFunc(values[1:], values[0].sameProperty) {
			// (A − A's strings are none, and need not be gone through.)
			for s := range values[0].strings {
				if !removed.strings[s] {
					strs[s] = true
				}
			}
		}
		chars := &vm.CharSet{Op: vm.CharSetIntersection, Items: []*vm.CharSet{values[0].chars, complementSet(removed.chars)}}
		return classSetValue{chars: chars, strings: strs}
	}
	return unionValues(values)
}

// unionValues returns the union of values, merging their plain ranges into one
// node so that a union of characters is a single binary search.
func unionValues(values []classSetValue) classSetValue {
	if len(values) == 1 {
		// Values are never modified, so the union of one is the value.
		return values[0]
	}
	var ranges []vm.RuneRange
	var others []*vm.CharSet
	// The strings of a single operand that has any are shared rather than
	// copied.
	var strs map[string]bool
	shared := false
	for _, v := range values {
		if v.chars.Op == vm.CharSetRanges {
			ranges = append(ranges, v.chars.Ranges...)
		} else {
			others = append(others, v.chars)
		}
		switch {
		case len(v.strings) == 0:
		case strs == nil:
			strs, shared = v.strings, true
		default:
			if shared {
				strs, shared = maps.Clone(strs), false
			}
			for s := range v.strings {
				strs[s] = true
			}
		}
	}
	chars := vm.NewCharSetRanges(ranges)
	if len(others) > 0 {
		if len(ranges) > 0 {
			others = append(others, chars)
		}
		chars = &vm.CharSet{Op: vm.CharSetUnion, Items: others}
		if len(others) == 1 {
			chars = others[0]
		}
	}
	return classSetValue{chars: chars, strings: strs}
}

func intersectStrings(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for s := range a {
		if b[s] {
			out[s] = true
		}
	}
	return out
}

func complementSet(s *vm.CharSet) *vm.CharSet {
	if s.Op == vm.CharSetComplement {
		return s.Items[0]
	}
	return &vm.CharSet{Op: vm.CharSetComplement, Items: []*vm.CharSet{s}}
}

// canonicalString returns s with each code point replaced by its
// vm.CanonicalFold when the pattern ignores case, so that strings matching the
// same text compare equal, and so that the trie's keys are the values
// OpRuneSwitch folds input runes to.
func (c *Compiler) canonicalString(s []rune) string {
	if !c.ignoreCase {
		return string(s)
	}
	out := make([]rune, len(s))
	for i, r := range s {
		out[i] = vm.CanonicalFold(r)
	}
	return string(out)
}

func classEscapeAtom(e *parser.ClassEscape) vm.ClassAtom {
	a := vm.ClassAtom{Negated: e.Negated, Prop: e.Property}
	switch e.Kind {
	case parser.ClassEscapeDigit:
		a.Kind = vm.ClassAtomDigit
	case parser.ClassEscapeWord:
		a.Kind = vm.ClassAtomWord
	case parser.ClassEscapeSpace:
		a.Kind = vm.ClassAtomSpace
	case parser.ClassEscapeUnicodeProperty:
		a.Kind = vm.ClassAtomUnicodeProp
	}
	return a
}

// reverseClassSet returns e for a lookbehind body, which is matched right to
// left: every string reversed, so that the trie shares suffixes, and longer
// strings are still tried first among those that can both end at a position.
func (c *Compiler) reverseClassSet(e *parser.ClassSetExpression) *parser.ClassSetExpression {
	return &parser.ClassSetExpression{Negated: e.Negated, Body: c.reverseClassSetNode(e.Body)}
}

func (c *Compiler) reverseClassSetNode(n parser.ClassSetNode) parser.ClassSetNode {
	switch n := n.(type) {
	case *parser.ClassStrings:
		if backward, ok := sharedTable(n); ok {
			t, _ := table(n.Property, !backward)
			return &parser.ClassStrings{Strings: t, Property: n.Property}
		}
		strs := make([][]rune, len(n.Strings))
		for i, s := range n.Strings {
			r := slices.Clone(s)
			slices.Reverse(r)
			strs[i] = r
		}
		return &parser.ClassStrings{Strings: strs, Property: n.Property}
	case *parser.ClassSetExpression:
		return c.reverseClassSet(n)
	case *parser.ClassSetOperation:
		items := make([]parser.ClassSetNode, len(n.Items))
		for i, it := range n.Items {
			items[i] = c.reverseClassSetNode(it)
		}
		return &parser.ClassSetOperation{Kind: n.Kind, Items: items}
	}
	return n // characters, ranges and escapes read the same both ways
}
