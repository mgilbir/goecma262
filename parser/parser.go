package parser

import (
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxNestingDepth is the maximum allowed nesting depth for the parser
const MaxNestingDepth = 200

// Parser parses ECMA-262 regular expressions.
//
// It is a recursive-descent parser over the pattern text that follows the
// grammar of ECMA-262 §22.2.1 production by production, with the grammar
// parameters as fields: UnicodeMode (the u or v flag), UnicodeSetsMode (v),
// NamedCaptureGroups, and — outside Unicode mode, when Flags.AnnexB is set —
// the web-compatibility grammar of Annex B.1.2 in place of the standard one.
// Which characters an escape may name depends on where it is (an atom or a
// class) and on those parameters, so escapes are decoded where they are
// parsed rather than by a context-free tokenizer.
type Parser struct {
	src   string
	pos   int
	flags Flags

	unicodeMode bool // [+UnicodeMode]: u or v
	setsMode    bool // [+UnicodeSetsMode]: v
	annexB      bool // the Annex B grammar applies: Flags.AnnexB and not unicodeMode
	named       bool // [+NamedCaptureGroups]

	sawGroupName   bool // the pattern has a GroupSpecifier (for Annex B's reparse)
	groupCount     int
	namedGroups    map[string][]int // name -> all group numbers (ES2022: duplicates allowed across alternatives)
	backreferences []backrefInfo    // to resolve after parsing
	depth          int              // current nesting depth
}

type backrefInfo struct {
	index  int
	name   string
	digits string // raw decimal digits for a numeric \n, for Annex B fallback
	node   *Backreference
}

// New creates a new parser for the given pattern and flags
func New(pattern string, flags Flags) *Parser {
	return &Parser{src: pattern, flags: flags}
}

// Parse parses the pattern and returns the AST, or the first syntax error
// (an ECMA-262 SyntaxError: the pattern does not match the grammar, or an
// early error applies).
func (p *Parser) Parse() (*Pattern, error) {
	p.unicodeMode = p.flags.Unicode || p.flags.UnicodeSets
	p.setsMode = p.flags.UnicodeSets
	p.annexB = p.flags.AnnexB && !p.unicodeMode
	if !p.annexB {
		// ParsePattern (§22.2.3.4): NamedCaptureGroups is always on.
		return p.parseAs(true)
	}
	// Annex B ParsePattern (B.1.2.9): parse with ~NamedCaptureGroups, where
	// \k is an identity escape; if the pattern has a named group, parse it
	// again with +NamedCaptureGroups, where \k must name a group.
	pat, err := p.parseAs(false)
	if err != nil || !p.sawGroupName {
		return pat, err
	}
	return p.parseAs(true)
}

func (p *Parser) parseAs(named bool) (*Pattern, error) {
	p.named = named
	p.pos = 0
	p.sawGroupName = false
	p.groupCount = 0
	p.namedGroups = make(map[string][]int)
	p.backreferences = nil
	p.depth = 0

	body, err := p.parseDisjunction()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.src) {
		// parseDisjunction stops early only at a ')' with no open group.
		return nil, fmt.Errorf("unmatched ')' at offset %d", p.pos)
	}

	if err := p.validateNamedGroupAlternatives(body); err != nil {
		return nil, err
	}

	// Resolve backreferences
	for _, br := range p.backreferences {
		if br.name != "" {
			if indices, ok := p.namedGroups[br.name]; ok && len(indices) > 0 {
				br.node.Index = indices[0] // primary index
				br.node.Name = br.name
				// For ES2022 duplicate names: store all alternative indices
				if len(indices) > 1 {
					br.node.AltIndices = indices[1:]
				}
			} else {
				return nil, fmt.Errorf("unknown named group: %s", br.name)
			}
			continue
		}
		// Numeric backreference to a group that does not exist.
		if br.index > p.groupCount {
			if p.annexB {
				// Web-compat: re-interpret as a legacy octal escape and/or
				// literal digits (e.g. \5 -> U+0005, \8 -> "8", \58 -> U+0005,"8").
				br.node.Fallback = legacyOctalRunes(br.digits)
			} else {
				return nil, fmt.Errorf("backreference to non-existent group: \\%s", br.digits)
			}
		}
	}

	return &Pattern{
		Body:      body,
		NumGroups: p.groupCount,
		Flags:     p.flags,
	}, nil
}

func (p *Parser) enterNesting() error {
	p.depth++
	if p.depth > MaxNestingDepth {
		return fmt.Errorf("pattern too deeply nested (limit: %d)", MaxNestingDepth)
	}
	return nil
}

func (p *Parser) leaveNesting() {
	p.depth--
}

func (p *Parser) eof() bool { return p.pos >= len(p.src) }

