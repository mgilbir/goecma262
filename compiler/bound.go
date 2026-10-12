package compiler

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/mgilbir/goecma262/internal/memsize"
	"github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// Cost is what compiling a pattern costs, as Bound reports it.
type Cost struct {
	// Instructions is the number of instructions Compile emits for the
	// pattern: exactly when Exact is set, otherwise an upper bound.
	Instructions int
	// Bytes bounds the memory the compiled program holds: the Footprint of
	// the vm.Program built from Compile's result.
	Bytes int64
	// Exact reports whether Instructions is exact. It is false only for a
	// pattern with a v-mode class whose strings are not the members of one
	// property of strings, such as [\q{ab|cd}] or [\p{RGI_Emoji}--\q{ab}]:
	// its trie is bounded from the strings' lengths rather than built.
	Exact bool
}

// Bound returns what Compile would spend on pattern, without compiling it, so
// that a caller can refuse a pattern before paying for it: nested counted
// quantifiers multiply, and (a{1000}){1000} would be a million instructions.
// It takes time linear in the size of the AST, allocates next to nothing,
// and saturates rather than overflow.
//
// It returns the error Compile would for a pattern nested deeper than
// MaxNestingDepth, a quantifier bound over MaxQuantifierRepeat or a node of
// an unknown type, but it does not compare the result with MaxProgramSize:
// Compile refuses a pattern whose exact size is over it before emitting
// anything.
func Bound(pattern *parser.Pattern) (Cost, error) {
	b := &bounder{
		ignoreCase:  pattern.Flags.IgnoreCase,
		unicodeSets: pattern.Flags.UnicodeSets,
		exact:       true,
	}
	body, err := b.node(pattern.Body)
	if err != nil {
		return Cost{}, err
	}
	// Group 0's saves and the final match.
	n := memsize.Add(body.instructions, 3)
	owned := memsize.Add(b.shared, body.owned)
	cost := Cost{
		Instructions: int(min(n, int64(maxInt))),
		Bytes:        vm.FootprintBound(n, body.splits, owned),
		Exact:        b.exact,
	}
	return cost, nil
}

const maxInt = int(^uint(0) >> 1)

// bounder walks an AST as Compile does, but visits a quantifier's body once
// and multiplies, where Compile emits it once per copy.
type bounder struct {
	depth int
	// ignoreCase is the i flag in effect, which a modifier group changes,
	// and backward is whether the code is emitted for a lookbehind body.
	ignoreCase, backward, unicodeSets bool
	exact                             bool
	// shared adds up the data the copies of a node share (see the Compiler's
	// memo fields): it is counted once, however many copies there are.
	shared  int64
	altSeen map[*int]bool // the group lists of backreferences counted

	// c, if set, is the compiler about to compile the pattern. The bounder
	// then counts exactly, evaluating class sets through c, which keeps them
	// for the code; Bytes is not computed.
	c *Compiler
}

// size is the cost of one copy of a node's code.
type size struct {
	instructions, splits int64
	owned                int64 // data each copy owns: rune switches, \P{…} under v
}

func (s size) plus(o size) size {
	return size{memsize.Add(s.instructions, o.instructions), memsize.Add(s.splits, o.splits), memsize.Add(s.owned, o.owned)}
}

func (s size) times(n int64) size {
	return size{memsize.Mul(s.instructions, n), memsize.Mul(s.splits, n), memsize.Mul(s.owned, n)}
}

// emits is the size of n instructions that own nothing, splits of them
// OpSplit.
func emits(n, splits int64) size { return size{instructions: n, splits: splits} }

func (b *bounder) share(bytes int64) { b.shared = memsize.Add(b.shared, bytes) }

