package parser

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mgilbir/goecma262/vm"
)

// MaxNestingDepth is the maximum allowed nesting depth for the parser
const MaxNestingDepth = 200

// Parser is a recursive-descent parser for ECMA-262 patterns (22.2.1, with
// the Annex B.1.2 web-compatibility grammar outside Unicode mode).
//
// It reads the pattern's code points directly rather than through a token
// layer: the same character means different things in different contexts ({
// opens a quantifier or is a literal, - is a range operator only inside a
// class, ] closes a class or is a literal, \k is a backreference only when the
// pattern has named groups), which a context-free tokenizer cannot express.
type Parser struct {
	pattern string
	src     []rune
	pos     int
	flags   Flags

	unicodeMode bool // u or v: Annex B never applies
	annexB      bool

	depth      int
	groupCount int
	// Found by precount before parsing: CountLeftCapturingParensWithin the
	// whole pattern, and whether it declares any named group.
	totalGroups    int
	hasNamedGroups bool

	namedGroups  map[string][]int // name -> every group number carrying it
	pendingNamed []pendingNamed
}

type pendingNamed struct {
	name string
	node *Backreference
}

// syntaxError aborts parsing; Parse recovers it and returns it as the error.
type syntaxError struct{ msg string }

func (e *syntaxError) Error() string { return e.msg }

// New creates a new parser for the given pattern and flags
func New(pattern string, flags Flags) *Parser {
	return &Parser{
		pattern:     pattern,
		flags:       flags,
		unicodeMode: flags.Unicode || flags.UnicodeSets,
		annexB:      flags.AnnexB && !flags.Unicode && !flags.UnicodeSets,
		namedGroups: make(map[string][]int),
	}
}

func (p *Parser) fail(format string, args ...any) {
	panic(&syntaxError{msg: fmt.Sprintf(format, args...)})
}

// Parse parses the pattern and returns the AST
func (p *Parser) Parse() (pat *Pattern, err error) {
	defer func() {
		if r := recover(); r != nil {
			se, ok := r.(*syntaxError)
			if !ok {
				panic(r)
			}
			pat, err = nil, se
		}
	}()

	if !utf8.ValidString(p.pattern) {
		p.fail("invalid UTF-8 in pattern")
	}
	p.src = []rune(p.pattern)
	p.precount()

	body := p.parseDisjunction()
	if !p.atEnd() {
		// parseAlternative stops only at '|' or ')', and parseDisjunction
		// consumes every '|', so what is left is an unbalanced ')'.
		p.fail("unmatched ')'")
	}
	if p.groupCount != p.totalGroups {
		// Numeric escapes were classified against totalGroups; a disagreement
		// would let a backreference index past the captures.
		p.fail("internal error: counted %d capturing groups, parsed %d", p.totalGroups, p.groupCount)
	}
	for _, br := range p.pendingNamed {
		indices, ok := p.namedGroups[br.name]
		if !ok {
			p.fail("invalid named reference: \\k<%s>", br.name)
		}
		br.node.Index = indices[0]
		br.node.Name = br.name
		if len(indices) > 1 {
			br.node.AltIndices = indices[1:]
		}
	}
	if _, err := p.pathNames(body); err != nil {
		return nil, err
	}

	return &Pattern{
		Body:      body,
		NumGroups: p.groupCount,
		Flags:     p.flags,
	}, nil
}

// ------------------------------------------------------------------ scanning

func (p *Parser) atEnd() bool { return p.pos >= len(p.src) }

func (p *Parser) peek() rune { return p.src[p.pos] }

// peekAt returns the code point offset ahead of pos, or -1 past the end.
func (p *Parser) peekAt(offset int) rune {
	if i := p.pos + offset; i < len(p.src) {
		return p.src[i]
	}
	return -1
}