// peek returns the byte at pos+off, or 0 past the end.
func (p *Parser) peek(off int) byte {
	if p.pos+off < len(p.src) && p.pos+off >= 0 {
		return p.src[p.pos+off]
	}
	return 0
}

func (p *Parser) at(prefix string) bool { return strings.HasPrefix(p.src[p.pos:], prefix) }

func (p *Parser) eat(c byte) bool {
	if p.peek(0) == c && !p.eof() {
		p.pos++
		return true
	}
	return false
}

// nextRune decodes and consumes the source character at pos.
func (p *Parser) nextRune() (rune, error) {
	r, size := utf8.DecodeRuneInString(p.src[p.pos:])
	if r == utf8.RuneError && size <= 1 {
		return 0, fmt.Errorf("invalid utf-8 sequence at offset %d", p.pos)
	}
	p.pos += size
	return r, nil
}

// parseDisjunction parses alternatives (a|b|c)
func (p *Parser) parseDisjunction() (Expression, error) {
	var alternatives []Expression
	for {
		alt, err := p.parseAlternative()
		if err != nil {
			return nil, err
		}
		alternatives = append(alternatives, alt)
		if !p.eat('|') {
			break
		}
	}
	if len(alternatives) == 1 {
		return alternatives[0], nil
	}
	return &Disjunction{Alternatives: alternatives}, nil
}

// parseAlternative parses a sequence of terms, up to '|', ')' or the end.
func (p *Parser) parseAlternative() (Expression, error) {
	var elements []Expression
	for !p.eof() && p.peek(0) != '|' && p.peek(0) != ')' {
		term, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		elements = append(elements, term)
	}
	if len(elements) == 0 {
		return &Sequence{Elements: []Expression{}}, nil
	}
	if len(elements) == 1 {
		return elements[0], nil
	}
	return &Sequence{Elements: elements}, nil
}

// parseTerm parses an assertion, or an atom and its optional quantifier.
func (p *Parser) parseTerm() (Expression, error) {
	var atom Expression
	switch {
	case p.eat('^'):
		atom = &Anchor{Type: StartOfLine}
	case p.eat('$'):
		atom = &Anchor{Type: EndOfLine}
	case p.at(`\b`):
		p.pos += 2
		atom = &Anchor{Type: WordBoundary}
	case p.at(`\B`):
		p.pos += 2
		atom = &Anchor{Type: NonWordBoundary}
	default:
		var err error
		if atom, err = p.parseAtom(); err != nil {
			return nil, err
		}
	}

	quantifiable := true
	switch atom.(type) {
	case *Anchor, *Lookbehind, *NegativeLookbehind:
		// Assertions take no quantifier.
		quantifiable = false
	case *Lookahead, *NegativeLookahead:
		// Annex B QuantifiableAssertion: only outside Unicode mode.
		quantifiable = p.annexB
	}
	if !quantifiable {
		if p.quantifierAt(p.pos) {
			return nil, fmt.Errorf("nothing to repeat at offset %d: an assertion cannot be quantified", p.pos)
		}
		return atom, nil
	}
	return p.parseQuantifier(atom)
}

// quantifierAt reports whether a well-formed quantifier prefix starts at i.
func (p *Parser) quantifierAt(i int) bool {
	if i >= len(p.src) {
		return false
	}
	switch p.src[i] {
	case '*', '+', '?':
		return true
	case '{':
		_, _, _, _, ok := p.scanBracedQuantifier(i)
		return ok
	}
	return false
}

// scanBracedQuantifier recognises { DecimalDigits } , { DecimalDigits , } or
// { DecimalDigits , DecimalDigits } at i, returning the digit strings (max is
// "" for an open upper bound, and equals min for {n}) and the offset after the
// closing brace.
func (p *Parser) scanBracedQuantifier(i int) (min, max string, open bool, end int, ok bool) {
	s := p.src
	if i >= len(s) || s[i] != '{' {
		return "", "", false, 0, false
	}
	j := i + 1
	k := j
	for k < len(s) && isDigit(s[k]) {
		k++
	}
	if k == j {
		return "", "", false, 0, false
	}
	min = s[j:k]
	if k < len(s) && s[k] == '}' {
		return min, min, false, k + 1, true
	}
	if k >= len(s) || s[k] != ',' {
		return "", "", false, 0, false
	}
	k++
	j = k
	for k < len(s) && isDigit(s[k]) {
		k++
	}
	if k >= len(s) || s[k] != '}' {
		return "", "", false, 0, false
	}
	if k == j {
		return min, "", true, k + 1, true
	}
	return min, s[j:k], false, k + 1, true
}

