package ecma262_test

import (
	"strings"
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// v-flag class sets (ClassSetExpression). Every expectation below was taken
// from node (V8, Unicode 17.0), not derived by hand; Test262's generated
// unicodeSets suite checks only whether a whole string matches, so these cover
// what it does not: syntax edge cases, which string alternative a class
// matches (longest first, in both directions), and case-insensitive set
// algebra, which Test262 does not exercise at all.

func TestUnicodeSets_Syntax(t *testing.T) {
	cases := []struct {
		pattern string
		flags   flags.Flags
		valid   bool
	}{
		{`[a-z&&b]`, flags.UnicodeSets, false},
		{`[b&&a-z]`, flags.UnicodeSets, false},
		{`[a-z--b]`, flags.UnicodeSets, false},
		{`[b--a-z]`, flags.UnicodeSets, false},
		{`[a&&&b]`, flags.UnicodeSets, false},
		{`[a&&&]`, flags.UnicodeSets, false},
		{`[a&&b--c]`, flags.UnicodeSets, false},
		{`[ab&&c]`, flags.UnicodeSets, false},
		{`[a--b&&c]`, flags.UnicodeSets, false},
		{`[a&&b&&c]`, flags.UnicodeSets, true},
		{`[a--b--c]`, flags.UnicodeSets, true},
		{`[[ab]&&c]`, flags.UnicodeSets, true},
		{`[\&&&a]`, flags.UnicodeSets, true},
		{`[a&&\&]`, flags.UnicodeSets, true},
		{`[&&a]`, flags.UnicodeSets, false},
		{`[a&&]`, flags.UnicodeSets, false},
		{`[a--]`, flags.UnicodeSets, false},
		{`[a---b]`, flags.UnicodeSets, false},
		{`[a!!b]`, flags.UnicodeSets, false},
		{`[a!b]`, flags.UnicodeSets, true},
		{`[$$]`, flags.UnicodeSets, false},
		{`[\$]`, flags.UnicodeSets, true},
		{`[^^^]`, flags.UnicodeSets, false},
		{`[_^^]`, flags.UnicodeSets, false},
		{`[(]`, flags.UnicodeSets, false},
		{`[\(]`, flags.UnicodeSets, true},
		{`[a|b]`, flags.UnicodeSets, false},
		{`[a{]`, flags.UnicodeSets, false},
		{`[a}]`, flags.UnicodeSets, false},
		{`[/]`, flags.UnicodeSets, false},
		{`[\/]`, flags.UnicodeSets, true},
		{`[-]`, flags.UnicodeSets, false},
		{`[a-]`, flags.UnicodeSets, false},
		{`[-a]`, flags.UnicodeSets, false},
		{`[\-]`, flags.UnicodeSets, true},
		{`[a\-z]`, flags.UnicodeSets, true},
		{`[a-b-c]`, flags.UnicodeSets, false},
		{`[z-a]`, flags.UnicodeSets, false},
		{`[a-a]`, flags.UnicodeSets, true},
		{`[\d-z]`, flags.UnicodeSets, false},
		{`[a-\d]`, flags.UnicodeSets, false},
		{`[a-[b]]`, flags.UnicodeSets, false},
		{`[a-\q{b}]`, flags.UnicodeSets, false},
		{`[\q{a}-b]`, flags.UnicodeSets, false},
		{`[]`, flags.UnicodeSets, true},
		{`[^]`, flags.UnicodeSets, true},
		{`[[]]`, flags.UnicodeSets, true},
		{`[[^]]`, flags.UnicodeSets, true},
		{`[[a][b]]`, flags.UnicodeSets, true},
		{`[a[b]]`, flags.UnicodeSets, true},
		{`[a`, flags.UnicodeSets, false},
		{`[[a]`, flags.UnicodeSets, false},
		{`[\q{}]`, flags.UnicodeSets, true},
		{`[\q{a|bc|}]`, flags.UnicodeSets, true},
		{`[\q{a\|b}]`, flags.UnicodeSets, true},
		{`[\q{\cA}]`, flags.UnicodeSets, true},
		{`[\q{\0}]`, flags.UnicodeSets, true},
		{`[\q{\01}]`, flags.UnicodeSets, false},
		{`[\q{\b}]`, flags.UnicodeSets, true},
		{`[\q{\-}]`, flags.UnicodeSets, true},
		{`[\q{\d}]`, flags.UnicodeSets, false},
		{`[\q{\q}]`, flags.UnicodeSets, false},
		{`[\q{a&&b}]`, flags.UnicodeSets, false},
		{`[\q{a&b}]`, flags.UnicodeSets, true},
		{`[\q{a-b}]`, flags.UnicodeSets, false},
		{`[\q{(}]`, flags.UnicodeSets, false},
		{`[\q{a]`, flags.UnicodeSets, false},
		{`[\q]`, flags.UnicodeSets, false},
		{`[\q{}}]`, flags.UnicodeSets, false},
		{`\q{a}`, flags.UnicodeSets, false},
		{`[\q{\u{1F600}}]`, flags.UnicodeSets, true},
		{`[\q{\ud83d\ude00}]`, flags.UnicodeSets, true},
		{`[^\q{}]`, flags.UnicodeSets, false},
		{`[^\q{a}]`, flags.UnicodeSets, true},
		{`[^\q{ab}]`, flags.UnicodeSets, false},
		{`[^\q{a|b}]`, flags.UnicodeSets, true},
		{`[^[\q{}]]`, flags.UnicodeSets, false},
		{`[^[\q{ab}--\q{ab}]]`, flags.UnicodeSets, false},
		{`[^[\q{ab}&&a]]`, flags.UnicodeSets, true},
		{`[^[a&&\q{ab}]]`, flags.UnicodeSets, true},
		{`[^[\q{ab}&&\q{ab}]]`, flags.UnicodeSets, false},
		{`[^[^\q{ab}]]`, flags.UnicodeSets, false},
		{`[^[a--\q{ab}]]`, flags.UnicodeSets, true},
		{`[^\p{RGI_Emoji}]`, flags.UnicodeSets, false},
		{`\P{RGI_Emoji}`, flags.UnicodeSets, false},
		{`[\P{RGI_Emoji}]`, flags.UnicodeSets, false},
		{`\p{RGI_Emoji}`, flags.UnicodeSets, true},
		{`\p{RGI_Emoji}`, flags.Unicode, false},
		{`[\p{RGI_Emoji}]`, flags.Unicode, false},
		{`\p{Basic_Emoji}`, flags.Unicode, false},
		{`[\p{rgi_emoji}]`, flags.UnicodeSets, false},
		{`[\p{RGI_Emoji=Yes}]`, flags.UnicodeSets, false},
		{`\p{Emoji_Keycap_Sequence}`, flags.UnicodeSets, true},
		{`[\c]`, flags.UnicodeSets, false},
		{`[\cA]`, flags.UnicodeSets, true},
		{`[\c1]`, flags.UnicodeSets, false},
		{`[\b]`, flags.UnicodeSets, true},
		{`[\B]`, flags.UnicodeSets, false},
		{`[\k]`, flags.UnicodeSets, false},
		{`[\0]`, flags.UnicodeSets, true},
		{`[\1]`, flags.UnicodeSets, false},
		{`[\p{L}--\p{Lu}]`, flags.UnicodeSets, true},
		{`[\w&&\d]`, flags.UnicodeSets, true},
		{`[\s--\n]`, flags.UnicodeSets, true},
		{`[.]`, flags.UnicodeSets, true},
		{`[\P{L}]`, flags.UnicodeSets, true},
		{`[^\P{L}]`, flags.UnicodeSets, true},
		{`(?<a>[[a]])\k<a>`, flags.UnicodeSets, true},
		{`[(]`, flags.Unicode, true},
		{`[[]`, flags.Unicode, true},
	}
	for _, tc := range cases {
		_, err := ecma262.Compile(tc.pattern, tc.flags)
		if tc.valid && err != nil {
			t.Errorf("/%s/ (flags %v): unexpected error %v", tc.pattern, tc.flags, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("/%s/ (flags %v): expected a SyntaxError", tc.pattern, tc.flags)
		}
	}
}

func TestUnicodeSets_Exec(t *testing.T) {
	cases := []struct {
		pattern string
		flags   flags.Flags
		input   string
		index   int      // byte offset of the match, or -1
		groups  []string // "<unset>" for a group that did not participate
	}{
		{`[\p{L}--\p{Lu}]`, flags.UnicodeSets, "A-a", 2, []string{"a"}},
		{`[\p{L}--\p{Lu}]+`, flags.UnicodeSets, "ABcdE", 2, []string{"cd"}},
		{`[\w&&\d]+`, flags.UnicodeSets, "ab12c", 2, []string{"12"}},
		{`[\w--\d]+`, flags.UnicodeSets, "12ab3", 2, []string{"ab"}},
		{`[[a-z]--[aeiou]]+`, flags.UnicodeSets, "aabcde", 2, []string{"bcd"}},
		{`[[a-z]&&[aeiou]]+`, flags.UnicodeSets, "xyzaeb", 3, []string{"ae"}},
		{`[a-c[x-z]]+`, flags.UnicodeSets, "-bzy-", 1, []string{"bzy"}},
		{`[^[a-z]--[x]]`, flags.UnicodeSets, "abcx", 3, []string{"x"}},
		{`[\s--\n]`, flags.UnicodeSets, "\u000a \u0009", 1, []string{" "}},
		{`[\q{abc|ab|a}]`, flags.UnicodeSets, "abcd", 0, []string{"abc"}},
		{`([\q{abc|ab}])`, flags.UnicodeSets, "abc", 0, []string{"abc", "abc"}},
		{`([\q{ab|abc}])c?`, flags.UnicodeSets, "abc", 0, []string{"abc", "abc"}},
		{`^[\q{ab|a}]b$`, flags.UnicodeSets, "ab", 0, []string{"ab"}},
		{`^([\q{abc|ab|a}])(b?c?)$`, flags.UnicodeSets, "abc", 0, []string{"abc", "abc", ""}},
		{`([\q{abcd|ab|abc}])(.*)`, flags.UnicodeSets, "abcde", 0, []string{"abcde", "abcd", "e"}},
		{`([\q{a|}])x`, flags.UnicodeSets, "x", 0, []string{"x", ""}},
		{`([\q{}])`, flags.UnicodeSets, "x", 0, []string{"", ""}},
		{`(?:[\q{}])*x`, flags.UnicodeSets, "x", 0, []string{"x"}},
		{`([\q{ab|}])+`, flags.UnicodeSets, "ababc", 0, []string{"abab", "ab"}},
		{`(?<=([\q{abc|bc}]))d`, flags.UnicodeSets, "abcd", 3, []string{"d", "abc"}},
		{`(?<=([\q{bc|abc}]))d`, flags.UnicodeSets, "abcd", 3, []string{"d", "abc"}},
		{`(?<=([\q{c|abc|bc}]))$`, flags.UnicodeSets, "abc", 3, []string{"", "abc"}},
		{`(?<![\q{ab}])c`, flags.UnicodeSets, "abc xc", 5, []string{"c"}},
		{`(?<=[\q{xy}])q`, flags.UnicodeSets, "xyq q", 2, []string{"q"}},
		{`[\q{abc|ab}--\q{abc}]+`, flags.UnicodeSets, "abcab", 0, []string{"ab"}},
		{`([\q{abc|ab}&&\q{ab|x}])`, flags.UnicodeSets, "abc", 0, []string{"ab", "ab"}},
		{`[\q{ab}--[a]]`, flags.UnicodeSets, "ab a", 0, []string{"ab"}},
		{`[\q{a|bc}--a]+`, flags.UnicodeSets, "abcbc", 1, []string{"bcbc"}},
		{`[[\q{ab}]\q{cd}]+`, flags.UnicodeSets, "abcdab", 0, []string{"abcdab"}},
		{`[a]`, flags.UnicodeSets | flags.IgnoreCase, "A", 0, []string{"A"}},
		{`[^a]`, flags.UnicodeSets | flags.IgnoreCase, "A", -1, nil},
		{`[^a]`, flags.UnicodeSets | flags.IgnoreCase, "b", 0, []string{"b"}},
		{`[\W]`, flags.UnicodeSets | flags.IgnoreCase, "\u017f", -1, nil},
		{`[\w]`, flags.UnicodeSets | flags.IgnoreCase, "\u017f", 0, []string{"\u017f"}},
		{`[\w]`, flags.UnicodeSets | flags.IgnoreCase, "\u212a", 0, []string{"\u212a"}},
		{`[^\W]`, flags.UnicodeSets | flags.IgnoreCase, "\u017f", 0, []string{"\u017f"}},
		{`[\p{Lu}]`, flags.UnicodeSets | flags.IgnoreCase, "a", 0, []string{"a"}},
		{`[^\p{Lu}]`, flags.UnicodeSets | flags.IgnoreCase, "a", -1, nil},
		{`[\P{Lu}]`, flags.UnicodeSets | flags.IgnoreCase, "a", -1, nil},
		{`[\P{Ll}]`, flags.UnicodeSets | flags.IgnoreCase, "A", -1, nil},
		{`[[a-z]--k]+`, flags.UnicodeSets | flags.IgnoreCase, "JKL", 0, []string{"J"}},
		{`[[a-z]&&K]`, flags.UnicodeSets | flags.IgnoreCase, "k", 0, []string{"k"}},
		{`[\p{L}--\p{Lu}]`, flags.UnicodeSets | flags.IgnoreCase, "A", -1, nil},
		{`[[^k]--x]`, flags.UnicodeSets | flags.IgnoreCase, "K\u212ax", -1, nil},
		{`[\q{ab}]`, flags.UnicodeSets | flags.IgnoreCase, "AB", 0, []string{"AB"}},
		{`([\q{AB|ab}])`, flags.UnicodeSets | flags.IgnoreCase, "aB", 0, []string{"aB", "aB"}},
		{`[\q{ab}--\q{AB}]`, flags.UnicodeSets | flags.IgnoreCase, "ab", -1, nil},
		{`[\q{ſx}]`, flags.UnicodeSets | flags.IgnoreCase, "SX", 0, []string{"SX"}},
		{`[\q{ab}&&\q{AB}]`, flags.UnicodeSets | flags.IgnoreCase, "Ab", 0, []string{"Ab"}},
		{`[\u{10400}]`, flags.UnicodeSets | flags.IgnoreCase, "\U00010428", 0, []string{"\U00010428"}},
		{`\p{RGI_Emoji}`, flags.UnicodeSets, "a\U0001f600b", 1, []string{"\U0001f600"}},
		{`\p{RGI_Emoji}+`, flags.UnicodeSets, "x\U0001f468\u200d\U0001f469\u200d\U0001f467\U0001f600y", 1, []string{"\U0001f468\u200d\U0001f469\u200d\U0001f467\U0001f600"}},
		{`^\p{RGI_Emoji}$`, flags.UnicodeSets, "#\ufe0f\u20e3", 0, []string{"#\ufe0f\u20e3"}},
		{`^\p{RGI_Emoji}$`, flags.UnicodeSets, "#", -1, nil},
		{`[\p{RGI_Emoji}--\q{😀}]`, flags.UnicodeSets, "\U0001f600\U0001f601", 4, []string{"\U0001f601"}},
		{`[\p{Emoji_Keycap_Sequence}--\q{#️⃣}]`, flags.UnicodeSets, "#\ufe0f\u20e3*\ufe0f\u20e3", 7, []string{"*\ufe0f\u20e3"}},
		{`(?<=\p{RGI_Emoji_Flag_Sequence})x`, flags.UnicodeSets, "\U0001f1fa\U0001f1f8x", 8, []string{"x"}},
		{`[\p{RGI_Emoji_Flag_Sequence}&&\q{🇺🇸|ab}]`, flags.UnicodeSets, "ab\U0001f1fa\U0001f1f8", 2, []string{"\U0001f1fa\U0001f1f8"}},
		{`\p{RGI_Emoji_Tag_Sequence}`, flags.UnicodeSets, "\U0001f3f4\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f", 0, []string{"\U0001f3f4\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f"}},
		{`[\p{Basic_Emoji}]`, flags.UnicodeSets | flags.IgnoreCase, "\u231a", 0, []string{"\u231a"}},
		{`\p{RGI_Emoji_Modifier_Sequence}`, flags.UnicodeSets, "\U0001f44b\U0001f3fd", 0, []string{"\U0001f44b\U0001f3fd"}},
		{`[^\d]+`, flags.UnicodeSets, "12ab3", 2, []string{"ab"}},
		{`[\d--[5-9]]+`, flags.UnicodeSets, "3456", 0, []string{"34"}},
		{`[\p{ASCII}--\p{L}]+`, flags.UnicodeSets, "ab12!c", 2, []string{"12!"}},
	}
	for _, tc := range cases {
		re, err := ecma262.Compile(tc.pattern, tc.flags)
		if err != nil {
			t.Errorf("/%s/ (flags %v): %v", tc.pattern, tc.flags, err)
			continue
		}
		idx, err := re.FindStringSubmatchIndexErr(tc.input)
		if err != nil {
			t.Errorf("/%s/ on %q: %v", tc.pattern, tc.input, err)
			continue
		}
		var got []string
		index := -1
		if idx != nil {
			index = idx[0]
			for g := 0; g < len(idx); g += 2 {
				if idx[g] < 0 {
					got = append(got, "<unset>")
				} else {
					got = append(got, tc.input[idx[g]:idx[g+1]])
				}
			}
		}
		if index != tc.index || !equalStrings(got, tc.groups) {
			t.Errorf("/%s/ (flags %v) on %q: got index %d groups %q, want %d %q", tc.pattern, tc.flags, tc.input, index, got, tc.index, tc.groups)
		}
	}
}

func equalStrings(a, b []string) bool {
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

// A class of many strings must cost a step per character read, not one per
// candidate string: \p{RGI_Emoji} begins with any of 382 characters, and
// trying them in turn at every position exhausts the step budget long before
// the end of a few hundred kilobytes of text.
func TestUnicodeSets_StringsWithinBudget(t *testing.T) {
	re := ecma262.MustCompile(`\p{RGI_Emoji}`, flags.UnicodeSets)
	// The last filler shares its lead bytes with emoji, so the first-byte
	// prefilter cannot skip it; neither © nor ❤ is RGI_Emoji without U+FE0F.
	for _, filler := range []string{"The quick brown fox jumps over the lazy dog. ", "日本語のテキスト", "©x❤y"} {
		text := strings.Repeat(filler, 256<<10/len(filler)) + "\U0001F468‍\U0001F469‍\U0001F467"
		loc, err := re.FindStringIndexErr(text)
		want := len(text) - len("\U0001F468‍\U0001F469‍\U0001F467")
		if err != nil || loc == nil || loc[0] != want {
			t.Errorf("filler %q: got %v, %v; want a match at %d", filler, loc, err, want)
		}
	}
}
