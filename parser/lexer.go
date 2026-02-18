package parser

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// TokenType represents the type of a token
type TokenType int

const (
	TokenEOF TokenType = iota
	TokenError
	TokenLiteral
	TokenDot
	TokenStar
	TokenPlus
	TokenQuestion
	TokenPipe
	TokenLParen
	TokenRParen
	TokenLBracket
	TokenRBracket
	TokenLBrace
	TokenRBrace
	TokenComma
	TokenCaret
	TokenDollar
	TokenBackslash
	TokenHyphen
	TokenColon
	TokenEquals
	TokenLess
	TokenGreater
	TokenExclaim
	TokenDigit
)

// Token represents a lexical token
type Token struct {
	Type  TokenType
	Value string
	Pos   int
}

// Lexer tokenizes a regex pattern string
type Lexer struct {
	input   string
	pos     int
	readPos int
	ch      byte
	flags   Flags
}

// NewLexer creates a new lexer for the given input
func NewLexer(input string, flags Flags) *Lexer {
	l := &Lexer{input: input, flags: flags}
	l.readChar()
	return l
}

func (l *Lexer) readChar() {
	if l.readPos >= len(l.input) {
		l.ch = 0
	} else {
		l.ch = l.input[l.readPos]
	}
	l.pos = l.readPos
	l.readPos++
}

func (l *Lexer) peekChar() byte {
	if l.readPos >= len(l.input) {
		return 0
	}
	return l.input[l.readPos]
}

// NextToken returns the next token from the input
func (l *Lexer) NextToken() Token {
	var tok Token

	pos := l.pos

	switch l.ch {
	case 0:
		tok = Token{Type: TokenEOF, Pos: pos}
	case '.':
		tok = Token{Type: TokenDot, Value: ".", Pos: pos}
	case '*':
		tok = l.makeQuantifierToken(TokenStar, '*')
	case '+':
		tok = l.makeQuantifierToken(TokenPlus, '+')
	case '?':
		tok = l.makeQuantifierToken(TokenQuestion, '?')
	case '|':
		tok = Token{Type: TokenPipe, Value: "|", Pos: pos}
	case '(':
		tok = Token{Type: TokenLParen, Value: "(", Pos: pos}
	case ')':
		tok = Token{Type: TokenRParen, Value: ")", Pos: pos}
	case '[':
		tok = Token{Type: TokenLBracket, Value: "[", Pos: pos}
	case ']':
		tok = Token{Type: TokenRBracket, Value: "]", Pos: pos}
	case '{':
		tok = Token{Type: TokenLBrace, Value: "{", Pos: pos}
	case '}':
		tok = Token{Type: TokenRBrace, Value: "}", Pos: pos}
	case ',':
		tok = Token{Type: TokenComma, Value: ",", Pos: pos}
	case '^':
		tok = Token{Type: TokenCaret, Value: "^", Pos: pos}
	case '$':
		tok = Token{Type: TokenDollar, Value: "$", Pos: pos}
	case '\\':
		tok = l.readEscape()
	case '-':
		tok = Token{Type: TokenHyphen, Value: "-", Pos: pos}
	case ':':
		tok = Token{Type: TokenColon, Value: ":", Pos: pos}
	case '=':
		tok = Token{Type: TokenEquals, Value: "=", Pos: pos}
	case '<':
		tok = Token{Type: TokenLess, Value: "<", Pos: pos}
	case '>':
		tok = Token{Type: TokenGreater, Value: ">", Pos: pos}
	case '!':
		tok = Token{Type: TokenExclaim, Value: "!", Pos: pos}
	default:
		if isDigit(l.ch) {
			tok = l.readNumber()
		} else if l.ch != 0 {
			if l.ch >= 0x80 {
				r, size := utf8.DecodeRuneInString(l.input[l.pos:])
				if r == utf8.RuneError && size == 1 {
					tok = Token{Type: TokenError, Value: "invalid utf-8 sequence", Pos: pos}
				} else {
					tok = Token{Type: TokenLiteral, Value: string(r), Pos: pos}
					l.readPos = l.pos + size
				}
			} else {
				tok = Token{Type: TokenLiteral, Value: string(l.ch), Pos: pos}
			}
		}
	}

	l.readChar()
	return tok
}