// parseQuantifier parses an optional quantifier after atom.
func (p *Parser) parseQuantifier(atom Expression) (Expression, error) {
	q := &Quantifier{Greedy: true, Body: atom}
	switch p.peek(0) {
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
		min, max, open, end, ok := p.scanBracedQuantifier(p.pos)
		if !ok {
			if p.annexB {
				// ExtendedPatternCharacter: the { is a literal, read as the next atom.
				return atom, nil
			}
			return nil, fmt.Errorf("incomplete quantifier at offset %d", p.pos)
		}
		// Out-of-order {n,m} (m < n) is an early error in every mode.
		if !open && decimalLess(max, min) {
			return nil, fmt.Errorf("quantifier range out of order: {%s,%s}", min, max)
		}
		p.pos = end
		q.Min = decimalValue(min)
		q.Max = -1
		if !open {
			q.Max = decimalValue(max)
		}
	default:
		return atom, nil
	}
	if p.eat('?') {
		q.Greedy = false
	}
	return q, nil
}

// decimalValue converts DecimalDigits to an int, saturating at MaxInt32 (the
// compiler rejects anything above its repeat limit long before that).
func decimalValue(digits string) int {
	v := 0
	for i := 0; i < len(digits); i++ {
		v = v*10 + int(digits[i]-'0')
		if v > math.MaxInt32 {
			return math.MaxInt32
		}
	}
	return v
}

