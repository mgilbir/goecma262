package ecma262_test

import (
	"strings"
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// The pattern grammar differs by mode: with u or v (UnicodeMode), in the
// standard grammar without them (strict), and in the web-compatibility
// grammar of Annex B.1.2 without them (the default). These tables pin, for
// each production that the modes treat differently, whether a pattern is
// accepted in each mode, and in Annex B what it means.
//
// The annexB, u and v columns are V8's verdicts (Node.js 26.9.0,
// new RegExp(pattern, flags)); the strict column follows ECMA-262 §22.2.1
// directly, since no JavaScript engine exposes the grammar without Annex B.
// The Annex B probes record V8's exec result on each input.

type grammarProbe struct {
	input, match string
	matched      bool
}

type grammarCase struct {
	pattern              string
	annexB, strict, u, v bool
	probes               []grammarProbe
}

var escapeGrammarCases = []grammarCase{
	{"\\0", true, true, true, true, []grammarProbe{{"\u0000", "\u0000", true}}},
	{"\\00", true, false, false, false, []grammarProbe{{"\u0000", "\u0000", true}, {"\u00000", "\u0000", true}}},
	{"\\01", true, false, false, false, []grammarProbe{{"\u0001", "\u0001", true}}},
	{"\\08", true, false, false, false, []grammarProbe{{"\u00008", "\u00008", true}, {"\u0000", "", false}}},
	{"\\0a", true, true, true, true, []grammarProbe{{"\u0000a", "\u0000a", true}}},
	{"\\1", true, false, false, false, []grammarProbe{{"\u0001", "\u0001", true}, {"1", "", false}}},
	{"(a)\\1", true, true, true, true, []grammarProbe{{"aa", "aa", true}}},
	{"\\1(a)", true, true, true, true, []grammarProbe{{"a", "a", true}}},
	{"(a)\\2", true, false, false, false, []grammarProbe{{"a\u0002", "a\u0002", true}}},
	{"\\8", true, false, false, false, []grammarProbe{{"8", "8", true}}},
	{"\\9", true, false, false, false, []grammarProbe{{"9", "9", true}}},
	{"\\18", true, false, false, false, []grammarProbe{{"\u00018", "\u00018", true}}},
	{"\\377", true, false, false, false, []grammarProbe{{"\u00ff", "\u00ff", true}}},
	{"\\400", true, false, false, false, []grammarProbe{{" 0", " 0", true}}},
	{"(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)\\10", true, true, true, true, []grammarProbe{{"abcdefghijj", "abcdefghijj", true}}},
	{"(a)\\10", true, false, false, false, []grammarProbe{{"a\u00010", "", false}, {"a\n", "", false}}},
	{"\\cA", true, true, true, true, []grammarProbe{{"\u0001", "\u0001", true}}},
	{"\\cz", true, true, true, true, []grammarProbe{{"\u001a", "\u001a", true}}},
	{"\\c0", true, false, false, false, []grammarProbe{{"\\c0", "\\c0", true}, {"\u0010", "", false}}},
	{"\\c_", true, false, false, false, []grammarProbe{{"\\c_", "\\c_", true}}},
	{"\\c", true, false, false, false, []grammarProbe{{"\\c", "\\c", true}}},
	{"\\c$", true, false, false, false, []grammarProbe{{"\\c", "\\c", true}, {"\\c$", "", false}}},
	{"\\c*", true, false, false, false, []grammarProbe{{"\\", "\\", true}, {"\\cc", "\\cc", true}}},
	{"^\\c", true, false, false, false, []grammarProbe{{"\\c", "\\c", true}}},
	{"\\x41", true, true, true, true, []grammarProbe{{"A", "A", true}}},
	{"\\x4", true, false, false, false, []grammarProbe{{"x4", "x4", true}}},
	{"\\x", true, false, false, false, []grammarProbe{{"x", "x", true}}},
	{"\\xg1", true, false, false, false, []grammarProbe{{"xg1", "xg1", true}}},
	{"\\u0041", true, true, true, true, []grammarProbe{{"A", "A", true}}},
	{"\\u004", true, false, false, false, []grammarProbe{{"u004", "u004", true}}},
	{"\\u", true, false, false, false, []grammarProbe{{"u", "u", true}}},
	{"\\u{41}", true, false, true, true, []grammarProbe{{"uuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuu", "uuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuu", true}, {"A", "", false}}},
	{"\\u{2}", true, false, true, true, []grammarProbe{{"uu", "uu", true}}},
	{"\\u{1,2}", true, false, false, false, []grammarProbe{{"uu", "uu", true}}},
	{"\\u{}", true, false, false, false, []grammarProbe{{"u{}", "u{}", true}}},
	{"\\k", true, false, false, false, []grammarProbe{{"k", "k", true}}},
	{"\\k<a>", true, false, false, false, []grammarProbe{{"k<a>", "k<a>", true}}},
	{"(?<a>x)\\k<a>", true, true, true, true, []grammarProbe{{"xx", "xx", true}}},
	{"\\k<a>(?<a>x)", true, true, true, true, []grammarProbe{{"x", "x", true}}},
	{"(?<a>x)\\k<b>", false, false, false, false, []grammarProbe{}},
	{"(?<a>x)\\k", false, false, false, false, []grammarProbe{}},
	{"(?<a>x)|\\k<a>", true, true, true, true, []grammarProbe{{"x", "x", true}}},
	{"\\p{L}", true, false, true, true, []grammarProbe{{"p{L}", "p{L}", true}}},
	{"\\P{L}", true, false, true, true, []grammarProbe{{"P{L}", "P{L}", true}}},
	{"\\p", true, false, false, false, []grammarProbe{{"p", "p", true}}},
	{"\\pL", true, false, false, false, []grammarProbe{{"pL", "pL", true}}},
	{"\\p{}", true, false, false, false, []grammarProbe{{"p{}", "p{}", true}}},
	{"\\p{Lu}", true, false, true, true, []grammarProbe{{"p{Lu}", "p{Lu}", true}}},
	{"\\p{L", true, false, false, false, []grammarProbe{{"p{L", "p{L", true}}},
	{"\\p{ L}", true, false, false, false, []grammarProbe{}},
	{"\\-", true, true, false, false, []grammarProbe{{"-", "-", true}}},
	{"\\/", true, true, true, true, []grammarProbe{{"/", "/", true}}},
	{"\\$", true, true, true, true, []grammarProbe{{"$", "$", true}}},
	{"\\ ", true, true, false, false, []grammarProbe{{" ", " ", true}}},
	{"\\a", true, false, false, false, []grammarProbe{{"a", "a", true}}},
	{"\\_", true, false, false, false, []grammarProbe{{"_", "_", true}}},
	{"\\\u00e9", true, false, false, false, []grammarProbe{{"\u00e9", "\u00e9", true}}},
	{"\\M", true, false, false, false, []grammarProbe{{"M", "M", true}}},
	{"a\\-b", true, true, false, false, []grammarProbe{{"a-b", "a-b", true}}},
	{"(\\-)", true, true, false, false, []grammarProbe{{"-", "-", true}}},
	{"\\b", true, true, true, true, []grammarProbe{}},
	{"\\B", true, true, true, true, []grammarProbe{}},
	{"\\d\\D\\s\\S\\w\\W", true, true, true, true, []grammarProbe{}},
	{"[\\b]", true, true, true, true, []grammarProbe{{"\b", "\b", true}}},
	{"[\\B]", true, false, false, false, []grammarProbe{{"B", "B", true}}},
	{"[\\-]", true, true, true, true, []grammarProbe{{"-", "-", true}}},
	{"[a\\-z]", true, true, true, true, []grammarProbe{{"-", "-", true}, {"b", "", false}}},
	{"[\\0]", true, true, true, true, []grammarProbe{{"\u0000", "\u0000", true}}},
	{"[\\00]", true, false, false, false, []grammarProbe{{"\u0000", "\u0000", true}}},
	{"[\\01]", true, false, false, false, []grammarProbe{{"\u0001", "\u0001", true}}},
	{"[\\1]", true, false, false, false, []grammarProbe{{"\u0001", "\u0001", true}}},
	{"[\\12]", true, false, false, false, []grammarProbe{{"\n", "\n", true}}},
	{"[\\8]", true, false, false, false, []grammarProbe{{"8", "8", true}}},
	{"[\\c]", true, false, false, false, []grammarProbe{{"\\", "\\", true}, {"c", "c", true}}},
	{"[\\c0]", true, false, false, false, []grammarProbe{{"\u0010", "\u0010", true}}},
	{"[\\c_]", true, false, false, false, []grammarProbe{{"\u001f", "\u001f", true}}},
	{"[\\c*]", true, false, false, false, []grammarProbe{{"\\", "\\", true}, {"c", "c", true}, {"*", "*", true}}},
	{"[\\cA]", true, true, true, true, []grammarProbe{{"\u0001", "\u0001", true}}},
	{"[\\k]", true, false, false, false, []grammarProbe{{"k", "k", true}}},
	{"(?<a>x)[\\k]", false, false, false, false, []grammarProbe{}},
	{"(?<a>x)[\\k<a>]", false, false, false, false, []grammarProbe{}},
	{"[\\x]", true, false, false, false, []grammarProbe{{"x", "x", true}}},
	{"[\\u]", true, false, false, false, []grammarProbe{{"u", "u", true}}},
	{"[\\u{41}]", true, false, true, true, []grammarProbe{{"u", "u", true}, {"{", "{", true}}},
	{"[\\p{L}]", true, false, true, true, []grammarProbe{{"p", "p", true}, {"L", "L", true}}},
	{"[\\/]", true, true, true, true, []grammarProbe{{"/", "/", true}}},
	{"[\\a]", true, false, false, false, []grammarProbe{{"a", "a", true}}},
	{"[\\]]", true, true, true, true, []grammarProbe{{"]", "]", true}}},
	{"[\\\\]", true, true, true, true, []grammarProbe{{"\\", "\\", true}}},
	{"[a-z]", true, true, true, true, []grammarProbe{{"m", "m", true}}},
	{"[z-a]", false, false, false, false, []grammarProbe{}},
	{"[\\d-z]", true, false, false, false, []grammarProbe{{"-", "-", true}, {"5", "5", true}, {"z", "z", true}, {"m", "", false}}},
	{"[a-\\d]", true, false, false, false, []grammarProbe{{"a", "a", true}, {"-", "-", true}, {"5", "5", true}, {"b", "", false}}},
	{"[\\d-\\w]", true, false, false, false, []grammarProbe{{"-", "-", true}, {"x", "x", true}}},
	{"[\\w-]", true, true, true, false, []grammarProbe{{"-", "-", true}}},
	{"[-\\w]", true, true, true, false, []grammarProbe{{"-", "-", true}}},
	{"[a-]", true, true, true, false, []grammarProbe{{"-", "-", true}}},
	{"[--0]", true, true, true, false, []grammarProbe{{"/", "/", true}}},
	{"[\\c0-\\c9]", true, false, false, false, []grammarProbe{{"\u0012", "\u0012", true}}},
	{"[a-b-c]", true, true, true, false, []grammarProbe{{"-", "-", true}, {"c", "c", true}}},
	{"[\\s-\\d]", true, false, false, false, []grammarProbe{{"-", "-", true}}},
	{"{", true, false, false, false, []grammarProbe{{"{", "{", true}}},
	{"}", true, false, false, false, []grammarProbe{{"}", "}", true}}},
	{"]", true, false, false, false, []grammarProbe{{"]", "]", true}}},
	{"a{", true, false, false, false, []grammarProbe{{"a{", "a{", true}}},
	{"a{1", true, false, false, false, []grammarProbe{{"a{1", "a{1", true}}},
	{"a{1,", true, false, false, false, []grammarProbe{{"a{1,", "a{1,", true}}},
	{"a{,1}", true, false, false, false, []grammarProbe{{"a{,1}", "a{,1}", true}}},
	{"a{ 1}", true, false, false, false, []grammarProbe{{"a{ 1}", "a{ 1}", true}}},
	{"a{}", true, false, false, false, []grammarProbe{{"a{}", "a{}", true}}},
	{"x]", true, false, false, false, []grammarProbe{{"x]", "x]", true}}},
	{"[]]", true, false, false, false, []grammarProbe{{"]", "", false}}},
	{"[[a]]", true, false, false, true, []grammarProbe{{"[]", "[]", true}, {"a]", "a]", true}}},
	{"[[]", true, true, true, false, []grammarProbe{{"[", "[", true}}},
	{"{1}", false, false, false, false, []grammarProbe{}},
	{"{1,}", false, false, false, false, []grammarProbe{}},
	{"a|{1,2}", false, false, false, false, []grammarProbe{}},
	{"a{1}{2}", false, false, false, false, []grammarProbe{}},
	{"a{2,1}", false, false, false, false, []grammarProbe{}},
	{"a{1,2}", true, true, true, true, []grammarProbe{{"aa", "aa", true}}},
	{"a{1}?", true, true, true, true, []grammarProbe{{"a", "a", true}}},
	{"a{1}??", false, false, false, false, []grammarProbe{}},
	{"a**", false, false, false, false, []grammarProbe{}},
	{"*", false, false, false, false, []grammarProbe{}},
	{"+a", false, false, false, false, []grammarProbe{}},
	{"?", false, false, false, false, []grammarProbe{}},
	{"^*", false, false, false, false, []grammarProbe{}},
	{"$+", false, false, false, false, []grammarProbe{}},
	{"\\b*", false, false, false, false, []grammarProbe{}},
	{"\\B{2}", false, false, false, false, []grammarProbe{}},
	{"(?=a)*", true, false, false, false, []grammarProbe{{"a", "", true}}},
	{"(?!a)+b", true, false, false, false, []grammarProbe{{"b", "b", true}}},
	{"(?=a){2}a", true, false, false, false, []grammarProbe{{"a", "a", true}}},
	{"(?<=a)*", false, false, false, false, []grammarProbe{}},
	{"(?<!a)?", false, false, false, false, []grammarProbe{}},
	{"(?<a>x)", true, true, true, true, []grammarProbe{}},
	{"(?<>x)", false, false, false, false, []grammarProbe{}},
	{"(?<1a>x)", false, false, false, false, []grammarProbe{}},
	{"(?<a1>x)", true, true, true, true, []grammarProbe{}},
	{"(?<\\u0061>x)", true, true, true, true, []grammarProbe{}},
	{"(?<\\u{61}>x)", true, true, true, true, []grammarProbe{}},
	{"(?<\\uD835\\uDC9C>x)", true, true, true, true, []grammarProbe{}},
	{"(?<\\uD835>x)", false, false, false, false, []grammarProbe{}},
	{"(?x)", false, false, false, false, []grammarProbe{}},
	{"(?", false, false, false, false, []grammarProbe{}},
	{"(?<a>x)(?<a>y)", false, false, false, false, []grammarProbe{}},
	{"(?<a>x)|(?<a>y)", true, true, true, true, []grammarProbe{}},
	{"(", false, false, false, false, []grammarProbe{}},
	{")", false, false, false, false, []grammarProbe{}},
	{"a)", false, false, false, false, []grammarProbe{}},
	{"\\", false, false, false, false, []grammarProbe{}},
}

// identityEscapeCases covers \c and [\c] for every printable ASCII character c,
// with V8's verdict in Annex B, u and v. The strict verdict is
// strictIdentityEscape.
var identityEscapeCases = []struct {
	pattern      string
	annexB, u, v bool
}{
	{"\\ ", true, false, false},
	{"[\\ ]", true, false, false},
	{"\\!", true, false, false},
	{"[\\!]", true, false, true},
	{"\\\"", true, false, false},
	{"[\\\"]", true, false, false},
	{"\\#", true, false, false},
	{"[\\#]", true, false, true},
	{"\\$", true, true, true},
	{"[\\$]", true, true, true},
	{"\\%", true, false, false},
	{"[\\%]", true, false, true},
	{"\\&", true, false, false},
	{"[\\&]", true, false, true},
	{"\\'", true, false, false},
	{"[\\']", true, false, false},
	{"\\(", true, true, true},
	{"[\\(]", true, true, true},
	{"\\)", true, true, true},
	{"[\\)]", true, true, true},
	{"\\*", true, true, true},
	{"[\\*]", true, true, true},
	{"\\+", true, true, true},
	{"[\\+]", true, true, true},
	{"\\,", true, false, false},
	{"[\\,]", true, false, true},
	{"\\-", true, false, false},
	{"[\\-]", true, true, true},
	{"\\.", true, true, true},
	{"[\\.]", true, true, true},
	{"\\/", true, true, true},
	{"[\\/]", true, true, true},
	{"\\0", true, true, true},
	{"[\\0]", true, true, true},
	{"\\1", true, false, false},
	{"[\\1]", true, false, false},
	{"\\2", true, false, false},
	{"[\\2]", true, false, false},
	{"\\3", true, false, false},
	{"[\\3]", true, false, false},
	{"\\4", true, false, false},
	{"[\\4]", true, false, false},
	{"\\5", true, false, false},
	{"[\\5]", true, false, false},
	{"\\6", true, false, false},
	{"[\\6]", true, false, false},
	{"\\7", true, false, false},
	{"[\\7]", true, false, false},
	{"\\8", true, false, false},
	{"[\\8]", true, false, false},
	{"\\9", true, false, false},
	{"[\\9]", true, false, false},
	{"\\:", true, false, false},
	{"[\\:]", true, false, true},
	{"\\;", true, false, false},
	{"[\\;]", true, false, true},
	{"\\<", true, false, false},
	{"[\\<]", true, false, true},
	{"\\=", true, false, false},
	{"[\\=]", true, false, true},
	{"\\>", true, false, false},
	{"[\\>]", true, false, true},
	{"\\?", true, true, true},
	{"[\\?]", true, true, true},
	{"\\@", true, false, false},
	{"[\\@]", true, false, true},
	{"\\A", true, false, false},
	{"[\\A]", true, false, false},
	{"\\B", true, true, true},
	{"[\\B]", true, false, false},
	{"\\C", true, false, false},
	{"[\\C]", true, false, false},
	{"\\D", true, true, true},
	{"[\\D]", true, true, true},
	{"\\E", true, false, false},
	{"[\\E]", true, false, false},
	{"\\F", true, false, false},
	{"[\\F]", true, false, false},
	{"\\G", true, false, false},
	{"[\\G]", true, false, false},
	{"\\H", true, false, false},
	{"[\\H]", true, false, false},
	{"\\I", true, false, false},
	{"[\\I]", true, false, false},
	{"\\J", true, false, false},
	{"[\\J]", true, false, false},
	{"\\K", true, false, false},
	{"[\\K]", true, false, false},
	{"\\L", true, false, false},
	{"[\\L]", true, false, false},
	{"\\M", true, false, false},
	{"[\\M]", true, false, false},
	{"\\N", true, false, false},
	{"[\\N]", true, false, false},
	{"\\O", true, false, false},
	{"[\\O]", true, false, false},
	{"\\P", true, false, false},
	{"[\\P]", true, false, false},
	{"\\Q", true, false, false},
	{"[\\Q]", true, false, false},
	{"\\R", true, false, false},
	{"[\\R]", true, false, false},
	{"\\S", true, true, true},
	{"[\\S]", true, true, true},
	{"\\T", true, false, false},
	{"[\\T]", true, false, false},
	{"\\U", true, false, false},
	{"[\\U]", true, false, false},
	{"\\V", true, false, false},
	{"[\\V]", true, false, false},
	{"\\W", true, true, true},
	{"[\\W]", true, true, true},
	{"\\X", true, false, false},
	{"[\\X]", true, false, false},
	{"\\Y", true, false, false},
	{"[\\Y]", true, false, false},
	{"\\Z", true, false, false},
	{"[\\Z]", true, false, false},
	{"\\[", true, true, true},
	{"[\\[]", true, true, true},
	{"\\\\", true, true, true},
	{"[\\\\]", true, true, true},
	{"\\]", true, true, true},
	{"[\\]]", true, true, true},
	{"\\^", true, true, true},
	{"[\\^]", true, true, true},
	{"\\_", true, false, false},
	{"[\\_]", true, false, false},
	{"\\`", true, false, false},
	{"[\\`]", true, false, true},
	{"\\a", true, false, false},
	{"[\\a]", true, false, false},
	{"\\b", true, true, true},
	{"[\\b]", true, true, true},
	{"\\c", true, false, false},
	{"[\\c]", true, false, false},
	{"\\d", true, true, true},
	{"[\\d]", true, true, true},
	{"\\e", true, false, false},
	{"[\\e]", true, false, false},
	{"\\f", true, true, true},
	{"[\\f]", true, true, true},
	{"\\g", true, false, false},
	{"[\\g]", true, false, false},
	{"\\h", true, false, false},
	{"[\\h]", true, false, false},
	{"\\i", true, false, false},
	{"[\\i]", true, false, false},
	{"\\j", true, false, false},
	{"[\\j]", true, false, false},
	{"\\k", true, false, false},
	{"[\\k]", true, false, false},
	{"\\l", true, false, false},
	{"[\\l]", true, false, false},
	{"\\m", true, false, false},
	{"[\\m]", true, false, false},
	{"\\n", true, true, true},
	{"[\\n]", true, true, true},
	{"\\o", true, false, false},
	{"[\\o]", true, false, false},
	{"\\p", true, false, false},
	{"[\\p]", true, false, false},
	{"\\q", true, false, false},
	{"[\\q]", true, false, false},
	{"\\r", true, true, true},
	{"[\\r]", true, true, true},
	{"\\s", true, true, true},
	{"[\\s]", true, true, true},
	{"\\t", true, true, true},
	{"[\\t]", true, true, true},
	{"\\u", true, false, false},
	{"[\\u]", true, false, false},
	{"\\v", true, true, true},
	{"[\\v]", true, true, true},
	{"\\w", true, true, true},
	{"[\\w]", true, true, true},
	{"\\x", true, false, false},
	{"[\\x]", true, false, false},
	{"\\y", true, false, false},
	{"[\\y]", true, false, false},
	{"\\z", true, false, false},
	{"[\\z]", true, false, false},
	{"\\{", true, true, true},
	{"[\\{]", true, true, true},
	{"\\|", true, true, true},
	{"[\\|]", true, true, true},
	{"\\}", true, true, true},
	{"[\\}]", true, true, true},
	{"\\~", true, false, false},
	{"[\\~]", true, false, true},
}

// strictIdentityEscape is the strict (non-Unicode, no Annex B) verdict on \c,
// or [\c] if inClass, for a printable ASCII c, per ECMA-262 §22.2.1: an
// escape letter with its own meaning is valid when complete; \0 is NUL; \1-\9
// is a backreference, invalid with no groups and in a class; \c, \x, \u, \k,
// \p and \P need a continuation; \B is not a ClassEscape; and any other
// character is an IdentityEscape, valid unless it is UnicodeIDContinue, which
// in ASCII is [A-Za-z0-9_].
func strictIdentityEscape(c byte, inClass bool) bool {
	switch {
	case strings.IndexByte("bdDsSwWfnrtv0", c) >= 0:
		return true
	case c == 'B':
		return !inClass
	case c >= '1' && c <= '9', strings.IndexByte("cxukpP", c) >= 0:
		return false
	case c == '-':
		return true
	}
	isIDContinue := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
	return !isIDContinue
}

func compiles(pattern string, f flags.Flags, opts ...ecma262.Option) bool {
	_, err := ecma262.Compile(pattern, f, opts...)
	return err == nil
}

// vModeUnsupported lists valid v-mode patterns in the tables that the engine
// rejects because it does not implement that part of the UnicodeSets class
// syntax. They must be rejected, never read with another meaning.
var vModeUnsupported = map[string]string{
	"[[a]]": "nested class",
}

func TestEscapeGrammarByMode(t *testing.T) {
	strict := ecma262.WithSyntax(ecma262.SyntaxStrict)
	check := func(pattern, mode string, got, want bool) {
		t.Helper()
		if got != want {
			verb := "reject"
			if want {
				verb = "accept"
			}
			t.Errorf("%s: /%s/ should %s", mode, pattern, verb)
		}
	}
	for _, tc := range escapeGrammarCases {
		check(tc.pattern, "Annex B", compiles(tc.pattern, 0), tc.annexB)
		check(tc.pattern, "strict", compiles(tc.pattern, 0, strict), tc.strict)
		check(tc.pattern, "u", compiles(tc.pattern, flags.Unicode), tc.u)
		if _, unsupported := vModeUnsupported[tc.pattern]; unsupported {
			check(tc.pattern, "v (unsupported)", compiles(tc.pattern, flags.UnicodeSets), false)
		} else {
			check(tc.pattern, "v", compiles(tc.pattern, flags.UnicodeSets), tc.v)
		}
		if !tc.annexB {
			continue
		}
		re, err := ecma262.Compile(tc.pattern, 0)
		if err != nil {
			continue // reported above
		}
		for _, pr := range tc.probes {
			got := re.FindStringSubmatch(pr.input)
			if (got != nil) != pr.matched || got != nil && got[0] != pr.match {
				t.Errorf("Annex B: /%s/ on %+q: got %+q, want match %v %+q", tc.pattern, pr.input, got, pr.matched, pr.match)
			}
		}
	}
	for _, tc := range identityEscapeCases {
		check(tc.pattern, "Annex B", compiles(tc.pattern, 0), tc.annexB)
		check(tc.pattern, "u", compiles(tc.pattern, flags.Unicode), tc.u)
		check(tc.pattern, "v", compiles(tc.pattern, flags.UnicodeSets), tc.v)
		inClass := tc.pattern[0] == '['
		c := tc.pattern[1]
		if inClass {
			c = tc.pattern[2]
		}
		check(tc.pattern, "strict", compiles(tc.pattern, 0, strict), strictIdentityEscape(c, inClass))
	}
}