func (l *Lexer) makeQuantifierToken(tt TokenType, ch byte) Token {
	pos := l.pos
	l.readChar()
	if l.ch == '?' {
		return Token{Type: tt, Value: string(ch) + "?", Pos: pos}
	}
	// Don't consume the next char if it's not ?
	l.pos = pos
	l.readPos = pos + 1
	l.ch = ch
	return Token{Type: tt, Value: string(ch), Pos: pos}
}

// isGreedy returns whether the quantifier token is greedy
func (t Token) isGreedy() bool {
	return !strings.HasSuffix(t.Value, "?")
}

func (l *Lexer) readNumber() Token {
	pos := l.pos
	var sb strings.Builder
	for isDigit(l.ch) {
		sb.WriteByte(l.ch)
		l.readChar()
	}
	// Don't consume the non-digit
	l.pos = pos
	l.readPos = pos + sb.Len()
	l.ch = l.input[pos]
	return Token{Type: TokenDigit, Value: sb.String(), Pos: pos}
}

func (l *Lexer) readEscape() Token {
	pos := l.pos
	l.readChar() // consume \

	if l.ch == 0 {
		return Token{Type: TokenError, Value: "unexpected end of pattern", Pos: pos}
	}

	ch := l.ch

	switch ch {
	case 'b':
		return Token{Type: TokenBackslash, Value: "\\b", Pos: pos}
	case 'B':
		return Token{Type: TokenBackslash, Value: "\\B", Pos: pos}
	case 'd', 'D', 'w', 'W', 's', 'S':
		return Token{Type: TokenBackslash, Value: "\\" + string(ch), Pos: pos}
	case 'p', 'P':
		// Unicode property escape
		return l.readUnicodeProperty(ch, pos)
	case 'k':
		// Named backreference
		return l.readNamedBackreference(pos)
	case 'x':
		// Hex escape
		return l.readHexEscape(pos)
	case 'u':
		// Unicode escape
		return l.readUnicodeEscape(pos)
	case 'c':
		// Control character
		return l.readControlEscape(pos)
	case '0':
		// Null character or octal (in non-unicode mode).
		// l.ch == '0'; leave it here so NextToken's trailing readChar advances past it.
		if l.flags.Unicode || l.flags.UnicodeSets {
			return Token{Type: TokenLiteral, Value: "\x00", Pos: pos}
		}
		return l.readOctalOrLiteral(pos)
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		// ECMA-262: always treat \1-\9 as backreferences.
		// Octal only applies to \0 prefix sequences.
		return l.readBackreference(ch, pos)
	case 'f', 'n', 'r', 't', 'v':
		// Standard escapes — l.ch is already at the escape letter; NextToken's
		// trailing readChar() will advance past it.
		return Token{Type: TokenLiteral, Value: "\\" + string(ch), Pos: pos}
	case '^', '$', '\\', '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '|', '/':
		// Escaped special characters
		return Token{Type: TokenLiteral, Value: string(ch), Pos: pos}
	default:
		// Identity escape (in unicode mode, only certain chars can be escaped)
		if l.flags.Unicode || l.flags.UnicodeSets {
			if !isSyntaxCharacter(ch) && ch != '/' {
				return Token{Type: TokenError, Value: fmt.Sprintf("invalid escape sequence: \\%c", ch), Pos: pos}
			}
		}
		return Token{Type: TokenLiteral, Value: string(ch), Pos: pos}
	}
}

func (l *Lexer) readUnicodeProperty(start byte, pos int) Token {
	l.readChar() // consume p or P
	if l.ch != '{' {
		return Token{Type: TokenError, Value: "expected { after \\p or \\P", Pos: pos}
	}
	l.readChar() // consume {

	var sb strings.Builder
	for l.ch != '}' && l.ch != 0 {
		sb.WriteByte(l.ch)
		l.readChar()
	}

	if l.ch != '}' {
		return Token{Type: TokenError, Value: "unterminated unicode property", Pos: pos}
	}

	prop := sb.String()
	negated := start == 'P'

	if negated {
		return Token{Type: TokenBackslash, Value: "\\P{" + prop + "}", Pos: pos}
	}
	return Token{Type: TokenBackslash, Value: "\\p{" + prop + "}", Pos: pos}
}

