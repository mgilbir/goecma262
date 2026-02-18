// Package parser defines the AST for ECMA-262 regular expressions
package parser

// Node is the interface implemented by all AST nodes
type Node interface {
	node()
}

// Expression is the base interface for regex expressions
type Expression interface {
	Node
	expr()
}

// Pattern represents a complete regex pattern with flags
type Pattern struct {
	Body  Expression
	Flags Flags
}

func (p *Pattern) node() {}

// Disjunction represents alternatives (a|b|c)
type Disjunction struct {
	Alternatives []Expression
}

func (d *Disjunction) node() {}
func (d *Disjunction) expr() {}

// Sequence represents a sequence of expressions
type Sequence struct {
	Elements []Expression
}

func (s *Sequence) node() {}
func (s *Sequence) expr() {}

// Literal represents a literal character
type Literal struct {
	Char rune
}

func (l *Literal) node() {}
func (l *Literal) expr() {}

// CharacterClass represents [...] or [^...]
type CharacterClass struct {
	Negated bool
	Atoms   []ClassAtom
}

func (c *CharacterClass) node() {}
func (c *CharacterClass) expr() {}

// ClassAtom represents a character class atom
type ClassAtom interface {
	classAtom()
}

// ClassLiteral represents a single literal in a character class
type ClassLiteral struct {
	Char rune
}

func (c *ClassLiteral) classAtom() {}

// ClassRange represents a character range (e.g., a-z)
type ClassRange struct {
	Start rune
	End   rune
}

func (c *ClassRange) classAtom() {}

// ClassEscapeKind represents the type of a class escape
type ClassEscapeKind int

const (
	ClassEscapeDigit ClassEscapeKind = iota
	ClassEscapeWord
	ClassEscapeSpace
	ClassEscapeUnicodeProperty
)

// ClassEscape represents a class escape like \d, \w, \s, \p{...}
type ClassEscape struct {
	Kind     ClassEscapeKind
	Property string
	Negated  bool
}

func (c *ClassEscape) classAtom() {}

// Dot represents the . metacharacter
type Dot struct{}

func (d *Dot) node() {}
func (d *Dot) expr() {}

// Quantifier represents *, +, ?, {n}, {n,}, {n,m}
type Quantifier struct {
	Min    int
	Max    int // -1 means infinity
	Greedy bool
	Body   Expression
}

func (q *Quantifier) node() {}
func (q *Quantifier) expr() {}

// Group represents a capturing group (...)
type Group struct {
	Body Expression
}

func (g *Group) node() {}
func (g *Group) expr() {}

// NamedGroup represents a named capturing group (?<name>...)
type NamedGroup struct {
	Name string
	Body Expression
}

func (n *NamedGroup) node() {}
func (n *NamedGroup) expr() {}

// NonCapturingGroup represents a non-capturing group (?:...)
type NonCapturingGroup struct {
	Body Expression
}

func (n *NonCapturingGroup) node() {}
func (n *NonCapturingGroup) expr() {}

// Lookahead represents a positive lookahead (?=...)
type Lookahead struct {
	Body Expression
}

func (l *Lookahead) node() {}
func (l *Lookahead) expr() {}

// NegativeLookahead represents a negative lookahead (?!...)
type NegativeLookahead struct {
	Body Expression
}

func (n *NegativeLookahead) node() {}
func (n *NegativeLookahead) expr() {}

// Lookbehind represents a positive lookbehind (?<=...)
type Lookbehind struct {
	Body Expression
}

func (l *Lookbehind) node() {}
func (l *Lookbehind) expr() {}

// NegativeLookbehind represents a negative lookbehind (?<!...)
type NegativeLookbehind struct {
	Body Expression
}

func (n *NegativeLookbehind) node() {}
func (n *NegativeLookbehind) expr() {}

// Backreference represents a backreference \n or \k<name>
type Backreference struct {
	Index      int    // primary group index (1-indexed)
	Name       string // empty if using numeric index
	AltIndices []int  // additional group indices for ES2022 duplicate named groups
}

func (b *Backreference) node() {}
func (b *Backreference) expr() {}

// Anchor represents ^ or $
type Anchor struct {
	Type AnchorType
}

// AnchorType represents the type of anchor
type AnchorType int

const (
	StartOfLine AnchorType = iota
	EndOfLine
	WordBoundary
	NonWordBoundary
)

func (a *Anchor) node() {}
func (a *Anchor) expr() {}

// WordChar represents \w (word character)
type WordChar struct{}

func (w *WordChar) node() {}
func (w *WordChar) expr() {}

// NonWordChar represents \W (non-word character)
type NonWordChar struct{}

func (n *NonWordChar) node() {}
func (n *NonWordChar) expr() {}

// Digit represents \d (digit)
type Digit struct{}

func (d *Digit) node() {}
func (d *Digit) expr() {}

// NonDigit represents \D (non-digit)
type NonDigit struct{}

func (n *NonDigit) node() {}
func (n *NonDigit) expr() {}

// Whitespace represents \s (whitespace)
type Whitespace struct{}

func (w *Whitespace) node() {}
func (w *Whitespace) expr() {}

// NonWhitespace represents \S (non-whitespace)
type NonWhitespace struct{}

func (n *NonWhitespace) node() {}
func (n *NonWhitespace) expr() {}

// UnicodeProperty represents \p{...} or \P{...}
type UnicodeProperty struct {
	Property string
	Negated  bool
}

func (u *UnicodeProperty) node() {}
func (u *UnicodeProperty) expr() {}

// Flags holds parser-level flag state
type Flags struct {
	IgnoreCase  bool
	Unicode     bool
	UnicodeSets bool
	DotAll      bool
	Multiline   bool
}