// decimalLess compares two DecimalDigits strings numerically, exactly (they
// may be longer than any integer type).
func decimalLess(a, b string) bool {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// parseAtom parses an Atom (or, under Annex B, an ExtendedAtom).
func (p *Parser) parseAtom() (Expression, error) {
	switch c := p.peek(0); c {
	case '.':
		p.pos++
		return &Dot{}, nil
	case '(':
		return p.parseGroup()
	case '[':
		return p.parseCharacterClass()
	case '\\':
		return p.parseAtomEscape()
	case '*', '+', '?':
		return nil, fmt.Errorf("nothing to repeat at offset %d", p.pos)
	case '{':
		if p.quantifierAt(p.pos) {
			// Annex B InvalidBracedQuantifier, or a quantifier with no atom.
			return nil, fmt.Errorf("nothing to repeat at offset %d", p.pos)
		}
		if p.annexB {
			p.pos++
			return &Literal{Char: '{'}, nil
		}
		return nil, fmt.Errorf("lone quantifier bracket at offset %d", p.pos)
	case '}', ']':
		if p.annexB {
			// ExtendedPatternCharacter.
			p.pos++
			return &Literal{Char: rune(c)}, nil
		}
		return nil, fmt.Errorf("lone %q at offset %d", c, p.pos)
	default:
		// PatternCharacter. ^ $ ) | are handled by the callers.
		r, err := p.nextRune()
		if err != nil {
			return nil, err
		}
		return &Literal{Char: r}, nil
	}
}

// parseAtomEscape parses \ AtomEscape (at the backslash).
func (p *Parser) parseAtomEscape() (Expression, error) {
	start := p.pos
	p.pos++ // '\'
	if p.eof() {
		return nil, fmt.Errorf(`\ at end of pattern`)
	}
	switch c := p.peek(0); c {
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		// DecimalEscape: a backreference. Whether the group exists is only
		// known once the whole pattern is parsed; see Parse.
		j := p.pos
		for j < len(p.src) && isDigit(p.src[j]) {
			j++
		}
		digits := p.src[p.pos:j]
		p.pos = j
		br := &Backreference{Index: decimalValue(digits)}
		p.backreferences = append(p.backreferences, backrefInfo{index: br.Index, digits: digits, node: br})
		return br, nil
	case 'k':
		if p.named {
			p.pos++
			if p.peek(0) != '<' {
				return nil, fmt.Errorf(`invalid named reference at offset %d: \k must be followed by <name>`, start)
			}
			name, err := p.parseGroupName()
			if err != nil {
				return nil, err
			}
			br := &Backreference{}
			p.backreferences = append(p.backreferences, backrefInfo{name: name, node: br})
			return br, nil
		}
		// Annex B, ~NamedCaptureGroups: an identity escape.
		p.pos++
		return &Literal{Char: 'k'}, nil
	case 'd':
		p.pos++
		return &Digit{}, nil
	case 'D':
		p.pos++
		return &NonDigit{}, nil
	case 'w':
		p.pos++
		return &WordChar{}, nil
	case 'W':
		p.pos++
		return &NonWordChar{}, nil
	case 's':
		p.pos++
		return &Whitespace{}, nil
	case 'S':
		p.pos++
		return &NonWhitespace{}, nil
	case 'p', 'P':
		if p.unicodeMode {
			prop, err := p.parsePropertyExpression()
			if err != nil {
				return nil, err
			}
			return &UnicodeProperty{Property: prop, Negated: c == 'P'}, nil
		}
	}
	r, err := p.parseCharacterEscape(false)
	if err != nil {
		return nil, err
	}
	return &Literal{Char: r}, nil
}

// parseCharacterEscape parses a CharacterEscape (or, in a class, the part of a
// ClassEscape that denotes one character) after the backslash, which has been
// consumed, and returns the character.
//
// Under Annex B, a \ followed by a c that does not begin a control escape is
// itself the character: parseCharacterEscape then returns '\' and leaves the c
// unconsumed, to be read as the next character.
func (p *Parser) parseCharacterEscape(inClass bool) (rune, error) {
	escStart := p.pos - 1
	c := p.peek(0)
	switch c {
	case 'f':
		p.pos++
		return '\f', nil
	case 'n':
		p.pos++
		return '\n', nil
	case 'r':
		p.pos++
		return '\r', nil
	case 't':
		p.pos++
		return '\t', nil
	case 'v':
		p.pos++
		return '\v', nil
	case 'c':
		if l := p.peek(1); isASCIILetter(l) && p.pos+1 < len(p.src) {
			p.pos += 2
			return rune(l % 32), nil
		}
		if p.annexB {
			// ClassEscape :: c ClassControlLetter, in a class only.
			if l := p.peek(1); inClass && p.pos+1 < len(p.src) && (isDigit(l) || l == '_') {
				p.pos += 2
				return rune(l % 32), nil
			}
			// ExtendedAtom / ClassAtomNoDash :: \ [lookahead = c]
			return '\\', nil
		}
		return 0, fmt.Errorf(`invalid control escape at offset %d: \c must be followed by a letter`, escStart)
	case '0':
		if !isDigit(p.peek(1)) || p.pos+1 >= len(p.src) {
			p.pos++
			return 0, nil
		}
		if p.annexB {
			return p.parseLegacyOctal(), nil
		}
		return 0, fmt.Errorf(`invalid decimal escape at offset %d: \0 followed by a digit`, escStart)
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		// Reached only in a class, where there are no backreferences.
		if p.annexB {
			if c <= '7' {
				return p.parseLegacyOctal(), nil
			}
			p.pos++ // \8 and \9 are identity escapes
			return rune(c), nil
		}
		return 0, fmt.Errorf(`invalid decimal escape in class at offset %d`, escStart)
	case 'x':
		if isHexDigit(p.peek(1)) && isHexDigit(p.peek(2)) && p.pos+2 < len(p.src) {
			v := hexValue(p.src[p.pos+1])<<4 | hexValue(p.src[p.pos+2])
			p.pos += 3
			return v, nil
		}
		if p.annexB {
			p.pos++
			return 'x', nil
		}
		return 0, fmt.Errorf(`invalid hex escape at offset %d`, escStart)
	case 'u':
		r, ok, err := p.parseRegExpUnicodeEscape(p.unicodeMode)
		if err != nil {
			return 0, err
		}
		if ok {
			return r, nil
		}
		if p.annexB {
			p.pos++
			return 'u', nil
		}
		return 0, fmt.Errorf(`invalid unicode escape at offset %d`, escStart)
	}

	// IdentityEscape.
	r, err := p.nextRune()
	if err != nil {
		return 0, err
	}
	switch {
	case p.unicodeMode:
		// [+UnicodeMode] SyntaxCharacter or /. (\- in a class is a
		// ClassEscape, handled by the class parser.)
		if r < utf8.RuneSelf && (isSyntaxCharacter(byte(r)) || r == '/') {
			return r, nil
		}
	case p.annexB:
		// SourceCharacterIdentityEscape: anything but c, and k when the
		// pattern has named groups.
		if r != 'k' || !p.named {
			return r, nil
		}
	default:
		// [~UnicodeMode] SourceCharacter but not UnicodeIDContinue.
		if !isIDContinue(r) {
			return r, nil
		}
	}
	return 0, fmt.Errorf(`invalid escape \%c at offset %d`, r, escStart)
}

// parseLegacyOctal parses an Annex B LegacyOctalEscapeSequence at pos (the
// first digit, 0-7): at most three digits when the first is 0-3, otherwise
// at most two, so the value stays below 0o400.
func (p *Parser) parseLegacyOctal() rune {
	maxDigits := 2
	if p.src[p.pos] <= '3' {
		maxDigits = 3
	}
	v := rune(0)
	for n := 0; n < maxDigits && p.pos < len(p.src) && isOctalDigit(p.src[p.pos]); n++ {
		v = v*8 + rune(p.src[p.pos]-'0')
		p.pos++
	}
	return v
}

// parseRegExpUnicodeEscape parses RegExpUnicodeEscapeSequence at pos (the u).
// In Unicode mode that is u{CodePoint} or u Hex4Digits, where a lead surrogate
// followed by \u and a trail surrogate is one code point; otherwise only
// u Hex4Digits. ok is false, with nothing consumed, when the text is not such
// an escape; err reports a malformed u{...} in Unicode mode.
func (p *Parser) parseRegExpUnicodeEscape(unicodeMode bool) (r rune, ok bool, err error) {
	escStart := p.pos - 1
	if unicodeMode && p.peek(1) == '{' {
		j := p.pos + 2
		v := rune(0)
		for j < len(p.src) && isHexDigit(p.src[j]) {
			if v <= unicode.MaxRune {
				v = v<<4 | hexValue(p.src[j])
			}
			j++
		}
		if j == p.pos+2 || j >= len(p.src) || p.src[j] != '}' {
			return 0, false, fmt.Errorf(`invalid unicode code point escape at offset %d`, escStart)
		}
		if v > unicode.MaxRune {
			return 0, false, fmt.Errorf(`unicode code point escape out of range at offset %d`, escStart)
		}
		p.pos = j + 1
		return v, true, nil
	}
	v, ok := p.hex4(p.pos + 1)
	if !ok {
		return 0, false, nil
	}
	p.pos += 5
	if unicodeMode && v >= 0xD800 && v <= 0xDBFF && p.at(`\u`) {
		if lo, ok := p.hex4(p.pos + 2); ok && lo >= 0xDC00 && lo <= 0xDFFF {
			p.pos += 6
			return 0x10000 + (v-0xD800)<<10 + (lo - 0xDC00), true, nil
		}
	}
	return v, true, nil
}

// hex4 decodes four hex digits at i.
func (p *Parser) hex4(i int) (rune, bool) {
	if i+4 > len(p.src) {
		return 0, false
	}
	v := rune(0)
	for k := i; k < i+4; k++ {
		if !isHexDigit(p.src[k]) {
			return 0, false
		}
		v = v<<4 | hexValue(p.src[k])
	}
	return v, true
}

// parsePropertyExpression parses {UnicodePropertyValueExpression} after \p or
// \P (at the p) and returns the expression. Whether it names a property is
// checked by the compiler.
func (p *Parser) parsePropertyExpression() (string, error) {
	escStart := p.pos - 1
	p.pos++ // p or P
	if !p.eat('{') {
		return "", fmt.Errorf(`invalid property escape at offset %d: expected {`, escStart)
	}
	end := strings.IndexByte(p.src[p.pos:], '}')
	if end < 0 {
		return "", fmt.Errorf(`unterminated property escape at offset %d`, escStart)
	}
	expr := p.src[p.pos : p.pos+end]
	// UnicodePropertyName :: [A-Za-z_]+; UnicodePropertyValue :: [A-Za-z0-9_]+.
	name, value, hasValue := strings.Cut(expr, "=")
	valid := func(s string, digits bool) bool {
		if s == "" {
			return false
		}
		for i := 0; i < len(s); i++ {
			if ok := isASCIILetter(s[i]) || s[i] == '_' || digits && isDigit(s[i]); !ok {
				return false
			}
		}
		return true
	}
	if ok := hasValue && valid(name, false) && valid(value, true) || !hasValue && valid(name, true); !ok {
		return "", fmt.Errorf(`invalid property escape at offset %d: \p{%s}`, escStart, expr)
	}
	p.pos += end + 1
	return expr, nil
}

// parseGroup parses a group: (...), (?:...), (?=...), (?!...), (?<=...),
// (?<!...), (?<name>...) or a modifier group (?ims-ims:...), at the opening
// parenthesis.
func (p *Parser) parseGroup() (Expression, error) {
	start := p.pos
	if err := p.enterNesting(); err != nil {
		return nil, err
	}
	defer p.leaveNesting()
	p.pos++ // '('

	// build wraps the parsed body, then consumes the closing parenthesis.
	body := func(build func(Expression) Expression) (Expression, error) {
		b, err := p.parseDisjunction()
		if err != nil {
			return nil, err
		}
		if !p.eat(')') {
			return nil, fmt.Errorf("unterminated group at offset %d", start)
		}
		return build(b), nil
	}

	if !p.eat('?') {
		// Capturing group. The index is fixed at the opening paren, before
		// the body (which may contain further, higher-numbered groups).
		p.groupCount++
		idx := p.groupCount
		return body(func(b Expression) Expression { return &Group{Index: idx, Body: b} })
	}
	switch {
	case p.eat(':'):
		return body(func(b Expression) Expression { return &NonCapturingGroup{Body: b} })
	case p.eat('='):
		return body(func(b Expression) Expression { return &Lookahead{Body: b} })
	case p.eat('!'):
		return body(func(b Expression) Expression { return &NegativeLookahead{Body: b} })
	case p.at("<="):
		p.pos += 2
		return body(func(b Expression) Expression { return &Lookbehind{Body: b} })
	case p.at("<!"):
		p.pos += 2
		return body(func(b Expression) Expression { return &NegativeLookbehind{Body: b} })
	case p.peek(0) == '<':
		name, err := p.parseGroupName()
		if err != nil {
			return nil, err
		}
		p.sawGroupName = true
		p.groupCount++
		idx := p.groupCount
		// ES2022: allow duplicate named groups across alternatives; track all indices
		p.namedGroups[name] = append(p.namedGroups[name], idx)
		return body(func(b Expression) Expression { return &NamedGroup{Index: idx, Name: name, Body: b} })
	}

	// (? RegularExpressionModifiers : Disjunction ) or
	// (? RegularExpressionModifiers - RegularExpressionModifiers : Disjunction )
	add, err := p.parseModifiers(start)
	if err != nil {
		return nil, err
	}
	var remove Modifiers
	dash := p.eat('-')
	if dash {
		if remove, err = p.parseModifiers(start); err != nil {
			return nil, err
		}
	}
	if !p.eat(':') {
		return nil, fmt.Errorf("invalid group at offset %d", start)
	}
	// Early errors: the (?: form was handled above, so add is non-empty
	// unless there is a -, and then the two may not both be empty or share
	// a flag.
	if add == 0 && remove == 0 {
		return nil, fmt.Errorf("invalid modifiers at offset %d: (?-: names no flag", start)
	}
	if add&remove != 0 {
		return nil, fmt.Errorf("invalid modifiers at offset %d: %s both added and removed", start, add&remove)
	}
	return body(func(b Expression) Expression { return &NonCapturingGroup{Add: add, Remove: remove, Body: b} })
}

// parseModifiers parses RegularExpressionModifiers: any of i, m and s, each at
// most once.
func (p *Parser) parseModifiers(start int) (Modifiers, error) {
	var m Modifiers
	for {
		var f Modifiers
		switch p.peek(0) {
		case 'i':
			f = ModIgnoreCase
		case 'm':
			f = ModMultiline
		case 's':
			f = ModDotAll
		default:
			return m, nil
		}
		if m&f != 0 {
			return 0, fmt.Errorf("invalid modifiers at offset %d: %s repeated", start, f)
		}
		m |= f
		p.pos++
	}
}

// parseGroupName parses < RegExpIdentifierName > at the <.
func (p *Parser) parseGroupName() (string, error) {
	start := p.pos
	p.pos++ // '<'
	var sb strings.Builder
	for {
		if p.eof() {
			return "", fmt.Errorf("unterminated group name at offset %d", start)
		}
		if p.eat('>') {
			break
		}
		var r rune
		if p.eat('\\') {
			// \ RegExpUnicodeEscapeSequence[+UnicodeMode], in every mode.
			if p.peek(0) != 'u' {
				return "", fmt.Errorf("invalid escape in group name at offset %d", p.pos-1)
			}
			v, ok, err := p.parseRegExpUnicodeEscape(true)
			if err != nil {
				return "", err
			}
			if !ok {
				return "", fmt.Errorf("invalid escape in group name at offset %d", p.pos-1)
			}
			r = v
		} else {
			v, err := p.nextRune()
			if err != nil {
				return "", err
			}
			r = v
		}
		if sb.Len() == 0 && !isIdentifierStartRune(r) || sb.Len() > 0 && !isIdentifierPartRune(r) {
			return "", fmt.Errorf("invalid character %q in group name at offset %d", r, start)
		}
		sb.WriteRune(r)
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("empty group name at offset %d", start)
	}
	return sb.String(), nil
}

// isIdentifierStartRune reports whether r is an IdentifierStartChar:
// UnicodeIDStart, $ or _.
func isIdentifierStartRune(r rune) bool {
	return r == '$' || r == '_' || isIDStart(r)
}

// isIdentifierPartRune reports whether r is an IdentifierPartChar:
// UnicodeIDContinue, $, ZWNJ or ZWJ.
func isIdentifierPartRune(r rune) bool {
	return r == '$' || r == 0x200C || r == 0x200D || isIDContinue(r)
}

// isIDStart reports whether r has the Unicode ID_Start property.
func isIDStart(r rune) bool {
	return (unicode.IsLetter(r) || unicode.In(r, unicode.Nl, unicode.Other_ID_Start)) &&
		!unicode.In(r, unicode.Pattern_Syntax, unicode.Pattern_White_Space)
}

// isIDContinue reports whether r has the Unicode ID_Continue property.
func isIDContinue(r rune) bool {
	return isIDStart(r) ||
		unicode.In(r, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc, unicode.Other_ID_Continue) &&
			!unicode.In(r, unicode.Pattern_Syntax, unicode.Pattern_White_Space)
}

type nameSet map[string]struct{}

func (p *Parser) validateNamedGroupAlternatives(node Expression) error {
	_, err := p.pathNames(node)
	return err
}

// pathNames returns the set of capture-group names reachable along a single
// match path through node, rejecting any name that can appear twice on the same
// path (ES2022 permits duplicate names only across different alternatives of a
// disjunction). It runs in linear time: a Disjunction unions its alternatives'
// name sets without conflict, while a Sequence errors if two elements share a
// name. (The previous implementation materialized the cartesian product of all
// alternatives, which was exponential — e.g. 30 nested (a|b) groups hung the
// compiler even with no named groups at all.)
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

// parseCharacterClass parses [...] or [^...] at the opening bracket.
func (p *Parser) parseCharacterClass() (Expression, error) {
	start := p.pos
	p.pos++ // '['
	negated := p.eat('^')
	if p.setsMode {
		return p.parseClassSetExpression(start, negated)
	}

	// NonemptyClassRanges: a ClassAtom followed by - and another ClassAtom is
	// a range, unless the - is the last character of the class.
	var atoms []ClassAtom
	for {
		if p.eof() {
			return nil, fmt.Errorf("unterminated character class at offset %d", start)
		}
		if p.eat(']') {
			break
		}
		first, err := p.parseClassAtom()
		if err != nil {
			return nil, err
		}
		if p.peek(0) != '-' || p.pos+1 >= len(p.src) || p.peek(1) == ']' {
			atoms = append(atoms, first)
			continue
		}
		p.pos++ // '-'
		last, err := p.parseClassAtom()
		if err != nil {
			return nil, err
		}
		lo, ok1 := first.(*ClassLiteral)
		hi, ok2 := last.(*ClassLiteral)
		if !ok1 || !ok2 {
			if !p.annexB {
				return nil, fmt.Errorf("invalid character class at offset %d: a class escape cannot be a range endpoint", start)
			}
			// Annex B CharacterRangeOrUnion: the union of both and '-'.
			atoms = append(atoms, first, &ClassLiteral{Char: '-'}, last)
			continue
		}
		// A range whose start code point exceeds its end is a SyntaxError
		// (ECMA-262 CharacterRange), e.g. [z-a].
		if lo.Char > hi.Char {
			return nil, fmt.Errorf("range out of order in character class: %c-%c", lo.Char, hi.Char)
		}
		atoms = append(atoms, &ClassRange{Start: lo.Char, End: hi.Char})
	}
	return &CharacterClass{Negated: negated, Atoms: atoms}, nil
}

// parseClassAtom parses a ClassAtom (not in UnicodeSets mode).
func (p *Parser) parseClassAtom() (ClassAtom, error) {
	if !p.eat('\\') {
		r, err := p.nextRune()
		if err != nil {
			return nil, err
		}
		return &ClassLiteral{Char: r}, nil
	}
	if p.eof() {
		return nil, fmt.Errorf(`\ at end of pattern`)
	}
	switch c := p.peek(0); c {
	case 'b':
		p.pos++
		return &ClassLiteral{Char: '\b'}, nil
	case '-':
		if p.unicodeMode {
			p.pos++
			return &ClassLiteral{Char: '-'}, nil
		}
	case 'd', 'D', 'w', 'W', 's', 'S':
		p.pos++
		return classEscapeAtom(c), nil
	case 'p', 'P':
		if p.unicodeMode {
			prop, err := p.parsePropertyExpression()
			if err != nil {
				return nil, err
			}
			return &ClassEscape{Kind: ClassEscapeUnicodeProperty, Property: prop, Negated: c == 'P'}, nil
		}
	}
	r, err := p.parseCharacterEscape(true)
	if err != nil {
		return nil, err
	}
	return &ClassLiteral{Char: r}, nil
}

func classEscapeAtom(c byte) ClassAtom {
	neg := c == 'D' || c == 'W' || c == 'S'
	switch c {
	case 'd', 'D':
		return &ClassEscape{Kind: ClassEscapeDigit, Negated: neg}
	case 'w', 'W':
		return &ClassEscape{Kind: ClassEscapeWord, Negated: neg}
	default:
		return &ClassEscape{Kind: ClassEscapeSpace, Negated: neg}
	}
}

// parseClassSetExpression parses the contents of a class in UnicodeSets mode
// (ClassSetExpression), after [ or [^.
//
// Only a ClassUnion of characters, ranges and class escapes is supported:
// nested classes, the set operations && and --, and \q{...} string literals
// are rejected as unsupported rather than read with u-mode meaning, which
// differs. The rest of the v-mode class syntax is enforced: the characters
// ( ) [ ] { } / - \ | must be escaped, a doubled punctuator such as !! is
// reserved, and \ may also escape & - ! # % , : ; < = > @ ` ~.
func (p *Parser) parseClassSetExpression(start int, negated bool) (Expression, error) {
	var atoms []ClassAtom
	for {
		if p.eof() {
			return nil, fmt.Errorf("unterminated character class at offset %d", start)
		}
		if p.eat(']') {
			break
		}
		if p.at("&&") || p.at("--") {
			return nil, fmt.Errorf("unsupported v-mode class syntax at offset %d: set operations (&& and --) are not implemented", p.pos)
		}
		first, err := p.parseClassSetOperand()
		if err != nil {
			return nil, err
		}
		if p.at("&&") || p.at("--") {
			return nil, fmt.Errorf("unsupported v-mode class syntax at offset %d: set operations (&& and --) are not implemented", p.pos)
		}
		if p.peek(0) != '-' || p.eof() {
			atoms = append(atoms, first)
			continue
		}
		// ClassSetRange :: ClassSetCharacter - ClassSetCharacter
		p.pos++
		last, err := p.parseClassSetOperand()
		if err != nil {
			return nil, err
		}
		lo, ok1 := first.(*ClassLiteral)
		hi, ok2 := last.(*ClassLiteral)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("invalid character class at offset %d: a class escape cannot be a range endpoint", start)
		}
		if lo.Char > hi.Char {
			return nil, fmt.Errorf("range out of order in character class: %c-%c", lo.Char, hi.Char)
		}
		atoms = append(atoms, &ClassRange{Start: lo.Char, End: hi.Char})
	}
	return &CharacterClass{Negated: negated, Atoms: atoms}, nil
}

