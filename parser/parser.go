package parser

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxNestingDepth is the maximum allowed nesting depth for the parser
const MaxNestingDepth = 200

// Parser parses ECMA-262 regular expressions
type Parser struct {
	lexer          *Lexer
	curToken       Token
	peekToken      Token
	flags          Flags
	groupCount     int
	namedGroups    map[string][]int // name -> all group numbers (ES2022: duplicates allowed across alternatives)
	backreferences []backrefInfo    // to resolve after parsing
	depth          int              // current nesting depth
}

type backrefInfo struct {
	index int
	name  string
	node  *Backreference
}

// New creates a new parser for the given pattern and flags
func New(pattern string, flags Flags) *Parser {
	l := NewLexer(pattern, flags)
	p := &Parser{
		lexer:       l,
		flags:       flags,
		namedGroups: make(map[string][]int),
	}
	// Read two tokens so curToken and peekToken are set
	p.nextToken()
	p.nextToken()
	return p
}

func (p *Parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.lexer.NextToken()
}

// Parse parses the pattern and returns the AST
func (p *Parser) Parse() (*Pattern, error) {
	body, err := p.parseDisjunction()
	if err != nil {
		return nil, err
	}

	if err := p.validateNamedGroupAlternatives(body); err != nil {
		return nil, err
	}

	if p.curToken.Type != TokenEOF {
		return nil, fmt.Errorf("unexpected token: %s", p.curToken.Value)
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

// parseDisjunction parses alternatives (a|b|c)
func (p *Parser) parseDisjunction() (Expression, error) {
	left, err := p.parseSequence()
	if err != nil {
		return nil, err
	}

	if p.curToken.Type != TokenPipe {
		return left, nil
	}

	alternatives := []Expression{left}

	for p.curToken.Type == TokenPipe {
		p.nextToken()
		alt, err := p.parseSequence()
		if err != nil {
			return nil, err
		}
		alternatives = append(alternatives, alt)
	}

	return &Disjunction{Alternatives: alternatives}, nil
}

// parseSequence parses a sequence of atoms
func (p *Parser) parseSequence() (Expression, error) {
	var elements []Expression

	for p.curToken.Type != TokenEOF &&
		p.curToken.Type != TokenPipe &&
		p.curToken.Type != TokenRParen {
		atom, err := p.parseAtom()
		if err != nil {
			return nil, err
		}

		// Check for quantifier
		if p.curToken.Type == TokenStar ||
			p.curToken.Type == TokenPlus ||
			p.curToken.Type == TokenQuestion {
			atom = p.parseQuantifier(atom)
		} else if p.curToken.Type == TokenLBrace && p.peekToken.Type == TokenDigit {
			quant, err := p.parseBracedQuantifier(atom)
			if err != nil {
				return nil, err
			}
			atom = quant
			// Check for non-greedy modifier after braced quantifier
			if p.curToken.Type == TokenQuestion {
				p.nextToken()
				quant.Greedy = false
			}
		}

		elements = append(elements, atom)
	}

	if len(elements) == 0 {
		return &Sequence{Elements: []Expression{}}, nil
	}
	if len(elements) == 1 {
		return elements[0], nil
	}
	return &Sequence{Elements: elements}, nil
}

// parseAtom parses a single atom
func (p *Parser) parseAtom() (Expression, error) {
	switch p.curToken.Type {
	case TokenEOF:
		return nil, fmt.Errorf("unexpected end of pattern")

	case TokenLiteral:
		val := p.curToken.Value
		p.nextToken()
		// If the value starts with \ and has at least 2 chars, it's an escape
		// sequence that needs decoding (e.g. \uXXXX, \xHH, \n, etc.).
		// A single \ means the lexer already decoded it to a literal backslash.
		if strings.HasPrefix(val, "\\") && len(val) >= 2 {
			return p.parseEscape(val)
		}
		if len(val) == 1 {
			return &Literal{Char: rune(val[0])}, nil
		}
		// Multi-byte rune
		r, _ := utf8.DecodeRuneInString(val)
		return &Literal{Char: r}, nil

	case TokenDot:
		p.nextToken()
		return &Dot{}, nil

	case TokenLParen:
		return p.parseGroup()

	case TokenLBracket:
		return p.parseCharacterClass()

	case TokenCaret:
		p.nextToken()
		return &Anchor{Type: StartOfLine}, nil

	case TokenDollar:
		p.nextToken()
		return &Anchor{Type: EndOfLine}, nil

	case TokenBackslash:
		val := p.curToken.Value
		p.nextToken()
		return p.parseEscape(val)

	case TokenComma:
		// Comma is a literal outside of character classes and quantifiers
		p.nextToken()
		return &Literal{Char: ','}, nil

	case TokenLBrace:
		// '{' is a literal when not part of a valid quantifier
		p.nextToken()
		return &Literal{Char: '{'}, nil

	case TokenRBrace:
		// '}' is a literal when not part of a valid quantifier
		p.nextToken()
		return &Literal{Char: '}'}, nil

	case TokenHyphen:
		// Hyphen is a literal outside of character classes
		p.nextToken()
		return &Literal{Char: '-'}, nil

	case TokenColon:
		p.nextToken()
		return &Literal{Char: ':'}, nil

	case TokenEquals:
		p.nextToken()
		return &Literal{Char: '='}, nil

	case TokenLess:
		p.nextToken()
		return &Literal{Char: '<'}, nil

	case TokenGreater:
		p.nextToken()
		return &Literal{Char: '>'}, nil

	case TokenExclaim:
		p.nextToken()
		return &Literal{Char: '!'}, nil

	case TokenDigit:
		// Digits are literal characters in the pattern body.
		// The lexer may return multiple contiguous digits as one token (e.g. "12"),
		// so we consume only the first character and leave the rest for next call.
		val := p.curToken.Value
		if len(val) == 0 {
			return nil, fmt.Errorf("empty digit token")
		}
		firstChar := rune(val[0])
		if len(val) > 1 {
			// Leave remaining digits as curToken (don't advance the lexer)
			p.curToken = Token{Type: TokenDigit, Value: val[1:], Pos: p.curToken.Pos + 1}
		} else {
			p.nextToken()
		}
		return &Literal{Char: firstChar}, nil

	default:
		return nil, fmt.Errorf("unexpected token: %s", p.curToken.Value)
	}
}

// parseEscape parses an escape sequence
func (p *Parser) parseEscape(val string) (Expression, error) {
	if len(val) < 2 {
		return nil, fmt.Errorf("invalid escape: %s", val)
	}

	ch := val[1]

	switch ch {
	case 'b':
		return &Anchor{Type: WordBoundary}, nil
	case 'B':
		return &Anchor{Type: NonWordBoundary}, nil
	case 'd':
		return &Digit{}, nil
	case 'D':
		return &NonDigit{}, nil
	case 'w':
		return &WordChar{}, nil
	case 'W':
		return &NonWordChar{}, nil
	case 's':
		return &Whitespace{}, nil
	case 'S':
		return &NonWhitespace{}, nil
	case 'p', 'P':
		// Unicode property
		return p.parseUnicodeProperty(val)
	case 'k':
		// Named backreference
		return p.parseNamedBackreference(val)
	default:
		if ch >= '1' && ch <= '9' {
			// Backreference
			return p.parseBackreference(val)
		}
		// \u{...} code point escape: only valid in unicode mode in pattern body.
		if ch == 'u' && len(val) > 3 && val[2] == '{' {
			if !(p.flags.Unicode || p.flags.UnicodeSets) {
				return nil, fmt.Errorf("unicode code point escape requires unicode flag")
			}
		}
		// Character escape
		r, err := decodeEscape(val)
		if err != nil {
			return nil, err
		}
		// In Unicode mode, combine surrogate pairs: \uD800-\uDBFF followed by \uDC00-\uDFFF
		if (p.flags.Unicode || p.flags.UnicodeSets) && r >= 0xD800 && r <= 0xDBFF {
			// r is a high surrogate; check if curToken is a low surrogate \uXXXX
			next := p.curToken
			if next.Type == TokenLiteral && strings.HasPrefix(next.Value, "\\u") &&
				!strings.HasPrefix(next.Value, "\\u{") && len(next.Value) == 6 {
				low, err2 := decodeEscape(next.Value)
				if err2 == nil && low >= 0xDC00 && low <= 0xDFFF {
					// Combine into a single code point
					combined := 0x10000 + (r-0xD800)*0x400 + (low - 0xDC00)
					p.nextToken() // consume the low surrogate token
					return &Literal{Char: combined}, nil
				}
			}
		}
		return &Literal{Char: r}, nil
	}
}

// parseGroup parses a group: (...), (?:...), (?=...), (?!...), (?<=...), (?<!...), (?<name>...)
func (p *Parser) parseGroup() (Expression, error) {
	if p.curToken.Type != TokenLParen {
		return nil, fmt.Errorf("expected (")
	}

	if err := p.enterNesting(); err != nil {
		return nil, err
	}
	defer p.leaveNesting()

	p.nextToken()

	// Check for special group types
	if p.curToken.Type == TokenQuestion {
		p.nextToken()
		return p.parseSpecialGroup()
	}

	// Regular capturing group. The index is fixed at the opening paren, before
	// the body (which may contain further, higher-numbered groups) is parsed.
	p.groupCount++
	idx := p.groupCount

	body, err := p.parseDisjunction()
	if err != nil {
		return nil, err
	}

	if p.curToken.Type != TokenRParen {
		return nil, fmt.Errorf("expected )")
	}
	p.nextToken()

	return &Group{Index: idx, Body: body}, nil
}

// parseSpecialGroup parses special group types after (?
func (p *Parser) parseSpecialGroup() (Expression, error) {
	switch p.curToken.Type {
	case TokenColon:
		// Non-capturing group (?:...)
		p.nextToken()
		body, err := p.parseDisjunction()
		if err != nil {
			return nil, err
		}
		if p.curToken.Type != TokenRParen {
			return nil, fmt.Errorf("expected )")
		}
		p.nextToken()
		return &NonCapturingGroup{Body: body}, nil

	case TokenEquals:
		// Positive lookahead (?=...)
		p.nextToken()
		body, err := p.parseDisjunction()
		if err != nil {
			return nil, err
		}
		if p.curToken.Type != TokenRParen {
			return nil, fmt.Errorf("expected )")
		}
		p.nextToken()
		return &Lookahead{Body: body}, nil

	case TokenExclaim:
		// Negative lookahead (?!...)
		p.nextToken()
		body, err := p.parseDisjunction()
		if err != nil {
			return nil, err
		}
		if p.curToken.Type != TokenRParen {
			return nil, fmt.Errorf("expected )")
		}
		p.nextToken()
		return &NegativeLookahead{Body: body}, nil

	case TokenLess:
		// Could be lookbehind or named group
		p.nextToken()
		return p.parseLookbehindOrNamedGroup()

	default:
		return nil, fmt.Errorf("unknown group type: %s", p.curToken.Value)
	}
}

// parseLookbehindOrNamedGroup parses (?<=...), (?<!...), or (?<name>...)
func (p *Parser) parseLookbehindOrNamedGroup() (Expression, error) {
	if p.curToken.Type == TokenEquals {
		// Positive lookbehind (?<=...)
		p.nextToken()
		body, err := p.parseDisjunction()
		if err != nil {
			return nil, err
		}
		if p.curToken.Type != TokenRParen {
			return nil, fmt.Errorf("expected )")
		}
		p.nextToken()
		return &Lookbehind{Body: body}, nil
	}

	if p.curToken.Type == TokenExclaim {
		// Negative lookbehind (?<!...)
		p.nextToken()
		body, err := p.parseDisjunction()
		if err != nil {
			return nil, err
		}
		if p.curToken.Type != TokenRParen {
			return nil, fmt.Errorf("expected )")
		}
		p.nextToken()
		return &NegativeLookbehind{Body: body}, nil
	}

	// Named group (?<name>...)
	return p.parseNamedGroup()
}

// parseNamedGroup parses (?<name>...)
func (p *Parser) parseNamedGroup() (Expression, error) {
	// Parse the name
	name, err := p.parseGroupName()
	if err != nil {
		return nil, err
	}

	if p.curToken.Type != TokenGreater {
		return nil, fmt.Errorf("expected > after group name")
	}
	p.nextToken() // consume >

	p.groupCount++
	idx := p.groupCount
	// ES2022: allow duplicate named groups across alternatives; track all indices
	p.namedGroups[name] = append(p.namedGroups[name], idx)

	body, err := p.parseDisjunction()
	if err != nil {
		return nil, err
	}

	if p.curToken.Type != TokenRParen {
		return nil, fmt.Errorf("expected )")
	}
	p.nextToken()

	return &NamedGroup{Index: idx, Name: name, Body: body}, nil
}

// parseGroupName parses a group name identifier.
// ECMA-262 allows Unicode identifier names in group names, including:
//   - Direct Unicode characters (including astral-plane chars as UTF-8)
//   - \uXXXX and \u{XXXX} escape sequences
//   - Surrogate pairs \uD800-\uDBFF followed by \uDC00-\uDFFF
//   - $ (TokenDollar) as identifier start
//   - Digits (TokenDigit) as identifier continuation
func (p *Parser) parseGroupName() (string, error) {
	var sb strings.Builder
	unicodeMode := p.flags.Unicode || p.flags.UnicodeSets

	// readGroupNameRune tries to decode one identifier rune from the current token.
	// Returns (rune, decoded, ok) where decoded=true means a rune was consumed.
	readGroupNameRune := func() (rune, bool) {
		tok := p.curToken
		switch tok.Type {
		case TokenDollar:
			p.nextToken()
			return '$', true
		case TokenLiteral:
			// Could be a direct char, a \uXXXX escape, or a \u{...} escape.
			val := tok.Value
			if strings.HasPrefix(val, "\\u") {
				// Decode to rune
				r, err := decodeEscape(val)
				if err != nil {
					return 0, false
				}
				p.nextToken()
				// Handle surrogate pairs: if this is a high surrogate, look ahead
				// for a matching low surrogate (\uDC00-\uDFFF).
				if r >= 0xD800 && r <= 0xDBFF {
					next := p.curToken
					if next.Type == TokenLiteral && strings.HasPrefix(next.Value, "\\u") &&
						!strings.HasPrefix(next.Value, "\\u{") {
						low, err2 := decodeEscape(next.Value)
						if err2 == nil && low >= 0xDC00 && low <= 0xDFFF {
							combined := 0x10000 + (r-0xD800)*0x400 + (low - 0xDC00)
							p.nextToken()
							return combined, true
						}
					}
				}
				return r, true
			}
			// Direct character (possibly multi-byte astral).
			r, size := utf8.DecodeRuneInString(val)
			if r == utf8.RuneError && size == 1 {
				return 0, false
			}
			p.nextToken()
			return r, true
		default:
			return 0, false
		}
	}

	// First character must be identifier start.
	// Special-case: $ is a valid identifier start.
	var firstRune rune
	var ok bool
	switch p.curToken.Type {
	case TokenDollar:
		firstRune = '$'
		p.nextToken()
		ok = true
	case TokenLiteral:
		firstRune, ok = readGroupNameRune()
	default:
		return "", fmt.Errorf("expected group name")
	}
	if !ok {
		return "", fmt.Errorf("invalid group name start")
	}
	if !isIdentifierStartRune(firstRune, unicodeMode) {
		return "", fmt.Errorf("invalid group name start: %c", firstRune)
	}
	sb.WriteRune(firstRune)

	// Rest must be identifier part characters.
	for {
		tok := p.curToken
		switch tok.Type {
		case TokenLiteral:
			r, decoded := readGroupNameRune()
			if !decoded {
				return "", fmt.Errorf("invalid group name continuation")
			}
			if !isIdentifierPartRune(r, unicodeMode) {
				return "", fmt.Errorf("invalid group name continuation: %c", r)
			}
			sb.WriteRune(r)
		case TokenDigit:
			// Digits are valid identifier continuation characters.
			val := tok.Value
			p.nextToken()
			for _, d := range val {
				sb.WriteRune(d)
			}
		case TokenDollar:
			// $ is also valid as a continuation.
			sb.WriteRune('$')
			p.nextToken()
		default:
			return sb.String(), nil
		}
	}
}

func tokenRune(tok Token) (rune, bool) {
	r, size := utf8.DecodeRuneInString(tok.Value)
	if r == utf8.RuneError && size == 1 {
		return 0, false
	}
	if size != len(tok.Value) {
		return 0, false
	}
	return r, true
}

func isIdentifierStartRune(r rune, unicodeMode bool) bool {
	if r == '$' || r == '_' {
		return true
	}
	return unicode.IsLetter(r) || unicode.In(r, unicode.Nl)
}

func isIdentifierPartRune(r rune, unicodeMode bool) bool {
	if isIdentifierStartRune(r, unicodeMode) {
		return true
	}
	// U+200C (ZWNJ) and U+200D (ZWJ) are valid identifier continuation
	// characters per ECMA-262 §12.7.1 (Other_ID_Continue).
	if r == 0x200C || r == 0x200D {
		return true
	}
	return unicode.IsDigit(r) || unicode.In(r, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc)
}

type nameSet map[string]struct{}

func (p *Parser) validateNamedGroupAlternatives(node Expression) error {
	_, err := p.namedGroupSets(node)
	return err
}

func (p *Parser) namedGroupSets(node Expression) ([]nameSet, error) {
	switch n := node.(type) {
	case *Disjunction:
		var all []nameSet
		for _, alt := range n.Alternatives {
			sets, err := p.namedGroupSets(alt)
			if err != nil {
				return nil, err
			}
			all = append(all, sets...)
		}
		return all, nil
	case *Sequence:
		sets := []nameSet{{}}
		for _, elem := range n.Elements {
			nextSets, err := p.namedGroupSets(elem)
			if err != nil {
				return nil, err
			}
			combined := make([]nameSet, 0, len(sets)*len(nextSets))
			for _, left := range sets {
				for _, right := range nextSets {
					merged, err := mergeNameSets(left, right)
					if err != nil {
						return nil, err
					}
					combined = append(combined, merged)
				}
			}
			sets = combined
		}
		return sets, nil
	case *Group:
		return p.namedGroupSets(n.Body)
	case *NamedGroup:
		sets, err := p.namedGroupSets(n.Body)
		if err != nil {
			return nil, err
		}
		for _, set := range sets {
			if _, exists := set[n.Name]; exists {
				return nil, fmt.Errorf("duplicate group name: %s", n.Name)
			}
			set[n.Name] = struct{}{}
		}
		return sets, nil
	case *NonCapturingGroup:
		return p.namedGroupSets(n.Body)
	case *Lookahead:
		return p.namedGroupSets(n.Body)
	case *NegativeLookahead:
		return p.namedGroupSets(n.Body)
	case *Lookbehind:
		return p.namedGroupSets(n.Body)
	case *NegativeLookbehind:
		return p.namedGroupSets(n.Body)
	case *Quantifier:
		return p.namedGroupSets(n.Body)
	default:
		return []nameSet{{}}, nil
	}
}

func mergeNameSets(left, right nameSet) (nameSet, error) {
	merged := make(nameSet, len(left)+len(right))
	for k := range left {
		merged[k] = struct{}{}
	}
	for k := range right {
		if _, exists := merged[k]; exists {
			return nil, fmt.Errorf("duplicate group name: %s", k)
		}
		merged[k] = struct{}{}
	}
	return merged, nil
}

// parseCharacterClass parses [...] or [^...]
func (p *Parser) parseCharacterClass() (Expression, error) {
	if p.curToken.Type != TokenLBracket {
		return nil, fmt.Errorf("expected [")
	}
	p.nextToken()

	negated := false
	if p.curToken.Type == TokenCaret {
		negated = true
		p.nextToken()
	}

	var atoms []ClassAtom

	// Use TokenRBracket (not TokenRBrace) to terminate character classes
	for p.curToken.Type != TokenRBracket && p.curToken.Type != TokenEOF {
		// Multi-digit tokens inside character classes must be split into
		// individual digit literals because the lexer reads contiguous digits
		// as a single TokenDigit (e.g. [1234567] → TokenDigit "1234567").
		// We expand them here so each character becomes its own atom.
		if p.curToken.Type == TokenDigit && len(p.curToken.Value) > 1 {
			digits := []rune(p.curToken.Value)
			// Consume the token and inject individual single-char digit tokens
			// by re-processing each rune. We do this by temporarily replacing
			// the token with single-char versions and advancing manually.
			p.nextToken() // consume the multi-digit token
			for _, d := range digits {
				atoms = append(atoms, &ClassLiteral{Char: d})
			}
			continue
		}
		if p.curToken.Type == TokenHyphen && len(atoms) > 0 {
			// Check if this is a range
			prevLiteral, ok := atoms[len(atoms)-1].(*ClassLiteral)
			p.nextToken()
			if p.curToken.Type == TokenRBracket {
				// Trailing hyphen is literal
				atoms = append(atoms, &ClassLiteral{Char: '-'})
				break
			}

			endAtom, err := p.parseClassAtom()
			if err != nil {
				return nil, err
			}

			endLiteral, okEnd := endAtom.(*ClassLiteral)
			if ok && okEnd {
				// Replace previous literal with range
				atoms[len(atoms)-1] = &ClassRange{Start: prevLiteral.Char, End: endLiteral.Char}
			} else {
				// Treat hyphen as literal and keep both atoms
				atoms = append(atoms, &ClassLiteral{Char: '-'})
				atoms = append(atoms, endAtom)
			}
		} else {
			atom, err := p.parseClassAtom()
			if err != nil {
				return nil, err
			}
			atoms = append(atoms, atom)
		}
	}

	if p.curToken.Type != TokenRBracket {
		return nil, fmt.Errorf("unterminated character class")
	}
	p.nextToken()

	return &CharacterClass{Negated: negated, Atoms: atoms}, nil
}

// parseClassAtom parses a single character or escape within a character class
func (p *Parser) parseClassAtom() (ClassAtom, error) {
	switch p.curToken.Type {
	case TokenLiteral:
		val := p.curToken.Value
		p.nextToken()
		if strings.HasPrefix(val, "\\") {
			r, err := decodeEscape(val)
			if err != nil {
				return nil, err
			}
			return &ClassLiteral{Char: r}, nil
		}
		if len(val) == 1 {
			return &ClassLiteral{Char: rune(val[0])}, nil
		}
		r, _ := utf8.DecodeRuneInString(val)
		return &ClassLiteral{Char: r}, nil

	case TokenBackslash:
		val := p.curToken.Value
		p.nextToken()
		return p.parseClassEscape(val)

	case TokenRBrace:
		// '}' inside a character class is a literal
		p.nextToken()
		return &ClassLiteral{Char: '}'}, nil

	case TokenLBrace:
		// '{' inside a character class is a literal
		p.nextToken()
		return &ClassLiteral{Char: '{'}, nil

	case TokenDot:
		// '.' inside a character class is a literal
		p.nextToken()
		return &ClassLiteral{Char: '.'}, nil

	case TokenStar:
		p.nextToken()
		return &ClassLiteral{Char: '*'}, nil

	case TokenPlus:
		p.nextToken()
		return &ClassLiteral{Char: '+'}, nil

	case TokenQuestion:
		p.nextToken()
		return &ClassLiteral{Char: '?'}, nil

	case TokenCaret:
		p.nextToken()
		return &ClassLiteral{Char: '^'}, nil

	case TokenDollar:
		p.nextToken()
		return &ClassLiteral{Char: '$'}, nil

	case TokenPipe:
		p.nextToken()
		return &ClassLiteral{Char: '|'}, nil

	case TokenLParen:
		p.nextToken()
		return &ClassLiteral{Char: '('}, nil

	case TokenRParen:
		p.nextToken()
		return &ClassLiteral{Char: ')'}, nil

	case TokenComma:
		p.nextToken()
		return &ClassLiteral{Char: ','}, nil

	case TokenDigit:
		val := p.curToken.Value
		p.nextToken()
		if len(val) == 1 {
			return &ClassLiteral{Char: rune(val[0])}, nil
		}
		// Multi-digit number in char class — take first digit as literal
		r, _ := utf8.DecodeRuneInString(val)
		return &ClassLiteral{Char: r}, nil

	case TokenHyphen:
		p.nextToken()
		return &ClassLiteral{Char: '-'}, nil

	case TokenColon:
		p.nextToken()
		return &ClassLiteral{Char: ':'}, nil

	case TokenEquals:
		p.nextToken()
		return &ClassLiteral{Char: '='}, nil

	case TokenLess:
		p.nextToken()
		return &ClassLiteral{Char: '<'}, nil

	case TokenGreater:
		p.nextToken()
		return &ClassLiteral{Char: '>'}, nil

	case TokenExclaim:
		p.nextToken()
		return &ClassLiteral{Char: '!'}, nil

	default:
		return nil, fmt.Errorf("unexpected token in character class: %s (type %d)", p.curToken.Value, p.curToken.Type)
	}
}

// parseClassEscape parses escape sequences within character classes
func (p *Parser) parseClassEscape(val string) (ClassAtom, error) {
	if len(val) < 2 {
		return nil, fmt.Errorf("invalid escape")
	}

	ch := val[1]

	switch ch {
	case 'b':
		// In character class, \b is backspace
		return &ClassLiteral{Char: '\b'}, nil
	case 'd', 'D', 'w', 'W', 's', 'S':
		// Character class escapes
		neg := ch == 'D' || ch == 'W' || ch == 'S'
		switch ch {
		case 'd', 'D':
			return &ClassEscape{Kind: ClassEscapeDigit, Negated: neg}, nil
		case 'w', 'W':
			return &ClassEscape{Kind: ClassEscapeWord, Negated: neg}, nil
		case 's', 'S':
			return &ClassEscape{Kind: ClassEscapeSpace, Negated: neg}, nil
		}
		return nil, fmt.Errorf("invalid class escape: %s", val)
	case 'p', 'P':
		// Unicode property
		start := strings.IndexByte(val, '{')
		end := strings.IndexByte(val, '}')
		if start == -1 || end == -1 {
			return nil, fmt.Errorf("invalid unicode property escape: %s", val)
		}
		prop := val[start+1 : end]
		neg := ch == 'P'
		return &ClassEscape{Kind: ClassEscapeUnicodeProperty, Property: prop, Negated: neg}, nil
	default:
		r, err := decodeEscape(val)
		if err != nil {
			return nil, err
		}
		return &ClassLiteral{Char: r}, nil
	}
}

// parseUnicodeProperty parses \p{...} or \P{...}
func (p *Parser) parseUnicodeProperty(val string) (Expression, error) {
	// Extract property name from \p{name} or \P{name}
	start := strings.IndexByte(val, '{')
	end := strings.IndexByte(val, '}')
	if start == -1 || end == -1 {
		return nil, fmt.Errorf("invalid unicode property escape: %s", val)
	}

	prop := val[start+1 : end]
	negated := val[1] == 'P'

	return &UnicodeProperty{Property: prop, Negated: negated}, nil
}

// parseBackreference parses \n backreferences
func (p *Parser) parseBackreference(val string) (Expression, error) {
	numStr := val[1:]
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return nil, fmt.Errorf("invalid backreference: %s", val)
	}

	br := &Backreference{Index: num}
	p.backreferences = append(p.backreferences, backrefInfo{
		index: num,
		node:  br,
	})

	return br, nil
}

// parseNamedBackreference parses \k<name>
func (p *Parser) parseNamedBackreference(val string) (Expression, error) {
	// Extract name from \k<name>
	start := strings.IndexByte(val, '<')
	end := strings.IndexByte(val, '>')
	if start == -1 || end == -1 {
		return nil, fmt.Errorf("invalid named backreference: %s", val)
	}

	name := val[start+1 : end]
	br := &Backreference{}
	p.backreferences = append(p.backreferences, backrefInfo{
		name: name,
		node: br,
	})

	return br, nil
}

// parseQuantifier parses *, +, ? quantifiers
func (p *Parser) parseQuantifier(body Expression) Expression {
	greedy := p.curToken.isGreedy()

	switch p.curToken.Type {
	case TokenStar:
		p.nextToken()
		return &Quantifier{Min: 0, Max: -1, Greedy: greedy, Body: body}
	case TokenPlus:
		p.nextToken()
		return &Quantifier{Min: 1, Max: -1, Greedy: greedy, Body: body}
	case TokenQuestion:
		p.nextToken()
		return &Quantifier{Min: 0, Max: 1, Greedy: greedy, Body: body}
	}

	return body
}

// parseBracedQuantifier parses {n}, {n,}, {n,m} quantifiers
func (p *Parser) parseBracedQuantifier(body Expression) (*Quantifier, error) {
	if p.curToken.Type != TokenLBrace {
		return nil, fmt.Errorf("expected {")
	}
	p.nextToken()

	if p.curToken.Type != TokenDigit {
		return nil, fmt.Errorf("expected number in quantifier")
	}

	min, err := strconv.Atoi(p.curToken.Value)
	if err != nil {
		return nil, err
	}
	p.nextToken()

	max := min

	if p.curToken.Type == TokenComma {
		p.nextToken()
		if p.curToken.Type == TokenDigit {
			max, err = strconv.Atoi(p.curToken.Value)
			if err != nil {
				return nil, err
			}
			p.nextToken()
		} else {
			max = -1 // unlimited
		}
	}

	if p.curToken.Type != TokenRBrace {
		return nil, fmt.Errorf("expected } in quantifier")
	}

	greedy := p.curToken.isGreedy()
	p.nextToken()

	return &Quantifier{Min: min, Max: max, Greedy: greedy, Body: body}, nil
}
