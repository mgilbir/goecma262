package ecma262_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// Without u or v, ECMA-262 matches by UTF-16 code unit, so a character above
// U+FFFF is two characters to the pattern and to the input: /^..$/ matches
// "😀". Every expectation in these tables was taken from node, with positions
// as UTF-16 indices. Where node's result has a position between the two halves
// of a surrogate pair, or a string holding half of one, noGoForm is set and the
// engine must return ErrSurrogateSplit (by the rule each method documents).

// unitIndex converts a position in s to a UTF-16 index. A position between the
// halves of a surrogate pair is the first byte's offset plus 2.
func unitIndex(s string, pos int) int {
	if pos < 0 {
		return pos
	}
	n := 0
	for i, r := range s {
		if i >= pos {
			break
		}
		if r > 0xFFFF {
			if pos < i+utf8.RuneLen(r) {
				return n + 1
			}
			n += 2
		} else {
			n++
		}
	}
	return n
}

func unitIndices(s string, idx []int) []int {
	out := make([]int, len(idx))
	for i, p := range idx {
		out[i] = unitIndex(s, p)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func compileFlags(t *testing.T, pattern, fl string) *ecma262.Regexp {
	t.Helper()
	re, err := ecma262.CompileFlags(pattern, fl)
	if err != nil {
		t.Fatalf("/%s/%s: %v", pattern, fl, err)
	}
	return re
}

func TestCodeUnits_Exec(t *testing.T) {
	cases := []struct {
		pattern, flags, input string
		want                  []int // UTF-16 [start, end) per group; nil for no match
		noGoForm, syntaxError bool
	}{
		{`.`, "", "😀", nil, true, false},
		{`^.$`, "", "😀", nil, false, false},
		{`^..$`, "", "😀", []int{0, 2}, false, false},
		{`\uD83D\uDE00`, "", "😀", []int{0, 2}, false, false},
		{`\uD83D`, "", "a😀", nil, true, false},
		{`\uDE00`, "", "😀", nil, true, false},
		{`[\uD800-\uDBFF][\uDC00-\uDFFF]`, "", "x😀", []int{1, 3}, false, false},
		{`\S{2}`, "", "😀", []int{0, 2}, false, false},
		{`[^a]`, "", "😀", nil, true, false},
		{`[^a]{2}`, "", "😀", []int{0, 2}, false, false},
		{`(.)(.)`, "", "😀", nil, true, false},
		{`(..)\1`, "", "😀😀", []int{0, 4, 0, 2}, false, false},
		{`(.)\1`, "", "😀😀", nil, false, false},
		{`^[😀]$`, "", "😀", nil, false, false},
		{`^[😀]+$`, "", "😀", []int{0, 2}, false, false},
		{`😀{2}`, "", "😀😀", nil, false, false},
		{`^(?:😀)+$`, "", "😀😀", []int{0, 4}, false, false},
		{`^a😀?$`, "", "a😀", []int{0, 3}, false, false},
		{`^a😀?$`, "", "a", nil, false, false},
		{`[😀-😂]`, "", "😁", nil, false, true},
		{`(?<𝒜>.)\k<𝒜>`, "", "aa", []int{0, 2, 0, 1}, false, false},
		{`\𝒜`, "", "𝒜", []int{0, 2}, false, false},
		{`𐐀`, "i", "𐐨", nil, false, false},
		{`[𐐀]`, "i", "𐐨", nil, true, false},
		{`𐐀`, "iu", "𐐨", []int{0, 2}, false, false},
		{`(?<=\uD83D\uDE00)x`, "", "😀x", []int{2, 3}, false, false},
		{`(?<=\uDE00)x`, "", "😀x", []int{2, 3}, false, false},
		{`(?<=^.)x`, "", "😀x", nil, false, false},
		{`(?<=^..)x`, "", "😀x", []int{2, 3}, false, false},
		{`^.*\uDE00`, "", "😀a", []int{0, 2}, false, false},
		{`^.*?\uDE00`, "", "😀a", []int{0, 2}, false, false},
		{`^[^x]*\uD83D`, "", "😀😀", nil, true, false},
		{`^[^x]*\uDE00a`, "", "😀😀a", []int{0, 5}, false, false},
		{`(?<=\uD83D.*)a`, "", "😀😀a", []int{4, 5}, false, false},
		{`(?<=^[^x]*\uDE00)a`, "", "😀a", []int{2, 3}, false, false},
		{`\uD83D$`, "m", "😀", nil, false, false},
		{`^\uDE00`, "m", "😀", nil, false, false},
		{`\uD83D\b`, "", "😀", nil, false, false},
		{`\uD83D\B\uDE00`, "", "😀", []int{0, 2}, false, false},
		{`.`, "u", "😀", []int{0, 2}, false, false},
		{`^.$`, "u", "😀", []int{0, 2}, false, false},
		{`\uD83D`, "u", "😀", nil, false, false},
		{`[😀-😂]`, "u", "😁", []int{0, 2}, false, false},
		{`^[😀]$`, "u", "😀", []int{0, 2}, false, false},
		{`😀{2}`, "u", "😀😀", []int{0, 4}, false, false},
		{`.`, "v", "😀", []int{0, 2}, false, false},
	}
	for _, tc := range cases {
		re, err := ecma262.CompileFlags(tc.pattern, tc.flags)
		if tc.syntaxError {
			if err == nil {
				t.Errorf("/%s/%s compiled, want a syntax error", tc.pattern, tc.flags)
			}
			continue
		}
		if err != nil {
			t.Errorf("/%s/%s: %v", tc.pattern, tc.flags, err)
			continue
		}
		got, err := re.FindStringSubmatchIndexErr(tc.input)
		switch {
		case tc.noGoForm:
			if !errors.Is(err, ecma262.ErrSurrogateSplit) {
				t.Errorf("/%s/%s.FindStringSubmatchIndexErr(%q) = %v, %v; want ErrSurrogateSplit", tc.pattern, tc.flags, tc.input, got, err)
			}
		case err != nil || !equalInts(unitIndices(tc.input, got), tc.want) || (got == nil) != (tc.want == nil):
			t.Errorf("/%s/%s.FindStringSubmatchIndexErr(%q) = %v (UTF-16 %v), %v; want %v", tc.pattern, tc.flags, tc.input, got, unitIndices(tc.input, got), err, tc.want)
		}
		if plain := re.FindStringSubmatchIndex(tc.input); !equalInts(plain, got) {
			t.Errorf("/%s/%s.FindStringSubmatchIndex(%q) = %v, want %v as the Err form without its error", tc.pattern, tc.flags, tc.input, plain, got)
		}
		// A match exists exactly when node found one.
		if matched, err := re.MatchStringErr(tc.input); err != nil || matched != (tc.want != nil || tc.noGoForm) {
			t.Errorf("/%s/%s.MatchStringErr(%q) = %v, %v", tc.pattern, tc.flags, tc.input, matched, err)
		}
	}
}

func TestCodeUnits_Test(t *testing.T) {
	cases := []struct {
		pattern, flags, input string
		want                  bool
	}{
		{`^\uD83D(\uDE00)\uD83D\1$`, "", "😀😀", true},
		{`^\uD83D(\uDE00)\uD83D\1$`, "", "😀😁", false},
		{`^\uD83D(\uDE00)\uD83D\1$`, "i", "😀😀", true},
		{`(?<=\1\uD83D(\uDE00))$`, "", "😀😀", true},
		{`(?<=\1\uD83D(\uDE00))$`, "", "😁😀", false},
		{`(\uD83D)\1`, "", "😀😀", false},
		{`(\uD83D)\1`, "i", "😀😀", false},
		{`^\uD83D(\uDE00)\uD83E\1$`, "", "😀🨀", true},
		{`(?<=\1\uD83E(\uDE00))$`, "", "😀🨀", true},
		{`\uD83D`, "", "😀", true},
		{`^\uDE00`, "", "😀", false},
		{`\uDE00$`, "", "😀", true},
	}
	for _, tc := range cases {
		re := compileFlags(t, tc.pattern, tc.flags)
		if got, err := re.MatchStringErr(tc.input); err != nil || got != tc.want {
			t.Errorf("/%s/%s.MatchStringErr(%q) = %v, %v; want %v", tc.pattern, tc.flags, tc.input, got, err, tc.want)
		}
	}
}

func TestCodeUnits_FindAll(t *testing.T) {
	cases := []struct {
		pattern, flags, input string
		want                  [][]int
		noGoForm              bool
	}{
		{`.`, "", "😀a", nil, true},
		{`..`, "", "😀a", [][]int{[]int{0, 2}}, false},
		{`x*`, "", "😀", nil, true},
		{`\uD83D\uDE00|a`, "", "a😀", [][]int{[]int{0, 1}, []int{1, 3}}, false},
		{`x*`, "u", "😀", [][]int{[]int{0, 0}, []int{2, 2}}, false},
	}
	for _, tc := range cases {
		re := compileFlags(t, tc.pattern, tc.flags)
		got, err := re.FindAllStringSubmatchIndexErr(tc.input, -1)
		if tc.noGoForm {
			if !errors.Is(err, ecma262.ErrSurrogateSplit) {
				t.Errorf("/%s/%s.FindAllStringSubmatchIndexErr(%q) = %v, %v; want ErrSurrogateSplit", tc.pattern, tc.flags, tc.input, got, err)
			}
			continue
		}
		ok := err == nil && len(got) == len(tc.want)
		for i := 0; ok && i < len(got); i++ {
			ok = equalInts(unitIndices(tc.input, got[i]), tc.want[i])
		}
		if !ok {
			t.Errorf("/%s/%s.FindAllStringSubmatchIndexErr(%q) = %v, %v; want UTF-16 %v", tc.pattern, tc.flags, tc.input, got, err, tc.want)
		}
	}
}

func TestCodeUnits_Replace(t *testing.T) {
	cases := []struct {
		pattern, flags, input, repl, want string
		noGoForm                          bool
	}{
		{`x*`, "g", "😀", "", "😀", false},
		{`x*`, "g", "😀", "-", "", true},
		{`\uD83D`, "g", "😀😀", "$&", "😀😀", false},
		{`(\uD83D)`, "g", "a😀", "$1", "a😀", false},
		{`\uD83D`, "", "😀", "x", "", true},
		{`\uDE00`, "", "😀", "", "", true},
		{`.`, "g", "😀", "$&", "😀", false},
		{`.`, "g", "😀", "[$&]", "", true},
		{`\uD83D\uDE00`, "g", "😀", "x", "x", false},
		{`(?:)`, "g", "a😀", "", "a😀", false},
		{`\uDE00(.)`, "", "😀😁", "", "😁", false},
		{`\uDE00(.)`, "", "😀😁", "$1", "", true},
		{`(\uD83D)(\uDE00)`, "g", "😀", "$1$2", "😀", false},
		{`(\uD83D)(\uDE00)`, "g", "😀", "$2$1", "", true},
		{`(?:)`, "gu", "😀", "-", "-😀-", false},
		{`\uDE00`, "", "😀", "$`", "", true},
	}
	for _, tc := range cases {
		re := compileFlags(t, tc.pattern, tc.flags)
		got, err := re.ReplaceAllStringErr(tc.input, tc.repl)
		if tc.noGoForm {
			if !errors.Is(err, ecma262.ErrSurrogateSplit) {
				t.Errorf("/%s/%s.ReplaceAllStringErr(%q, %q) = %q, %v; want ErrSurrogateSplit", tc.pattern, tc.flags, tc.input, tc.repl, got, err)
			}
		} else if err != nil || got != tc.want {
			t.Errorf("/%s/%s.ReplaceAllStringErr(%q, %q) = %q, %v; want %q", tc.pattern, tc.flags, tc.input, tc.repl, got, err, tc.want)
		}
		// Without an error result, a result with no Go form leaves src as it is.
		want := tc.want
		if tc.noGoForm {
			want = tc.input
		}
		if got := re.ReplaceAllString(tc.input, tc.repl); got != want {
			t.Errorf("/%s/%s.ReplaceAllString(%q, %q) = %q, want %q", tc.pattern, tc.flags, tc.input, tc.repl, got, want)
		}
	}
}

func TestCodeUnits_Split(t *testing.T) {
	cases := []struct {
		pattern, flags, input string
		want                  []string
		noGoForm              bool
	}{
		{`\S`, "", "😀", []string{"", "", ""}, false},
		{`(?:)`, "", "😀", nil, true},
		{`(?:)`, "", "a😀", nil, true},
		{`\uDE00`, "", "😀a😀", nil, true},
		{`\uD83D\uDE00`, "", "a😀b", []string{"a", "b"}, false},
		{`.`, "", "😀", []string{"", "", ""}, false},
		{`x*`, "", "😀", nil, true},
		{`(?:)`, "u", "😀", []string{"😀"}, false},
		{`[^a]`, "", "a😀a", []string{"a", "", "a"}, false},
		{`\uD83D`, "", "😀", nil, true},
		{`\uDE00`, "", "a😀", nil, true},
	}
	for _, tc := range cases {
		re := compileFlags(t, tc.pattern, tc.flags)
		got, err := re.SplitErr(tc.input, -1)
		if tc.noGoForm {
			if !errors.Is(err, ecma262.ErrSurrogateSplit) {
				t.Errorf("/%s/%s.SplitErr(%q, -1) = %q, %v; want ErrSurrogateSplit", tc.pattern, tc.flags, tc.input, got, err)
			}
			continue
		}
		if err != nil || !equalStrings(got, tc.want) {
			t.Errorf("/%s/%s.SplitErr(%q, -1) = %q, %v; want %q", tc.pattern, tc.flags, tc.input, got, err, tc.want)
		}
		if plain := re.Split(tc.input, -1); !equalStrings(plain, tc.want) {
			t.Errorf("/%s/%s.Split(%q, -1) = %q, want %q", tc.pattern, tc.flags, tc.input, plain, tc.want)
		}
	}
}

// The methods without an error result report a result with no Go form as no
// match; the iterating ones stop there. The JavaScript results they stand in
// for are in the comments, from node.
func TestCodeUnits_NoGoForm(t *testing.T) {
	re := ecma262.MustCompile(`.`, flags.Flags(0))
	if got := re.FindString("😀"); got != "" {
		t.Errorf(`/./.FindString("😀") = %q, want "" (JavaScript: "\ud83d")`, got)
	}
	if got := re.FindStringIndex("😀"); got != nil {
		t.Errorf(`/./.FindStringIndex("😀") = %v, want nil`, got)
	}
	if got, err := re.FindStringIndexErr("😀"); got != nil || !errors.Is(err, ecma262.ErrSurrogateSplit) {
		t.Errorf(`/./.FindStringIndexErr("😀") = %v, %v; want ErrSurrogateSplit`, got, err)
	}
	if got := re.FindStringSubmatch("😀"); got != nil {
		t.Errorf(`/./.FindStringSubmatch("😀") = %q, want nil`, got)
	}
	// ["a", "\ud83d", "\ude00", "b"]
	if got := re.FindAllString("a😀b", -1); !equalStrings(got, []string{"a"}) {
		t.Errorf(`/./g.FindAllString("a😀b") = %q, want ["a"]`, got)
	}
	if got := re.FindAllStringIndex("a😀b", -1); len(got) != 1 || !equalInts(got[0], []int{0, 1}) {
		t.Errorf(`/./g.FindAllStringIndex("a😀b") = %v, want [[0 1]]`, got)
	}
	if got := re.FindAllStringSubmatch("a😀b", -1); len(got) != 1 || !equalStrings(got[0], []string{"a"}) {
		t.Errorf(`/./g.FindAllStringSubmatch("a😀b") = %q, want [["a"]]`, got)
	}
	// Match text is a Go string even when its position is not: ["", "", ""].
	empty := ecma262.MustCompile(`x*`, flags.Flags(0))
	if got := empty.FindAllString("😀", -1); !equalStrings(got, []string{"", "", ""}) {
		t.Errorf(`/x*/g.FindAllString("😀") = %q, want ["" "" ""]`, got)
	}
	if got := empty.FindAllStringIndex("😀", -1); len(got) != 1 {
		t.Errorf(`/x*/g.FindAllStringIndex("😀") = %v, want only [[0 0]]`, got)
	}
	// ["", ""] at index 1.
	after := ecma262.MustCompile(`(?<=\uD83D)()`, flags.Flags(0))
	if got := after.FindStringSubmatch("😀"); !equalStrings(got, []string{"", ""}) {
		t.Errorf(`/(?<=\uD83D)()/.FindStringSubmatch("😀") = %q, want ["" ""]`, got)
	}
	if got, err := after.FindStringSubmatchIndexErr("😀"); !errors.Is(err, ecma262.ErrSurrogateSplit) {
		t.Errorf(`/(?<=\uD83D)()/.FindStringSubmatchIndexErr("😀") = %v, %v; want ErrSurrogateSplit`, got, err)
	}
	// ["a", "\ud83d", "\ude00"]: Split stops after the last separator that
	// ended on a byte offset.
	parts := ecma262.MustCompile(`(?:)`, flags.Flags(0))
	if got := parts.Split("a😀", -1); !equalStrings(got, []string{"a", "😀"}) {
		t.Errorf(`/(?:)/.Split("a😀", -1) = %q, want ["a" "😀"]`, got)
	}
	// ["", "\ude00x"]: the separator ends between the halves, so Split keeps
	// nothing but the whole of s.
	if got := ecma262.MustCompile(`\uD83D`, flags.Flags(0)).Split("😀x", -1); !equalStrings(got, []string{"😀x"}) {
		t.Errorf(`/\uD83D/.Split("😀x", -1) = %q, want ["😀x"]`, got)
	}
	upper := ecma262.MustCompile(`.`, flags.Global)
	if got := upper.ReplaceAllStringFunc("a😀", strings.ToUpper); got != "a😀" {
		t.Errorf(`/./g.ReplaceAllStringFunc("a😀", ToUpper) = %q, want "a😀" unchanged`, got)
	}
	if got := ecma262.MustCompile(`x*`, flags.Global).ReplaceAllStringFunc("😀", func(string) string { return "" }); got != "😀" {
		t.Errorf(`/x*/g.ReplaceAllStringFunc("😀", "") = %q, want "😀"`, got)
	}
	if got := ecma262.MustCompile(`x*`, flags.Global).ReplaceAllStringFunc("😀", func(string) string { return "-" }); got != "😀" {
		t.Errorf(`/x*/g.ReplaceAllStringFunc("😀", "-") = %q, want "😀" unchanged`, got)
	}
}

// lastIndex can fall between the halves of a pair, where it is the offset of
// the character's first byte plus 2, and matching resumes from there.
func TestCodeUnits_LastIndex(t *testing.T) {
	re := ecma262.MustCompile(`\uD83D`, flags.Global)
	if !re.MatchString("😀") || re.LastIndex() != 2 {
		t.Errorf(`/\uD83D/g.test("😀"): lastIndex %d, want 2 (JavaScript: 1)`, re.LastIndex())
	}
	if re.MatchString("😀") || re.LastIndex() != 0 {
		t.Errorf(`/\uD83D/g.test("😀") again: lastIndex %d, want false and 0`, re.LastIndex())
	}
	low := ecma262.MustCompile(`\uDE00`, flags.Sticky)
	low.SetLastIndex(2)
	if !low.MatchString("😀") || low.LastIndex() != 4 {
		t.Errorf(`/\uDE00/y at lastIndex 2 of "😀": lastIndex %d, want 4`, low.LastIndex())
	}
	pair := ecma262.MustCompile(`😀`, flags.Global)
	pair.SetLastIndex(2)
	if got := pair.FindStringSubmatch("😀😀"); !equalStrings(got, []string{"😀"}) || pair.LastIndex() != 8 {
		t.Errorf(`/😀/g.exec("😀😀") from 2 = %q, lastIndex %d; want ["😀"], 8`, got, pair.LastIndex())
	}
	// A result with no Go form is no match, and resets lastIndex.
	re.SetLastIndex(0)
	if got := re.FindStringSubmatch("😀"); got != nil || re.LastIndex() != 0 {
		t.Errorf(`/\uD83D/g.FindStringSubmatch("😀") = %q, lastIndex %d; want nil, 0`, got, re.LastIndex())
	}
}

// Strict mode accepts \𝒜: without u the escape is of a lead surrogate, which
// is not UnicodeIDContinue.
func TestCodeUnits_StrictIdentityEscape(t *testing.T) {
	re, err := ecma262.Compile(`\𝒜`, flags.Flags(0), ecma262.WithSyntax(ecma262.SyntaxStrict))
	if err != nil {
		t.Fatal(err)
	}
	if !re.MatchString("𝒜") {
		t.Error(`/\𝒜/ does not match "𝒜"`)
	}
	if _, err := ecma262.Compile(`\𝒜`, flags.Unicode); err == nil {
		t.Error(`/\𝒜/u compiled; an identity escape of 𝒜 is a syntax error under u`)
	}
}

// Input that is not valid UTF-8, and start positions inside a character, must
// not be mistaken for the position between two halves.
func TestCodeUnits_InvalidUTF8(t *testing.T) {
	truncated := "\xf0\x9f\x98" // the first three bytes of 😀
	if got := ecma262.MustCompile(`^...$`, flags.Flags(0)).FindStringIndex(truncated); !equalInts(got, []int{0, 3}) {
		t.Errorf(`/^...$/.FindStringIndex(%q) = %v, want [0 3]`, truncated, got)
	}
	s := "😀x\xff😀"
	for _, p := range []string{`.`, `x*`, `\uDE00`, `(?<=\uD83D)`, `(.)\1`, `(?<=(.))\1`} {
		for _, fl := range []flags.Flags{0, flags.Global, flags.Sticky, flags.Unicode} {
			re := ecma262.MustCompile(p, fl)
			for start := 0; start <= len(s)+1; start++ {
				re.SetLastIndex(start)
				re.MatchString(s)
				re.FindStringSubmatch(s)
			}
			re.FindAllStringSubmatchIndex(s, -1)
			re.ReplaceAllString(s, "$&-")
			re.Split(s, -1)
		}
	}
}