func (l *Lexer) readNamedBackreference(pos int) Token {
	l.readChar() // consume k
	if l.ch != '<' {
		return Token{Type: TokenError, Value: "expected < after \\k", Pos: pos}
	}
	l.readChar() // consume <

	var sb strings.Builder
	for l.ch != '>' && l.ch != 0 {
		sb.WriteByte(l.ch)
		l.readChar()
	}

	if l.ch != '>' {
		return Token{Type: TokenError, Value: "unterminated named backreference", Pos: pos}
	}

	return Token{Type: TokenBackslash, Value: "\\k<" + sb.String() + ">", Pos: pos}
}

func (l *Lexer) readHexEscape(pos int) Token {
	l.readChar() // consume x; now l.ch = first hex digit

	if l.pos+2 > len(l.input) {
		return Token{Type: TokenError, Value: "invalid hex escape", Pos: pos}
	}

	hex := l.input[l.pos : l.pos+2]
	for i := 0; i < 2; i++ {
		if !isHexDigit(l.input[l.pos+i]) {
			return Token{Type: TokenError, Value: "invalid hex escape", Pos: pos}
		}
	}

	// Advance to the second (last) hex digit so that NextToken's trailing
	// readChar() moves exactly past it.
	l.readChar()

	return Token{Type: TokenLiteral, Value: "\\x" + hex, Pos: pos}
}

func (l *Lexer) readUnicodeEscape(pos int) Token {
	l.readChar() // consume u; now l.ch = first hex digit (or '{')

	if l.ch == '{' {
		// Unicode code point escape: \u{...} requires Unicode mode.
		if !(l.flags.Unicode || l.flags.UnicodeSets) {
			return Token{Type: TokenError, Value: "unicode code point escape requires unicode flag", Pos: pos}
		}
		return l.readUnicodeCodePointEscape(pos)
	}

	// 4-digit hex escape
	if l.pos+4 > len(l.input) {
		return Token{Type: TokenError, Value: "invalid unicode escape", Pos: pos}
	}

	hex := l.input[l.pos : l.pos+4]
	for i := 0; i < 4; i++ {
		if !isHexDigit(l.input[l.pos+i]) {
			return Token{Type: TokenError, Value: "invalid unicode escape", Pos: pos}
		}
	}

	// Advance to the fourth (last) hex digit so that NextToken's trailing
	// readChar() moves exactly past it.
	l.readChar()
	l.readChar()
	l.readChar()

	return Token{Type: TokenLiteral, Value: "\\u" + hex, Pos: pos}
}

func (l *Lexer) readUnicodeCodePointEscape(pos int) Token {
	l.readChar() // consume {

	start := l.pos
	for l.ch != '}' && l.ch != 0 {
		if !isHexDigit(l.ch) {
			return Token{Type: TokenError, Value: "invalid unicode code point escape", Pos: pos}
		}
		l.readChar()
	}

	if l.ch != '}' {
		return Token{Type: TokenError, Value: "unterminated unicode code point escape", Pos: pos}
	}

	hex := l.input[start:l.pos]

	return Token{Type: TokenLiteral, Value: "\\u{" + hex + "}", Pos: pos}
}

func (l *Lexer) readControlEscape(pos int) Token {
	l.readChar() // consume c; now l.ch = control letter

	if l.ch == 0 {
		return Token{Type: TokenError, Value: "invalid control escape", Pos: pos}
	}

	ctrl := l.ch
	// Leave l.ch at ctrl so NextToken's trailing readChar() advances past it.

	// In unicode mode, must be A-Z or a-z
	if l.flags.Unicode || l.flags.UnicodeSets {
		if !((ctrl >= 'a' && ctrl <= 'z') || (ctrl >= 'A' && ctrl <= 'Z')) {
			return Token{Type: TokenError, Value: "invalid control escape", Pos: pos}
		}
	}

	return Token{Type: TokenLiteral, Value: "\\c" + string(ctrl), Pos: pos}
}