func (b *bounder) node(node parser.Node) (size, error) {
	b.depth++
	if b.depth > MaxNestingDepth {
		return size{}, limitf(ErrNestingTooDeep, "pattern too deeply nested (limit: %d)", MaxNestingDepth)
	}
	defer func() { b.depth-- }()

	switch n := node.(type) {
	case *parser.Disjunction:
		// A split and a jump for every alternative but the last.
		k := int64(len(n.Alternatives))
		s := emits(2*(k-1), k-1)
		for _, alt := range n.Alternatives {
			as, err := b.node(alt)
			if err != nil {
				return size{}, err
			}
			s = s.plus(as)
		}
		return s, nil
	case *parser.Sequence:
		var s size
		for _, el := range n.Elements {
			es, err := b.node(el)
			if err != nil {
				return size{}, err
			}
			s = s.plus(es)
		}
		return s, nil
	case *parser.Literal, *parser.Dot, *parser.Anchor,
		*parser.WordChar, *parser.NonWordChar, *parser.Digit, *parser.NonDigit,
		*parser.Whitespace, *parser.NonWhitespace:
		return emits(1, 0), nil
	case *parser.CharacterClass:
		// The atoms, shared by every copy.
		b.share(memsize.Round(memsize.Mul(int64(len(n.Atoms)), int64(unsafe.Sizeof(vm.ClassAtom{})))))
		for _, a := range n.Atoms {
			if e, ok := a.(*parser.ClassEscape); ok {
				b.share(memsize.RoundNoScan(int64(len(e.Property))))
			}
		}
		return emits(1, 0), nil
	case *parser.ClassSetExpression:
		return b.classSet(n), nil
	case *parser.Quantifier:
		return b.quantifier(n)
	case *parser.Group:
		return b.wrapped(n.Body, 2)
	case *parser.NamedGroup:
		return b.wrapped(n.Body, 2)
	case *parser.NonCapturingGroup:
		return b.node(n.Body)
	case *parser.ModifierGroup:
		ic := b.ignoreCase
		if n.Add&parser.ModIgnoreCase != 0 {
			b.ignoreCase = true
		}
		if n.Remove&parser.ModIgnoreCase != 0 {
			b.ignoreCase = false
		}
		s, err := b.node(n.Body)
		b.ignoreCase = ic
		return s, err
	case *parser.Lookahead:
		return b.lookaround(n.Body, false)
	case *parser.NegativeLookahead:
		return b.lookaround(n.Body, false)
	case *parser.Lookbehind:
		return b.lookaround(n.Body, true)
	case *parser.NegativeLookbehind:
		return b.lookaround(n.Body, true)
	case *parser.Backreference:
		if n.Fallback != nil {
			return emits(int64(len(n.Fallback)), 0), nil
		}
		// The parser gives every reference to a name the same list.
		if alt := unsafe.SliceData(n.AltIndices); alt != nil && !b.altSeen[alt] {
			if b.altSeen == nil {
				b.altSeen = make(map[*int]bool)
			}
			b.altSeen[alt] = true
			b.share(memsize.RoundNoScan(memsize.Mul(int64(cap(n.AltIndices)), int64(unsafe.Sizeof(int(0))))))
		}
		return emits(1, 0), nil
	case *parser.UnicodeProperty:
		b.share(memsize.RoundNoScan(int64(len(n.Property))))
		s := emits(1, 0)
		if n.Negated && b.unicodeSets {
			// A class set of its own, built for each copy.
			s.owned = memsize.Round(int64(unsafe.Sizeof(vm.CharSet{})))
		}
		return s, nil
	}
	return size{}, fmt.Errorf("unknown node type: %T", node)
}

// wrapped is the size of body plus extra instructions around it.
func (b *bounder) wrapped(body parser.Expression, extra int64) (size, error) {
	s, err := b.node(body)
	if err != nil {
		return size{}, err
	}
	return s.plus(emits(extra, 0)), nil
}

// lookaround is the size of a lookaround: its body between a jump and the
// lookaround instruction.
func (b *bounder) lookaround(body parser.Expression, behind bool) (size, error) {
	if behind && b.c != nil {
		// What the compiler compiles, so that it finds the sets evaluated.
		body = b.c.reverseBody(body)
	}
	back := b.backward
	b.backward = behind
	s, err := b.wrapped(body, 2)
	b.backward = back
	return s, err
}

