package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// Split with n < 0 must split where String.prototype.split does; these
// expectations were taken from node. An empty match splits between
// characters, but never at the start of the input, at its end, or where the
// previous match ended, and splitting "" gives nothing when the separator
// matches it.
func TestSplit_MatchesJavaScript(t *testing.T) {
	cases := []struct {
		pattern string
		flags   flags.Flags
		input   string
		want    []string
	}{
		{`x*`, flags.Flags(0), "abc", []string{"a", "b", "c"}},
		{`x*`, flags.Flags(0), "", []string{}},
		{`a`, flags.Flags(0), "", []string{""}},
		{`(?:)`, flags.Flags(0), "abc", []string{"a", "b", "c"}},
		{``, flags.Flags(0), "abc", []string{"a", "b", "c"}},
		{`\d`, flags.Flags(0), "a1b2c3", []string{"a", "b", "c", ""}},
		{`\d`, flags.Flags(0), "1a", []string{"", "a"}},
		{`\d*`, flags.Flags(0), "a1b22c", []string{"a", "b", "c"}},
		{`\d*`, flags.Flags(0), "12", []string{"", ""}},
		{`$`, flags.Flags(0), "abc", []string{"abc"}},
		{`^`, flags.Flags(0), "abc", []string{"abc"}},
		{`$`, flags.Multiline, "a\u000ab", []string{"a", "\u000ab"}},
		{`^`, flags.Multiline, "a\u000ab", []string{"a\u000a", "b"}},
		{`\b`, flags.Flags(0), "ab cd", []string{"ab", " ", "cd"}},
		{`\B`, flags.Flags(0), "ab", []string{"a", "b"}},
		{`(?=b)`, flags.Flags(0), "abab", []string{"a", "ba", "b"}},
		{`(?<=a)`, flags.Flags(0), "aab", []string{"a", "a", "b"}},
		{`a?`, flags.Flags(0), "baab", []string{"b", "", "b"}},
		{`a*?`, flags.Flags(0), "aab", []string{"a", "a", "b"}},
		{``, flags.Unicode, "\u00e9\u65e5\u672c\U0001f600", []string{"\u00e9", "\u65e5", "\u672c", "\U0001f600"}},
		{`.`, flags.Unicode, "\u00e9\U0001f600", []string{"", "", ""}},
		{`x*`, flags.Unicode, "\u65e5\u672c", []string{"\u65e5", "\u672c"}},
		{`a`, flags.Sticky, "bab", []string{"b", "b"}},
		{`a|`, flags.Sticky, "bab", []string{"b", "b"}},
		{`\s*`, flags.Flags(0), " a b ", []string{"", "a", "b", ""}},
		{`b*`, flags.Flags(0), "abbc", []string{"a", "c"}},
		{`,`, flags.Flags(0), ",a,,b,", []string{"", "a", "", "b", ""}},
	}
	for _, tc := range cases {
		re := ecma262.MustCompile(tc.pattern, tc.flags)
		got := re.Split(tc.input, -1)
		if !equalStrings(got, tc.want) || got == nil {
			t.Errorf("/%s/ (flags %v).Split(%q, -1) = %q, want %q", tc.pattern, tc.flags, tc.input, got, tc.want)
		}
		gotErr, err := re.SplitErr(tc.input, -1)
		if err != nil || !equalStrings(gotErr, got) {
			t.Errorf("/%s/.SplitErr(%q, -1) = %q, %v; want %q", tc.pattern, tc.input, gotErr, err, got)
		}
	}
}

// n > 0 follows Go's regexp: at most n substrings, the last being the
// unsplit remainder; n == 0 returns nil.
func TestSplit_Limit(t *testing.T) {
	cases := []struct {
		pattern, input string
		n              int
		want           []string
	}{
		{`x*`, "abc", 2, []string{"a", "bc"}},
		{`x*`, "abc", 1, []string{"abc"}},
		{`\d`, "a1b2c3", 3, []string{"a", "b", "c3"}},
		{`\d`, "a1b2c3", 10, []string{"a", "b", "c", ""}},
	}
	for _, tc := range cases {
		if got := ecma262.MustCompile(tc.pattern, flags.Flags(0)).Split(tc.input, tc.n); !equalStrings(got, tc.want) {
			t.Errorf("/%s/.Split(%q, %d) = %q, want %q", tc.pattern, tc.input, tc.n, got, tc.want)
		}
	}
	if got := ecma262.MustCompile(`x*`, flags.Flags(0)).Split("abc", 0); got != nil {
		t.Errorf("Split(_, 0) = %q, want nil", got)
	}
}