func (l *Lexer) readOctalOrLiteral(pos int) Token {
	// l.ch == '0' when entered.
	// Check if followed by more octal digits.
	firstDigitPos := l.pos

	octal := "0"
	l.readChar() // advance past '0'

	for isOctalDigit(l.ch) && len(octal) < 3 {
		octal += string(l.ch)
		l.readChar()
	}

	if len(octal) > 1 {
		// It's an octal escape; l.ch is now one past the last digit.
		// Step back so l.ch = last digit (NextToken's trailing readChar advances past it).
		lastPos := l.pos - 1
		if lastPos < 0 {
			lastPos = 0
		}
		l.pos = lastPos
		l.readPos = lastPos + 1
		l.ch = l.input[lastPos]
		return Token{Type: TokenLiteral, Value: "\\" + octal, Pos: pos}
	}

	// It's just \0 (null character). Step back to '0' so NextToken's trailing
	// readChar() advances past the '0'.
	l.pos = firstDigitPos
	l.readPos = firstDigitPos + 1
	l.ch = '0'
	return Token{Type: TokenLiteral, Value: "\x00", Pos: pos}
}

// readBackreference reads \1-\9 (and multi-digit) as backreferences.
// ECMA-262 requires backreferences to take priority over octal for \1-\9.
func (l *Lexer) readBackreference(start byte, pos int) Token {
	num := string(start)
	l.readChar() // consume first digit

	for isDigit(l.ch) {
		num += string(l.ch)
		l.readChar()
	}
	endPos := l.pos
	if l.ch == 0 {
		endPos = l.pos
	}
	lastPos := endPos - 1
	if lastPos < 0 {
		lastPos = 0
	}
	if lastPos < len(l.input) {
		l.pos = lastPos
		l.readPos = endPos
		l.ch = l.input[l.pos]
	}

	// Always treat as backreference
	return Token{Type: TokenBackslash, Value: "\\" + num, Pos: pos}
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func isHexDigit(ch byte) bool {
	return isDigit(ch) || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func isOctalDigit(ch byte) bool {
	return ch >= '0' && ch <= '7'
}

func isSyntaxCharacter(ch byte) bool {
	switch ch {
	case '^', '$', '\\', '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '|', '/':
		return true
	}
	return false
}

func decodeEscape(s string) (rune, error) {
	if len(s) < 2 || s[0] != '\\' {
		return 0, fmt.Errorf("not an escape sequence: %s", s)
	}

	ch := s[1]
	switch ch {
	case 'f':
		return '\f', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case 'v':
		return '\v', nil
	case 'x':
		if len(s) != 4 {
			return 0, fmt.Errorf("invalid hex escape: %s", s)
		}
		var val rune
		for i := 2; i < 4; i++ {
			val = val*16 + hexValue(s[i])
		}
		return val, nil
	case 'u':
		if s[2] == '{' {
			// \u{...}
			end := strings.IndexByte(s, '}')
			if end == -1 {
				return 0, fmt.Errorf("unterminated unicode escape: %s", s)
			}
			var val rune
			for i := 3; i < end; i++ {
				val = val*16 + hexValue(s[i])
			}
			return val, nil
		}
		// \uXXXX
		if len(s) != 6 {
			return 0, fmt.Errorf("invalid unicode escape: %s", s)
		}
		var val rune
		for i := 2; i < 6; i++ {
			val = val*16 + hexValue(s[i])
		}
		return val, nil
	case 'c':
		if len(s) != 3 {
			return 0, fmt.Errorf("invalid control escape: %s", s)
		}
		ctrl := s[2]
		if ctrl >= 'a' && ctrl <= 'z' {
			return rune(ctrl - 'a' + 1), nil
		}
		if ctrl >= 'A' && ctrl <= 'Z' {
			return rune(ctrl - 'A' + 1), nil
		}
		return 0, fmt.Errorf("invalid control escape: %s", s)
	case '0', '1', '2', '3', '4', '5', '6', '7':
		// Octal escape
		var val rune
		for i := 1; i < len(s) && i < 4; i++ {
			if s[i] < '0' || s[i] > '7' {
				break
			}
			val = val*8 + rune(s[i]-'0')
		}
		return val, nil
	default:
		return rune(ch), nil
	}
}

func hexValue(b byte) rune {
	if b >= '0' && b <= '9' {
		return rune(b - '0')
	}
	if b >= 'a' && b <= 'f' {
		return rune(b - 'a' + 10)
	}
	if b >= 'A' && b <= 'F' {
		return rune(b - 'A' + 10)
	}
	return 0
}