func (p *Parser) eat(c rune) bool {
	if p.pos < len(p.src) && p.src[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

// precount finds CountLeftCapturingParensWithin the whole pattern and whether
// it declares a named group, both needed before parsing: in Annex B an escape
// \N is a backreference only if N does not exceed the number of groups in the
// whole pattern (including groups after it), and \k is a backreference only
// if some group is named. The count is verified against the parse.
func (p *Parser) precount() {
	classDepth := 0
	for i := 0; i < len(p.src); i++ {
		switch p.src[i] {
		case '\\':
			i++ // whatever follows is escaped, never structural
		case '[':
			if p.flags.UnicodeSets {
				classDepth++ // v-mode classes nest
			} else {
				classDepth = 1
			}
		case ']':
			if classDepth > 0 {
				classDepth--
			}
		case '(':
			if classDepth > 0 {
				continue
			}
			if i+1 >= len(p.src) || p.src[i+1] != '?' {
				p.totalGroups++
				continue
			}
			if i+3 < len(p.src) && p.src[i+2] == '<' && p.src[i+3] != '=' && p.src[i+3] != '!' {
				p.totalGroups++
				p.hasNamedGroups = true
			}
		}
	}
}

// ------------------------------------------------------------- disjunction

func (p *Parser) parseDisjunction() Expression {
	first := p.parseAlternative()
	if p.atEnd() || p.peek() != '|' {
		return first
	}
	alts := []Expression{first}
	for p.eat('|') {
		alts = append(alts, p.parseAlternative())
	}
	return &Disjunction{Alternatives: alts}
}

func (p *Parser) parseAlternative() Expression {
	var elements []Expression
	for !p.atEnd() && p.peek() != '|' && p.peek() != ')' {
		elements = append(elements, p.parseTerm())
	}
	switch len(elements) {
	case 0:
		return &Sequence{Elements: []Expression{}}
	case 1:
		return elements[0]
	}
	return &Sequence{Elements: elements}
}

// -------------------------------------------------------------------- term

func (p *Parser) parseTerm() Expression {
	switch p.peek() {
	case '^':
		p.pos++
		p.rejectQuantifier("^")
		return &Anchor{Type: StartOfLine}
	case '$':
		p.pos++
		p.rejectQuantifier("$")
		return &Anchor{Type: EndOfLine}
	case '(':
		return p.parseGroup()
	case '*', '+', '?':
		p.fail("nothing to repeat")
	case '{':
		// A well-formed {n,m} here has nothing to repeat. A malformed one is a
		// literal '{' in Annex B and an error otherwise (see parseAtom).
		if p.bracedQuantifierAhead() {
			p.fail("nothing to repeat")
		}
	case '\\':
		switch p.peekAt(1) {
		case 'b':
			p.pos += 2
			p.rejectQuantifier(`\b`)
			return &Anchor{Type: WordBoundary}
		case 'B':
			p.pos += 2
			p.rejectQuantifier(`\B`)
			return &Anchor{Type: NonWordBoundary}
		}
	}
	return p.applyQuantifier(p.parseAtom())
}

// rejectQuantifier fails if a quantifier follows an assertion that ECMA-262
// does not allow to be quantified.
func (p *Parser) rejectQuantifier(what string) {
	if p.quantifierAhead() {
		p.fail("nothing to repeat: a quantifier cannot follow %s", what)
	}
}

func (p *Parser) quantifierAhead() bool {
	if p.atEnd() {
		return false
	}
	switch p.peek() {
	case '*', '+', '?':
		return true
	case '{':
		return p.bracedQuantifierAhead()
	}
	return false
}

func (p *Parser) bracedQuantifierAhead() bool {
	save := p.pos
	_, ok := p.tryBracedQuantifier()
	p.pos = save
	return ok
}

// bound is a quantifier bound: its value, saturated at maxBound, and its
// digits, so that bounds beyond the saturation point still compare exactly.
type bound struct {
	value  int
	digits string // without leading zeros; "" for zero
}

const maxBound = 1 << 30

func (b bound) less(o bound) bool {
	if len(b.digits) != len(o.digits) {
		return len(b.digits) < len(o.digits)
	}
	return b.digits < o.digits
}

// tryBracedQuantifier parses {n}, {n,} or {n,m}; ok is false (with pos
// unchanged) if the text there is not one. max is nil when unbounded.
func (p *Parser) tryBracedQuantifier() (bounds [2]*bound, ok bool) {
	save := p.pos
	if !p.eat('{') {
		return bounds, false
	}
	min, ok := p.readDecimal()
	if !ok {
		p.pos = save
		return bounds, false
	}
	bounds[0], bounds[1] = &min, &min
	if p.eat(',') {
		bounds[1] = nil
		if max, ok := p.readDecimal(); ok {
			bounds[1] = &max
		}
	}
	if !p.eat('}') {
		p.pos = save
		return bounds, false
	}
	return bounds, true
}

func (p *Parser) readDecimal() (bound, bool) {
	start := p.pos
	var b bound
	for !p.atEnd() && isDigit(p.peek()) {
		if b.value < maxBound {
			b.value = b.value*10 + int(p.peek()-'0')
		}
		p.pos++
	}
	if p.pos == start {
		return b, false
	}
	b.value = min(b.value, maxBound)
	b.digits = strings.TrimLeft(string(p.src[start:p.pos]), "0")
	return b, true
}

// applyQuantifier parses an optional quantifier following atom.
func (p *Parser) applyQuantifier(atom Expression) Expression {
	if p.atEnd() {
		return atom
	}
	q := &Quantifier{Body: atom, Greedy: true}
	switch p.peek() {
	case '*':
		p.pos++
		q.Min, q.Max = 0, -1
	case '+':
		p.pos++
		q.Min, q.Max = 1, -1
	case '?':
		p.pos++
		q.Min, q.Max = 0, 1
	case '{':
		bounds, ok := p.tryBracedQuantifier()
		if !ok {
			// Not a quantifier: Annex B reads the '{' as a literal next.
			if !p.annexB {
				p.fail("incomplete quantifier")
			}
			return atom
		}
		q.Min, q.Max = bounds[0].value, -1
		if bounds[1] != nil {
			// Out-of-order bounds are an early error in every mode, unlike
			// a merely malformed {...}.
			if bounds[1].less(*bounds[0]) {
				p.fail("numbers out of order in {} quantifier")
			}
			q.Max = bounds[1].value
		}
	default:
		return atom
	}
	if p.eat('?') {
		q.Greedy = false
	}
	if p.quantifierAhead() {
		p.fail("nothing to repeat: a quantifier cannot follow a quantifier")
	}
	return q
}

// -------------------------------------------------------------------- atom

func (p *Parser) parseAtom() Expression {
	switch c := p.peek(); c {
	case '.':
		p.pos++
		return &Dot{}
	case '\\':
		return p.parseAtomEscape()
	case '[':
		if p.flags.UnicodeSets {
			return p.parseClassSetExpression()
		}
		return p.parseCharacterClass()
	case ')':
		p.fail("unmatched ')'")
	case ']', '{', '}':
		// Annex B's ExtendedPatternCharacter admits these as literals; they are
		// SyntaxCharacters otherwise.
		if !p.annexB {
			p.fail("lone quantifier bracket %q", c)
		}
		p.pos++
		return &Literal{Char: c}
	}
	c := p.peek()
	p.pos++
	return &Literal{Char: c}
}

// ------------------------------------------------------------------ groups

func (p *Parser) parseGroup() Expression {
	p.depth++
	if p.depth > MaxNestingDepth {
		p.fail("pattern too deeply nested (limit: %d)", MaxNestingDepth)
	}
	defer func() { p.depth-- }()

	p.pos++ // '('
	if !p.eat('?') {
		// The group's number is fixed at its opening paren, before the body
		// (which may contain further, higher-numbered groups).
		p.groupCount++
		idx := p.groupCount
		body := p.parseDisjunction()
		p.expectGroupClose()
		return p.applyQuantifier(&Group{Index: idx, Body: body})
	}
	switch {
	case p.eat(':'):
		body := p.parseDisjunction()
		p.expectGroupClose()
		return p.applyQuantifier(&NonCapturingGroup{Body: body})
	case p.eat('='):
		return p.finishLookaround(false, false)
	case p.eat('!'):
		return p.finishLookaround(false, true)
	case p.eat('<'):
		switch {
		case p.eat('='):
			return p.finishLookaround(true, false)
		case p.eat('!'):
			return p.finishLookaround(true, true)
		}
		return p.parseNamedGroup()
	case p.peekAt(0) == '-' || modifierFlag(p.peekAt(0)) != 0:
		return p.parseModifierGroup()
	}
	p.fail("invalid group")
	return nil
}

// parseModifierGroup parses the rest of (?add-remove:...), pos after the '?'.
// Only i, m and s may be modified; a flag may appear once, and not in both
// lists; and the lists may not both be empty.
func (p *Parser) parseModifierGroup() Expression {
	add := p.readModifiers()
	var remove Modifiers
	if p.eat('-') {
		remove = p.readModifiers()
		if add == 0 && remove == 0 {
			p.fail("invalid group: (?-: modifies no flag")
		}
	}
	if add&remove != 0 {
		p.fail("invalid group: a flag is both added and removed")
	}
	if !p.eat(':') {
		p.fail("invalid group")
	}
	body := p.parseDisjunction()
	p.expectGroupClose()
	return p.applyQuantifier(&ModifierGroup{Add: add, Remove: remove, Body: body})
}

func (p *Parser) readModifiers() Modifiers {
	var seen Modifiers
	for !p.atEnd() {
		m := modifierFlag(p.peek())
		if m == 0 {
			break
		}
		if seen&m != 0 {
			p.fail("invalid group: repeated flag %c", p.peek())
		}
		seen |= m
		p.pos++
	}
	return seen
}

func modifierFlag(r rune) Modifiers {
	switch r {
	case 'i':
		return ModIgnoreCase
	case 'm':
		return ModMultiline
	case 's':
		return ModDotAll
	}
	return 0
}

func (p *Parser) expectGroupClose() {
	if !p.eat(')') {
		p.fail("unterminated group")
	}
}

func (p *Parser) finishLookaround(behind, negated bool) Expression {
	body := p.parseDisjunction()
	p.expectGroupClose()
	var node Expression
	switch {
	case !behind && !negated:
		node = &Lookahead{Body: body}
	case !behind:
		node = &NegativeLookahead{Body: body}
	case !negated:
		node = &Lookbehind{Body: body}
	default:
		node = &NegativeLookbehind{Body: body}
	}
	// Annex B's QuantifiableAssertion makes lookahead, and only lookahead,
	// quantifiable.
	if !behind && p.annexB {
		return p.applyQuantifier(node)
	}
	p.rejectQuantifier("a lookaround")
	return node
}

func (p *Parser) parseNamedGroup() Expression {
	name := p.parseGroupName()
	p.groupCount++
	idx := p.groupCount
	p.namedGroups[name] = append(p.namedGroups[name], idx)
	body := p.parseDisjunction()
	p.expectGroupClose()
	return p.applyQuantifier(&NamedGroup{Index: idx, Name: name, Body: body})
}

// parseGroupName parses a RegExpIdentifierName through its closing '>'. \u
// escapes, including \u{...} and escaped surrogate pairs, are accepted in
// every mode.
func (p *Parser) parseGroupName() string {
	var sb strings.Builder
	for first := true; ; first = false {
		if p.atEnd() {
			p.fail("unterminated group name")
		}
		if p.eat('>') {
			break
		}
		var cp rune
		if p.peek() == '\\' {
			if p.peekAt(1) != 'u' {
				p.fail("invalid escape in group name")
			}
			p.pos += 2
			v, ok := p.readUnicodeEscape(true)
			if !ok {
				p.fail("invalid unicode escape in group name")
			}
			cp = v
		} else {
			cp = p.peek()
			p.pos++
		}
		if first && !isIdentifierStart(cp) || !first && !isIdentifierPart(cp) {
			p.fail("invalid capture group name")
		}
		sb.WriteRune(cp)
	}
	if sb.Len() == 0 {
		p.fail("empty capture group name")
	}
	return sb.String()
}

// isIdentifierStart is IdentifierStartChar: ID_Start, $ or _.
func isIdentifierStart(r rune) bool {
	return r == '$' || r == '_' || isIDStart(r)
}

// isIdentifierPart is IdentifierPartChar: ID_Continue, $, ZWNJ or ZWJ.
func isIdentifierPart(r rune) bool {
	return r == '$' || r == 0x200C || r == 0x200D || isIDContinue(r)
}

// isIDStart is Unicode's derived ID_Start: letters, letter numbers and
// Other_ID_Start, less Pattern_Syntax and Pattern_White_Space.
func isIDStart(r rune) bool {
	if unicode.Is(unicode.Pattern_Syntax, r) || unicode.Is(unicode.Pattern_White_Space, r) {
		return false
	}
	return unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_ID_Start, r)
}

// isIDContinue is Unicode's derived ID_Continue: ID_Start plus nonspacing and
// spacing marks, decimal digits, connector punctuation and Other_ID_Continue,
// less Pattern_Syntax and Pattern_White_Space.
func isIDContinue(r rune) bool {
	if unicode.Is(unicode.Pattern_Syntax, r) || unicode.Is(unicode.Pattern_White_Space, r) {
		return false
	}
	return isIDStart(r) || unicode.In(r, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc, unicode.Other_ID_Continue)
}

// ----------------------------------------------------------------- escapes

// parseAtomEscape parses \ AtomEscape outside a class. pos is on the '\'.
func (p *Parser) parseAtomEscape() Expression {
	if p.pos+1 >= len(p.src) {
		p.fail(`\ at end of pattern`)
	}
	p.pos++ // '\'
	switch c := p.peek(); c {
	case 'd':
		p.pos++
		return &Digit{}
	case 'D':
		p.pos++
		return &NonDigit{}
	case 'w':
		p.pos++
		return &WordChar{}
	case 'W':
		p.pos++
		return &NonWordChar{}
	case 's':
		p.pos++
		return &Whitespace{}
	case 'S':
		p.pos++
		return &NonWhitespace{}
	case 'p', 'P':
		if !p.unicodeMode {
			break // identity escape
		}
		spec, negated := p.readPropertyEscape()
		if strs := p.propertyOfStrings(spec, negated); strs != nil {
			return &ClassSetExpression{Body: strs}
		}
		p.checkCharacterProperty(spec)
		return &UnicodeProperty{Property: spec, Negated: negated}
	case 'k':
		// In Annex B, \k is a backreference only in a pattern with a named
		// group; otherwise it is the identity escape for 'k'.
		if p.annexB && !p.hasNamedGroups {
			break
		}
		p.pos++
		if !p.eat('<') {
			p.fail(`invalid named reference: expected '<' after \k`)
		}
		br := &Backreference{}
		p.pendingNamed = append(p.pendingNamed, pendingNamed{name: p.parseGroupName(), node: br})
		return br
	case '0':
		if !isDigit(p.peekAt(1)) {
			p.pos++
			return &Literal{Char: 0}
		}
		if !p.annexB {
			p.fail("invalid decimal escape")
		}
		return &Literal{Char: p.readLegacyOctal()}
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return p.parseDecimalEscape()
	case 'c':
		return &Literal{Char: p.parseControlEscape(false)}
	}
	return &Literal{Char: p.parseCharacterEscape(false)}
}

// parseDecimalEscape parses \N for a nonzero N; pos is on its first digit.
func (p *Parser) parseDecimalEscape() Expression {
	start := p.pos
	n, _ := p.readDecimal()
	if n.value <= p.totalGroups {
		return &Backreference{Index: n.value}
	}
	if !p.annexB {
		p.fail("invalid escape: backreference to non-existent group \\%s", string(p.src[start:p.pos]))
	}
	// Annex B: with no such group, the DecimalEscape alternative does not
	// apply and the text is a CharacterEscape — a legacy octal escape, or the
	// identity escape of 8 or 9 — followed by ordinary pattern characters, so
	// in \18* the quantifier applies to the 8 alone.
	p.pos = start
	if c := p.peek(); c == '8' || c == '9' {
		p.pos++
		return &Literal{Char: c}
	}
	return &Literal{Char: p.readLegacyOctal()}
}

// readLegacyOctal reads a LegacyOctalEscapeSequence: up to three octal digits
// when the first is 0-3, else up to two, so the value stays <= 0o377.
func (p *Parser) readLegacyOctal() rune {
	maxDigits := 2
	if p.peek() <= '3' {
		maxDigits = 3
	}
	var v rune
	for n := 0; n < maxDigits && !p.atEnd() && p.peek() >= '0' && p.peek() <= '7'; n++ {
		v = v*8 + p.peek() - '0'
		p.pos++
	}
	return v
}

// parseControlEscape parses \cX, pos on the 'c', returning the code point it
// denotes. In an Annex B class the control letter may also be a digit or _.
//
// An invalid control escape in Annex B denotes the backslash alone
// (ExtendedAtom :: \ [lookahead = c], ClassAtomNoDash :: \ [lookahead = c]):
// only the backslash is consumed, and the c is read as the next atom, so in
// a\c* the quantifier applies to the c and in [\c-z] the c opens a range.
func (p *Parser) parseControlEscape(inClass bool) rune {
	letter := p.peekAt(1)
	if isASCIILetter(letter) || p.annexB && inClass && (isDigit(letter) || letter == '_') {
		p.pos += 2
		return letter % 32
	}
	if !p.annexB {
		p.fail("invalid control escape")
	}
	return '\\'
}

// parseCharacterEscape parses a CharacterEscape (or, in a class, a ClassEscape
// denoting one character); pos is on the character after the backslash.
func (p *Parser) parseCharacterEscape(inClass bool) rune {
	c := p.peek()
	switch c {
	case 'f':
		p.pos++
		return '\f'
	case 'n':
		p.pos++
		return '\n'
	case 'r':
		p.pos++
		return '\r'
	case 't':
		p.pos++
		return '\t'
	case 'v':
		p.pos++
		return '\v'
	case 'x':
		p.pos++
		if v, ok := p.readFixedHex(2); ok {
			return v
		}
		if !p.annexB {
			p.fail("invalid hexadecimal escape")
		}
		return 'x' // identity escape
	case 'u':
		p.pos++
		if v, ok := p.readUnicodeEscape(false); ok {
			return v
		}
		if !p.annexB {
			p.fail("invalid unicode escape")
		}
		return 'u' // identity escape
	}

	// IdentityEscape.
	if isSyntaxCharacter(c) || c == '/' {
		p.pos++
		return c
	}
	if p.unicodeMode {
		if c == '-' && inClass { // ClassEscape :: [+UnicodeMode] -
			p.pos++
			return c
		}
		p.fail("invalid escape: \\%c", c)
	}
	if p.annexB {
		// SourceCharacterIdentityEscape: anything but c, and but k in a pattern
		// with named groups (both are handled before reaching here outside a
		// class).
		if c == 'k' && p.hasNamedGroups {
			p.fail(`invalid escape: \k`)
		}
		p.pos++
		return c
	}
	// Strict, non-Unicode: SourceCharacter but not UnicodeIDContinue.
	if isIDContinue(c) {
		p.fail("invalid escape: \\%c", c)
	}
	p.pos++
	return c
}

func (p *Parser) readFixedHex(n int) (rune, bool) {
	if p.pos+n > len(p.src) {
		return 0, false
	}
	var v rune
	for i := 0; i < n; i++ {
		d := hexValue(p.src[p.pos+i])
		if d < 0 {
			return 0, false
		}
		v = v*16 + d
	}
	p.pos += n
	return v, true
}

// readUnicodeEscape reads the body of a \u escape, pos after the 'u': four hex
// digits, or {hex} in Unicode mode. In Unicode mode (and in group names, where
// inName is set) an escaped surrogate pair \uHHHH\uHHHH is one code point.
// ok is false, with pos unchanged, when the text is not a well-formed escape.
func (p *Parser) readUnicodeEscape(inName bool) (rune, bool) {
	save := p.pos
	combine := p.unicodeMode || inName
	if !p.atEnd() && p.peek() == '{' {
		// \u{...} is Unicode-mode syntax; in Annex B the '{' is left for the
		// quantifier or literal path, so \u{2} is "u" twice.
		if !combine {
			return 0, false
		}
		p.pos++
		start := p.pos
		var v rune
		for !p.atEnd() && p.peek() != '}' {
			d := hexValue(p.peek())
			if d < 0 {
				p.pos = save
				return 0, false
			}
			v = v*16 + d
			if v > unicode.MaxRune {
				p.fail("unicode code point escape out of range")
			}
			p.pos++
		}
		if p.atEnd() || p.pos == start {
			p.pos = save
			return 0, false
		}
		p.pos++ // '}'
		return v, true
	}
	first, ok := p.readFixedHex(4)
	if !ok {
		p.pos = save
		return 0, false
	}
	if combine && first >= 0xD800 && first <= 0xDBFF && p.peekAt(0) == '\\' && p.peekAt(1) == 'u' {
		mark := p.pos
		p.pos += 2
		if low, ok := p.readFixedHex(4); ok && low >= 0xDC00 && low <= 0xDFFF {
			return 0x10000 + (first-0xD800)<<10 + (low - 0xDC00), true
		}
		p.pos = mark
	}
	return first, true
}

// readPropertyEscape reads \p{...} or \P{...} in Unicode mode, pos on the 'p'
// or 'P', returning the property expression unvalidated.
func (p *Parser) readPropertyEscape() (spec string, negated bool) {
	negated = p.peek() == 'P'
	p.pos++
	if !p.eat('{') {
		p.fail(`invalid property name: expected '{' after \p`)
	}
	start := p.pos
	for !p.atEnd() && p.peek() != '}' {
		p.pos++
	}
	if p.atEnd() {
		p.fail("invalid property name: unterminated")
	}
	spec = string(p.src[start:p.pos])
	p.pos++ // '}'
	return spec, negated
}

// checkCharacterProperty rejects a property expression that is not a known
// character property. It runs at parse time rather than compile time so that
// a body the compiler never emits (as under {0}) is still checked.
func (p *Parser) checkCharacterProperty(spec string) {
	if !vm.ValidUnicodeProperty(spec) {
		p.fail("invalid property name: %s", spec)
	}
}

// propertyOfStrings returns the members of spec when it names a property of
// strings and the v flag admits one, or nil. A property of strings cannot be
// complemented, so \P{RGI_Emoji} is an error.
func (p *Parser) propertyOfStrings(spec string, negated bool) *ClassStrings {
	if !p.flags.UnicodeSets {
		return nil
	}
	members, ok := vm.PropertyOfStrings(spec)
	if !ok {
		return nil
	}
	if negated {
		p.fail(`invalid property name: \P{%s}: a property of strings cannot be negated`, spec)
	}
	strs := make([][]rune, len(members))
	for i, m := range members {
		strs[i] = []rune(m)
	}
	return &ClassStrings{Strings: strs, Property: spec}
}

// -------------------------------------------------------- character classes

func (p *Parser) parseCharacterClass() Expression {
	p.pos++ // '['
	negated := p.eat('^')
	var atoms []ClassAtom
	for {
		if p.atEnd() {
			p.fail("unterminated character class")
		}
		if p.eat(']') {
			break
		}
		first := p.parseClassAtom()
		// A '-' is a range operator only when an atom follows it.
		if p.peekAt(0) == '-' && p.peekAt(1) != ']' && p.peekAt(1) != -1 {
			p.pos++ // '-'
			second := p.parseClassAtom()
			lo, loOK := first.(*ClassLiteral)
			hi, hiOK := second.(*ClassLiteral)
			if loOK && hiOK {
				if lo.Char > hi.Char {
					p.fail("range out of order in character class")
				}
				atoms = append(atoms, &ClassRange{Start: lo.Char, End: hi.Char})
				continue
			}
			// A class escape at either end, as in [\d-z]: an error in Unicode
			// mode; Annex B reads the '-' as a literal.
			if !p.annexB {
				p.fail("invalid character class range")
			}
			atoms = append(atoms, first, &ClassLiteral{Char: '-'}, second)
			continue
		}
		atoms = append(atoms, first)
	}
	return &CharacterClass{Negated: negated, Atoms: atoms}
}

// parseClassAtom parses one ClassAtom; pos is not on the closing ']'.
func (p *Parser) parseClassAtom() ClassAtom {
	if p.peek() != '\\' {
		c := p.peek()
		p.pos++
		return &ClassLiteral{Char: c}
	}
	if p.pos+1 >= len(p.src) {
		p.fail(`\ at end of pattern`)
	}
	p.pos++ // '\'
	switch c := p.peek(); c {
	case 'd', 'D':
		p.pos++
		return &ClassEscape{Kind: ClassEscapeDigit, Negated: c == 'D'}
	case 'w', 'W':
		p.pos++
		return &ClassEscape{Kind: ClassEscapeWord, Negated: c == 'W'}
	case 's', 'S':
		p.pos++
		return &ClassEscape{Kind: ClassEscapeSpace, Negated: c == 'S'}
	case 'b':
		p.pos++
		return &ClassLiteral{Char: '\b'} // backspace, not a boundary
	case 'p', 'P':
		if !p.unicodeMode {
			break // identity escape
		}
		spec, negated := p.readPropertyEscape()
		p.checkCharacterProperty(spec)
		return &ClassEscape{Kind: ClassEscapeUnicodeProperty, Property: spec, Negated: negated}
	case 'c':
		return &ClassLiteral{Char: p.parseControlEscape(true)}
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		// A class has no backreferences. \0 is NUL when no digit follows; in
		// Annex B the rest are legacy octal escapes, or identity escapes of 8
		// and 9, and any digits after them are the next atoms.
		if c == '0' && !isDigit(p.peekAt(1)) {
			p.pos++
			return &ClassLiteral{Char: 0}
		}
		if !p.annexB {
			p.fail("invalid decimal escape in character class")
		}
		if c == '8' || c == '9' {
			p.pos++
			return &ClassLiteral{Char: c}
		}
		return &ClassLiteral{Char: p.readLegacyOctal()}
	}
	return &ClassLiteral{Char: p.parseCharacterEscape(true)}
}

// ------------------------------------------- character classes (v flag)

// parseClassSetExpression parses a v-mode [...] or [^...], top-level or
// nested, pos on the '['.
//
//	ClassSetExpression :: ClassUnion | ClassIntersection | ClassSubtraction
//	ClassUnion         :: ClassSetRange ClassUnion? | ClassSetOperand ClassUnion?
//	ClassIntersection  :: ClassSetOperand && [lookahead ≠ &] ClassSetOperand
//	                      (&& [lookahead ≠ &] ClassSetOperand)*
//	ClassSubtraction   :: ClassSetOperand -- ClassSetOperand (-- ClassSetOperand)*
//
// Only a union admits ranges, and one level cannot mix operators; nesting
// ([[a-z]&&[aeiou]]) is how they combine.
func (p *Parser) parseClassSetExpression() *ClassSetExpression {
	p.depth++
	if p.depth > MaxNestingDepth {
		p.fail("pattern too deeply nested (limit: %d)", MaxNestingDepth)
	}
	defer func() { p.depth-- }()

	p.pos++ // '['
	negated := p.eat('^')
	body := p.parseClassSetContents()
	if !p.eat(']') {
		p.fail("unterminated character class")
	}
	if negated && mayContainStrings(body) {
		p.fail("negated character class may contain strings")
	}
	return &ClassSetExpression{Negated: negated, Body: body}
}

func (p *Parser) parseClassSetContents() ClassSetNode {
	if p.peekAt(0) == ']' {
		return &ClassSetOperation{Kind: SetUnion}
	}
	first, isRange := p.parseClassSetOperandOrRange()

	for _, op := range []struct {
		c    rune
		kind SetOperationKind
	}{{'&', SetIntersection}, {'-', SetSubtraction}} {
		if !p.doubleAhead(op.c) {
			continue
		}
		if isRange {
			p.fail("invalid set operation in character class: a range cannot be an operand")
		}
		items := []ClassSetNode{first}
		for p.doubleAhead(op.c) {
			p.pos += 2
			if op.c == '&' && p.peekAt(0) == '&' {
				p.fail("invalid character in character class: &&&")
			}
			items = append(items, p.parseClassSetOperand())
		}
		if p.peekAt(0) != ']' {
			p.fail("invalid set operation in character class")
		}
		return &ClassSetOperation{Kind: op.kind, Items: items}
	}

	items := []ClassSetNode{first}
	for !p.atEnd() && p.peek() != ']' {
		if p.doubleAhead('&') || p.doubleAhead('-') {
			p.fail("invalid set operation in character class")
		}
		item, _ := p.parseClassSetOperandOrRange()
		items = append(items, item)
	}
	if len(items) == 1 {
		return first
	}
	return &ClassSetOperation{Kind: SetUnion, Items: items}
}

func (p *Parser) doubleAhead(c rune) bool { return p.peekAt(0) == c && p.peekAt(1) == c }

// parseClassSetOperandOrRange parses a ClassSetOperand, or a ClassSetRange
// when the operand is a character followed by a single '-'.
func (p *Parser) parseClassSetOperandOrRange() (node ClassSetNode, isRange bool) {
	operand := p.parseClassSetOperand()
	lo, ok := operand.(*ClassSetCharacter)
	if !ok || p.peekAt(0) != '-' || p.peekAt(1) == '-' {
		return operand, false
	}
	p.pos++ // '-'
	hi, ok := p.parseClassSetOperand().(*ClassSetCharacter)
	if !ok {
		p.fail("invalid character class range")
	}
	if lo.Char > hi.Char {
		p.fail("range out of order in character class")
	}
	return &ClassSetRange{Start: lo.Char, End: hi.Char}, true
}

// parseClassSetOperand parses a NestedClass, ClassStringDisjunction or
// ClassSetCharacter.
func (p *Parser) parseClassSetOperand() ClassSetNode {
	if p.atEnd() {
		p.fail("unterminated character class")
	}
	switch c := p.peek(); {
	case c == '[':
		return p.parseClassSetExpression()
	case c == '\\':
		return p.parseClassSetEscape()
	}
	return &ClassSetCharacter{Char: p.readClassSetSourceCharacter()}
}

// readClassSetSourceCharacter reads an unescaped ClassSetCharacter: any
// character but a ClassSetSyntaxCharacter, and not the start of a
// ClassSetReservedDoublePunctuator.
func (p *Parser) readClassSetSourceCharacter() rune {
	c := p.peek()
	if isClassSetSyntaxCharacter(c) {
		p.fail("invalid character in character class: %q must be escaped", c)
	}
	if isClassSetReservedDoublePunctuator(c) && p.peekAt(1) == c {
		p.fail("invalid set operation in character class: %c%c is reserved", c, c)
	}
	p.pos++
	return c
}

// parseClassSetEscape parses an escape in a v-mode class, pos on the '\'.
func (p *Parser) parseClassSetEscape() ClassSetNode {
	if p.pos+1 >= len(p.src) {
		p.fail(`\ at end of pattern`)
	}
	p.pos++ // '\'
	switch c := p.peek(); c {
	case 'd', 'D':
		p.pos++
		return &ClassEscape{Kind: ClassEscapeDigit, Negated: c == 'D'}
	case 'w', 'W':
		p.pos++
		return &ClassEscape{Kind: ClassEscapeWord, Negated: c == 'W'}
	case 's', 'S':
		p.pos++
		return &ClassEscape{Kind: ClassEscapeSpace, Negated: c == 'S'}
	case 'p', 'P':
		spec, negated := p.readPropertyEscape()
		if strs := p.propertyOfStrings(spec, negated); strs != nil {
			return strs
		}
		p.checkCharacterProperty(spec)
		return &ClassEscape{Kind: ClassEscapeUnicodeProperty, Property: spec, Negated: negated}
	case 'q':
		return p.parseClassStringDisjunction()
	}
	return &ClassSetCharacter{Char: p.parseClassSetCharacterEscape()}
}

// parseClassSetCharacterEscape parses the escapes that denote one
// ClassSetCharacter — \ CharacterEscape, \ ClassSetReservedPunctuator, or \b
// (backspace) — pos after the '\'.
func (p *Parser) parseClassSetCharacterEscape() rune {
	switch c := p.peek(); {
	case c == 'b':
		p.pos++
		return '\b'
	case c == 'c':
		return p.parseControlEscape(true)
	case c == '0' && !isDigit(p.peekAt(1)):
		p.pos++
		return 0
	case isDigit(c):
		p.fail("invalid decimal escape in character class")
	case isClassSetReservedPunctuator(c):
		p.pos++
		return c
	}
	return p.parseCharacterEscape(true)
}

// parseClassStringDisjunction parses \q{...}, pos on the 'q': literal strings
// separated by '|', any of which may be empty.
func (p *Parser) parseClassStringDisjunction() *ClassStrings {
	p.pos++ // 'q'
	if !p.eat('{') {
		p.fail(`invalid escape: expected '{' after \q`)
	}
	strs := [][]rune{}
	cur := []rune{}
	for {
		if p.atEnd() {
			p.fail(`unterminated \q{...}`)
		}
		switch p.peek() {
		case '}':
			p.pos++
			return &ClassStrings{Strings: append(strs, cur)}
		case '|':
			p.pos++
			strs = append(strs, cur)
			cur = []rune{}
		case '\\':
			if p.pos+1 >= len(p.src) {
				p.fail(`\ at end of pattern`)
			}
			p.pos++
			cur = append(cur, p.parseClassSetCharacterEscape())
		default:
			cur = append(cur, p.readClassSetSourceCharacter())
		}
	}
}

// mayContainStrings is ECMA-262's MayContainStrings, decided from the syntax
// rather than the evaluated set, so [^[\q{ab}--\q{ab}]] is an error although
// the difference is empty: a union may contain strings if any operand may, an
// intersection only if every operand may, and a subtraction if its first
// operand may.
func mayContainStrings(n ClassSetNode) bool {
	switch n := n.(type) {
	case *ClassStrings:
		if n.Property != "" {
			return true
		}
		for _, s := range n.Strings {
			if len(s) != 1 {
				return true
			}
		}
		return false
	case *ClassSetExpression:
		// A negated nested class cannot contain strings; it was rejected
		// when parsed if its contents could.
		return !n.Negated && mayContainStrings(n.Body)
	case *ClassSetOperation:
		switch n.Kind {
		case SetUnion:
			for _, it := range n.Items {
				if mayContainStrings(it) {
					return true
				}
			}
			return false
		case SetIntersection:
			for _, it := range n.Items {
				if !mayContainStrings(it) {
					return false
				}
			}
			return len(n.Items) > 0
		case SetSubtraction:
			return mayContainStrings(n.Items[0])
		}
	}
	return false
}

// isClassSetSyntaxCharacter reports a ClassSetSyntaxCharacter, which must be
// escaped to be a literal in a v-mode class.
func isClassSetSyntaxCharacter(r rune) bool {
	switch r {
	case '(', ')', '[', ']', '{', '}', '/', '-', '\\', '|':
		return true
	}
	return false
}

// isClassSetReservedPunctuator reports a ClassSetReservedPunctuator, which may
// be escaped in a v-mode class.
func isClassSetReservedPunctuator(r rune) bool {
	switch r {
	case '&', '-', '!', '#', '%', ',', ':', ';', '<', '=', '>', '@', '`', '~':
		return true
	}
	return false
}

// isClassSetReservedDoublePunctuator reports whether r doubled is a
// ClassSetReservedDoublePunctuator: reserved for future set operators, so
// [a!!b] is an error while [a!b] is a union containing '!'.
func isClassSetReservedDoublePunctuator(r rune) bool {
	switch r {
	case '&', '!', '#', '$', '%', '*', '+', ',', '.', ':', ';', '<', '=', '>',
		'?', '@', '^', '`', '~':
		return true
	}
	return false
}

// ------------------------------------------------------------- validation

type nameSet map[string]struct{}

// pathNames returns the set of capture-group names reachable along a single
// match path through node, rejecting any name that can appear twice on the same
// path (ES2022 permits duplicate names only across different alternatives of a
// disjunction). It runs in linear time: a Disjunction unions its alternatives'
// name sets without conflict, while a Sequence errors if two elements share a
// name. (Materializing the cartesian product of all alternatives instead is
// exponential — e.g. 30 nested (a|b) groups.)
func (p *Parser) pathNames(node Expression) (nameSet, error) {
	switch n := node.(type) {
	case *Disjunction:
		result := nameSet{}
		for _, alt := range n.Alternatives {
			s, err := p.pathNames(alt)
			if err != nil {
				return nil, err
			}
			// Names in different alternatives may coincide; just union them.
			for k := range s {
				result[k] = struct{}{}
			}
		}
		return result, nil
	case *Sequence:
		result := nameSet{}
		for _, elem := range n.Elements {
			s, err := p.pathNames(elem)
			if err != nil {
				return nil, err
			}
			for k := range s {
				if _, dup := result[k]; dup {
					return nil, fmt.Errorf("duplicate group name: %s", k)
				}
				result[k] = struct{}{}
			}
		}
		return result, nil
	case *Group:
		return p.pathNames(n.Body)
	case *NamedGroup:
		bodyNames, err := p.pathNames(n.Body)
		if err != nil {
			return nil, err
		}
		if _, dup := bodyNames[n.Name]; dup {
			return nil, fmt.Errorf("duplicate group name: %s", n.Name)
		}
		result := nameSet{n.Name: struct{}{}}
		for k := range bodyNames {
			result[k] = struct{}{}
		}
		return result, nil
	case *NonCapturingGroup:
		return p.pathNames(n.Body)
	case *ModifierGroup:
		// Transparent to naming: names inside collide with names outside.
		return p.pathNames(n.Body)
	case *Lookahead:
		return p.pathNames(n.Body)
	case *NegativeLookahead:
		return p.pathNames(n.Body)
	case *Lookbehind:
		return p.pathNames(n.Body)
	case *NegativeLookbehind:
		return p.pathNames(n.Body)
	case *Quantifier:
		return p.pathNames(n.Body)
	default:
		return nameSet{}, nil
	}
}

// -------------------------------------------------------------- characters

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func isASCIILetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }

func hexValue(r rune) rune {
	switch {
	case r >= '0' && r <= '9':
		return r - '0'
	case r >= 'a' && r <= 'f':
		return r - 'a' + 10
	case r >= 'A' && r <= 'F':
		return r - 'A' + 10
	}
	return -1
}

func isSyntaxCharacter(r rune) bool {
	switch r {
	case '^', '$', '\\', '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '|':
		return true
	}
	return false
}