// parseClassSetOperand parses a ClassSetCharacter or a CharacterClassEscape
// in UnicodeSets mode.
func (p *Parser) parseClassSetOperand() (ClassAtom, error) {
	c := p.peek(0)
	if c != '\\' {
		if c == '[' {
			return nil, fmt.Errorf("unsupported v-mode class syntax at offset %d: nested classes are not implemented", p.pos)
		}
		if isClassSetSyntaxCharacter(c) {
			return nil, fmt.Errorf("invalid character %q in character class at offset %d: it must be escaped with the v flag", c, p.pos)
		}
		if isClassSetReservedDoublePunctuator(c) && p.peek(1) == c && p.pos+1 < len(p.src) {
			return nil, fmt.Errorf("invalid set operation %q in character class at offset %d", p.src[p.pos:p.pos+2], p.pos)
		}
		r, err := p.nextRune()
		if err != nil {
			return nil, err
		}
		return &ClassLiteral{Char: r}, nil
	}
	p.pos++ // '\'
	if p.eof() {
		return nil, fmt.Errorf(`\ at end of pattern`)
	}
	switch e := p.peek(0); {
	case e == 'b':
		p.pos++
		return &ClassLiteral{Char: '\b'}, nil
	case e == 'q':
		return nil, fmt.Errorf(`unsupported v-mode class syntax at offset %d: \q{...} string literals are not implemented`, p.pos-1)
	case strings.IndexByte("dDwWsS", e) >= 0:
		p.pos++
		return classEscapeAtom(e), nil
	case e == 'p' || e == 'P':
		prop, err := p.parsePropertyExpression()
		if err != nil {
			return nil, err
		}
		return &ClassEscape{Kind: ClassEscapeUnicodeProperty, Property: prop, Negated: e == 'P'}, nil
	case isClassSetReservedPunctuator(e):
		p.pos++
		return &ClassLiteral{Char: rune(e)}, nil
	}
	r, err := p.parseCharacterEscape(true)
	if err != nil {
		return nil, err
	}
	return &ClassLiteral{Char: r}, nil
}