// quantifier mirrors compileQuantifier.
func (b *bounder) quantifier(q *parser.Quantifier) (size, error) {
	if q.Min > MaxQuantifierRepeat {
		return size{}, limitf(ErrQuantifierTooLarge, "quantifier minimum %d exceeds limit %d", q.Min, MaxQuantifierRepeat)
	}
	if q.Max > MaxQuantifierRepeat {
		return size{}, limitf(ErrQuantifierTooLarge, "quantifier maximum %d exceeds limit %d", q.Max, MaxQuantifierRepeat)
	}
	if q.Max == 0 {
		return size{}, nil
	}
	body, err := b.node(q.Body)
	if err != nil {
		return size{}, err
	}
	var reset, checks int64
	if _, _, has := groupIndexRange(q.Body); has {
		reset = 1
	}
	if canMatchEmpty(q.Body) {
		checks = 2 // a mark and a progress check
	}
	// An unbounded tail (and +): a split, the checks, the reset, the body
	// and a jump.
	loop := body.plus(emits(2+checks+reset, 1))
	if q.Min == 0 && q.Max == -1 || q.Min == 1 && q.Max == -1 {
		return loop, nil
	}
	lo, hi := int64(q.Min), int64(q.Max)
	var s size
	if lo > 0 {
		// The mandatory copies, with a reset before each after the first.
		s = body.times(lo).plus(emits(memsize.Mul(lo-1, reset), 0))
	}
	if q.Max == -1 {
		return s.plus(loop), nil
	}
	// The optional copies: a split, the checks, the reset and the body.
	return s.plus(body.plus(emits(1+checks+reset, 1)).times(hi - lo)), nil
}

// classSet mirrors compileClassSet: the instructions of one copy, which owns
// its trie's rune switches, while the evaluated set is shared.
func (b *bounder) classSet(e *parser.ClassSetExpression) size {
	var t trieSize
	if b.c != nil {
		b.c.ignoreCase = b.ignoreCase
		v := b.c.setValue(e)
		if len(v.strings) == 0 {
			return emits(1, 0)
		}
		t = measureTrie(v.trie)
	} else {
		strs, chars, _ := b.setNode(e)
		b.share(chars)
		switch strs.kind {
		case noStrings:
			return emits(1, 0)
		case oneTable:
			t = tableStats(strs.table).trie
		default:
			b.exact = false
			t = boundTrie(strs.count, strs.runes)
		}
	}
	// An alternation of the strings, the characters and the empty string:
	// a split and a jump for each alternative but the last.
	alts := int64(1)
	if t.hasStrings {
		alts++
	}
	if t.empty {
		alts++
	}
	return size{
		instructions: memsize.Add(t.instructions, 1+2*(alts-1)),
		splits:       memsize.Add(t.splits, alts-1),
		owned:        t.switchBytes,
	}
}

// stringsBound describes the strings, other than single characters, that a
// v-mode class set holds.
type stringsBound struct {
	kind int
	// table is, for oneTable, the property and the context its trie is
	// compiled in. (No member of today's properties has a case fold, so
	// ignoreCase does not change their tries, but it would if one did.)
	table propertyKey
	// count and runes bound the number of strings and the code points in
	// them all, whatever the kind.
	count, runes int64
}

const (
	noStrings = iota
	oneTable  // exactly the members of one property of strings
	bounded   // count and runes bound them
)

var charSetBytes = memsize.Round(int64(unsafe.Sizeof(vm.CharSet{})))

// rangesBytes bounds a slice of n ranges built by appending one at a time,
// whose capacity is at most twice its length.
func rangesBytes(n int64) int64 {
	return memsize.RoundNoScan(memsize.Mul(n, 2*int64(unsafe.Sizeof(vm.RuneRange{}))))
}

// pointersBytes bounds a slice of n *vm.CharSet built by appending.
func pointersBytes(n int64) int64 {
	return memsize.Round(memsize.Mul(n, 2*int64(unsafe.Sizeof((*vm.CharSet)(nil)))))
}

