package parser_test

import (
	"testing"

	"github.com/mgilbir/goecma262/compiler"
	"github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// The parser no longer uses Lexer, Token or Backreference.Fallback, but they
// stay exported, and working, for existing importers.

func TestDeprecatedLexerStillTokenizes(t *testing.T) {
	l := parser.NewLexer(`a*[b]`, parser.Flags{})
	var types []parser.TokenType
	for {
		tok := l.NextToken()
		types = append(types, tok.Type)
		if tok.Type == parser.TokenEOF || tok.Type == parser.TokenError || len(types) > 20 {
			break
		}
	}
	want := []parser.TokenType{parser.TokenLiteral, parser.TokenStar, parser.TokenLBracket, parser.TokenLiteral, parser.TokenRBracket, parser.TokenEOF}
	if len(types) != len(want) {
		t.Fatalf("tokens %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("tokens %v, want %v", types, want)
		}
	}
}

func TestDeprecatedFallbackCompilesToLiterals(t *testing.T) {
	pat := &parser.Pattern{Body: &parser.Backreference{Index: 1, Fallback: []rune("\x01x")}}
	code, n, err := compiler.Compile(pat)
	if err != nil {
		t.Fatal(err)
	}
	m := &vm.VM{Code: code, NumGroups: n}
	if ok, end, _ := m.Match("\x01x", 0); !ok || end != 2 {
		t.Errorf("Fallback \\x01x: matched %v up to %d, want the two characters", ok, end)
	}
	if ok, _, _ := m.Match("x", 0); ok {
		t.Error("Fallback \\x01x matched \"x\"")
	}
}