// isClassSetSyntaxCharacter: ( ) [ ] { } / - \ |
func isClassSetSyntaxCharacter(c byte) bool {
	return strings.IndexByte("()[]{}/-\\|", c) >= 0
}

// isClassSetReservedDoublePunctuator reports whether c doubled is a
// ClassSetReservedDoublePunctuator: one of & ! # $ % * + , . : ; < = > ? @ ^
// backquote ~ written twice.
func isClassSetReservedDoublePunctuator(c byte) bool {
	return strings.IndexByte("&!#$%*+,.:;<=>?@^`~", c) >= 0
}

// isClassSetReservedPunctuator: & - ! # % , : ; < = > @ ` ~
func isClassSetReservedPunctuator(c byte) bool {
	return strings.IndexByte("&-!#%,:;<=>@`~", c) >= 0
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// legacyOctalRunes decodes a run of decimal digits from a numeric escape that is
// not a valid backreference, per ECMA-262 Annex B. A leading run of octal digits
// (at most 3 when the first is 0-3, otherwise at most 2, so the value stays
// <= 0o377) becomes one character; every remaining digit is a literal.
func legacyOctalRunes(digits string) []rune {
	if digits == "" {
		return nil
	}
	var out []rune
	i := 0
	if digits[0] >= '0' && digits[0] <= '7' {
		maxOctal := 2
		if digits[0] <= '3' {
			maxOctal = 3
		}
		val := 0
		for i < len(digits) && i < maxOctal && digits[i] >= '0' && digits[i] <= '7' {
			val = val*8 + int(digits[i]-'0')
			i++
		}
		out = append(out, rune(val))
	}
	for ; i < len(digits); i++ {
		out = append(out, rune(digits[i]))
	}
	return out
}