// setNode mirrors evalClassSetNode: what strings the set holds, a bound on
// the bytes of the vm.CharSet tree that evaluating it builds (which the
// program may keep), and the number of ranges its result may carry into a
// union.
func (b *bounder) setNode(n parser.ClassSetNode) (strs stringsBound, bytes, ranges int64) {
	switch n := n.(type) {
	case *parser.ClassSetCharacter, *parser.ClassSetRange:
		return stringsBound{}, charSetBytes + rangesBytes(1), 1
	case *parser.ClassEscape:
		b.share(memsize.RoundNoScan(int64(len(n.Property))))
		return stringsBound{}, charSetBytes, 0
	case *parser.ClassStrings:
		if key, ok := b.tableOf(n); ok {
			st := tableStats(key)
			strs = stringsBound{kind: oneTable, table: key, count: st.count, runes: st.runes}
			return strs, st.charsBytes, st.ranges
		}
		var singles int64
		for _, s := range n.Strings {
			if len(s) == 1 {
				singles++
				continue
			}
			strs.count++
			strs.runes = memsize.Add(strs.runes, int64(len(s)))
		}
		if strs.count > 0 {
			strs.kind = bounded
		}
		return strs, charSetBytes + rangesBytes(singles), singles
	case *parser.ClassSetExpression:
		strs, bytes, ranges = b.setNode(n.Body)
		if n.Negated {
			// The complement holds no strings.
			return stringsBound{}, bytes + charSetBytes + pointersBytes(1), 0
		}
		return strs, bytes, ranges
	case *parser.ClassSetOperation:
		k := int64(len(n.Items))
		items := make([]stringsBound, 0, 4)
		for _, it := range n.Items {
			s, by, r := b.setNode(it)
			items = append(items, s)
			bytes = memsize.Add(bytes, by)
			ranges = memsize.Add(ranges, r)
		}
		// A union merges its operands' ranges into a new node and holds
		// the rest in a union node; an intersection and a subtraction build
		// at most an intersection, a complement and a union of their own.
		bytes = memsize.Add(bytes, 4*charSetBytes+rangesBytes(ranges)+pointersBytes(k+1)+2*pointersBytes(2))
		switch n.Kind {
		case parser.SetIntersection:
			return intersectBounds(items), bytes, 0
		case parser.SetSubtraction:
			return subtractBounds(items), bytes, 0
		}
		return unionBounds(items), bytes, ranges
	}
	panic(fmt.Sprintf("unknown class set node %T", n))
}

func unionBounds(items []stringsBound) stringsBound {
	var out stringsBound
	for _, s := range items {
		switch {
		case s.kind == noStrings:
			continue
		case out.kind == noStrings:
			out = s
			continue
		case out.kind == oneTable && s.kind == oneTable && out.table == s.table:
			continue
		}
		out = stringsBound{kind: bounded, count: memsize.Add(out.count, s.count), runes: memsize.Add(out.runes, s.runes)}
	}
	return out
}

func intersectBounds(items []stringsBound) stringsBound {
	out := items[0]
	for _, s := range items[1:] {
		switch {
		case out.kind == noStrings || s.kind == noStrings:
			return stringsBound{}
		case out.kind == oneTable && s.kind == oneTable && out.table == s.table:
			continue
		}
		out = stringsBound{kind: bounded, count: min(out.count, s.count), runes: min(out.runes, s.runes)}
	}
	return out
}

func subtractBounds(items []stringsBound) stringsBound {
	out := items[0]
	if out.kind == noStrings {
		return out
	}
	for _, s := range items[1:] {
		if s.kind != noStrings {
			// Some of the first operand's strings may be removed.
			return stringsBound{kind: bounded, count: out.count, runes: out.runes}
		}
	}
	return out
}

// trieSize is the code compileClassSet emits for a set's strings.
type trieSize struct {
	hasStrings   bool  // there are strings of two or more code points
	empty        bool  // the empty string is a member
	instructions int64 // of the trie alone
	splits       int64
	switchBytes  int64 // the rune switches the trie's code owns
}

// boundTrie bounds the trie of at most count strings of at most runes code
// points in all.
func boundTrie(count, runes int64) trieSize {
	// Each node below the root is a code point of some string, each costs at
	// most an instruction, and a node where a string ends with more below it
	// costs a split and a jump. Each node with children costs a rune switch
	// with a key and a target per child; a slice of x bytes takes at most
	// 2x+16.
	const keyTarget = int64(unsafe.Sizeof(rune(0)) + unsafe.Sizeof(int(0)))
	perSwitch := memsize.Round(int64(unsafe.Sizeof(vm.RuneSwitch{}))) + 2*16
	return trieSize{
		hasStrings:   runes > 0,
		empty:        count > 0,
		instructions: memsize.Add(runes, memsize.Mul(2, count)),
		splits:       count,
		switchBytes:  memsize.Add(memsize.Mul(runes+1, perSwitch), memsize.Mul(runes, 2*keyTarget)),
	}
}

// tableOf reports whether n holds exactly the shared members of the property
// it names, as the parser builds it. A caller-built node with other strings
// is bounded instead.
func (b *bounder) tableOf(n *parser.ClassStrings) (propertyKey, bool) {
	if n.Property == "" {
		return propertyKey{}, false
	}
	members, ok := vm.PropertyOfStringsRunes(n.Property)
	if !ok || len(members) != len(n.Strings) || len(members) == 0 || unsafe.SliceData(members) != unsafe.SliceData(n.Strings) {
		return propertyKey{}, false
	}
	return propertyKey{name: n.Property, ignoreCase: b.ignoreCase, backward: b.backward}, true
}

type tableStat struct {
	trie         trieSize
	count, runes int64
	// charsBytes is what the set of the single code points among the
	// members holds, and ranges the number of its ranges.
	charsBytes, ranges int64
}

// tableStatsCache holds a tableStat per property of strings and context:
// at most 4 per property.
var tableStatsCache sync.Map // propertyKey -> *tableStat

// tableStats builds the trie of a property's members once, as setValue
// would, and measures it.
func tableStats(key propertyKey) *tableStat {
	if v, ok := tableStatsCache.Load(key); ok {
		return v.(*tableStat)
	}
	v := propertyValue(key)
	st := &tableStat{count: int64(len(v.strings))}
	for s := range v.strings {
		st.runes += int64(len([]rune(s)))
	}
	st.ranges = int64(len(v.chars.Ranges))
	st.charsBytes = charSetBytes + memsize.RoundNoScan(int64(cap(v.chars.Ranges))*int64(unsafe.Sizeof(vm.RuneRange{})))
	root := &trieNode{}
	for s := range v.strings {
		root.insert([]rune(s))
	}
	st.trie = measureTrie(root)
	actual, _ := tableStatsCache.LoadOrStore(key, st)
	return actual.(*tableStat)
}

// measureTrie returns the size of the code emitTrieChildren emits for root.
func measureTrie(root *trieNode) trieSize {
	t := trieSize{hasStrings: len(root.children) > 0, empty: root.terminal}
	var walk func(n *trieNode)
	walk = func(n *trieNode) {
		// A rune switch, a jump after each child's code but the last, and
		// the children's code: none for a leaf, an alternation of the longer
		// strings and the end of this one (a split and a jump) where a
		// string ends with more below it.
		k := int64(len(n.children))
		t.instructions += k
		t.switchBytes += memsize.Round(int64(unsafe.Sizeof(vm.RuneSwitch{}))) +
			memsize.RoundNoScan(k*int64(unsafe.Sizeof(rune(0)))) +
			memsize.RoundNoScan(k*int64(unsafe.Sizeof(int(0))))
		for _, ch := range n.children {
			if len(ch.children) == 0 {
				continue
			}
			if ch.terminal {
				t.instructions += 2
				t.splits++
			}
			walk(ch)
		}
	}
	if t.hasStrings {
		walk(root)
	}
	return t
}
