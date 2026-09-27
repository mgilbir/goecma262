package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// Modifier groups, (?ims-ims:...). Every expectation was taken from node (V8).
// The flags apply to exactly the instructions inside the group: a
// backreference outside (?i:...) stays case-sensitive, (?m:^) keeps a pattern
// from being anchored, and lookbehind bodies and v-mode class sets inside a
// group follow its flags.

func TestModifiers_Syntax(t *testing.T) {
	cases := []struct {
		pattern string
		flags   flags.Flags
		valid   bool
	}{
		{`(?i:a)`, flags.Flags(0), true},
		{`(?i:a)`, flags.Unicode, true},
		{`(?i:a)`, flags.UnicodeSets, true},
		{`(?-i:a)`, flags.Flags(0), true},
		{`(?-i:a)`, flags.Unicode, true},
		{`(?-i:a)`, flags.UnicodeSets, true},
		{`(?i-:a)`, flags.Flags(0), true},
		{`(?i-:a)`, flags.Unicode, true},
		{`(?i-:a)`, flags.UnicodeSets, true},
		{`(?-:a)`, flags.Flags(0), false},
		{`(?-:a)`, flags.Unicode, false},
		{`(?-:a)`, flags.UnicodeSets, false},
		{`(?ii:a)`, flags.Flags(0), false},
		{`(?ii:a)`, flags.Unicode, false},
		{`(?ii:a)`, flags.UnicodeSets, false},
		{`(?i-i:a)`, flags.Flags(0), false},
		{`(?i-i:a)`, flags.Unicode, false},
		{`(?i-i:a)`, flags.UnicodeSets, false},
		{`(?im-s:a)`, flags.Flags(0), true},
		{`(?im-s:a)`, flags.Unicode, true},
		{`(?im-s:a)`, flags.UnicodeSets, true},
		{`(?i-mi:a)`, flags.Flags(0), false},
		{`(?i-mi:a)`, flags.Unicode, false},
		{`(?i-mi:a)`, flags.UnicodeSets, false},
		{`(?i)`, flags.Flags(0), false},
		{`(?i)`, flags.Unicode, false},
		{`(?i)`, flags.UnicodeSets, false},
		{`(?-i)a`, flags.Flags(0), false},
		{`(?-i)a`, flags.Unicode, false},
		{`(?-i)a`, flags.UnicodeSets, false},
		{`(?u:a)`, flags.Flags(0), false},
		{`(?u:a)`, flags.Unicode, false},
		{`(?u:a)`, flags.UnicodeSets, false},
		{`(?I:a)`, flags.Flags(0), false},
		{`(?I:a)`, flags.Unicode, false},
		{`(?I:a)`, flags.UnicodeSets, false},
		{`(?g:a)`, flags.Flags(0), false},
		{`(?g:a)`, flags.Unicode, false},
		{`(?g:a)`, flags.UnicodeSets, false},
		{`(?i:a)*`, flags.Flags(0), true},
		{`(?i:a)*`, flags.Unicode, true},
		{`(?i:a)*`, flags.UnicodeSets, true},
		{`(?i:a`, flags.Flags(0), false},
		{`(?i:a`, flags.Unicode, false},
		{`(?i:a`, flags.UnicodeSets, false},
		{`(?smi:a)`, flags.Flags(0), true},
		{`(?smi:a)`, flags.Unicode, true},
		{`(?smi:a)`, flags.UnicodeSets, true},
		{`(?-sm:a)`, flags.Flags(0), true},
		{`(?-sm:a)`, flags.Unicode, true},
		{`(?-sm:a)`, flags.UnicodeSets, true},
		{`(?i-m-s:a)`, flags.Flags(0), false},
		{`(?i-m-s:a)`, flags.Unicode, false},
		{`(?i-m-s:a)`, flags.UnicodeSets, false},
		{`(?i-ms-:a)`, flags.Flags(0), false},
		{`(?i-ms-:a)`, flags.Unicode, false},
		{`(?i-ms-:a)`, flags.UnicodeSets, false},
		{`(?i:)`, flags.Flags(0), true},
		{`(?i:)`, flags.Unicode, true},
		{`(?i:)`, flags.UnicodeSets, true},
		{`(?i:a|b)+?`, flags.Flags(0), true},
		{`(?i:a|b)+?`, flags.Unicode, true},
		{`(?i:a|b)+?`, flags.UnicodeSets, true},
		{`(?i:(?<n>a))\k<n>`, flags.Flags(0), true},
		{`(?i:(?<n>a))\k<n>`, flags.Unicode, true},
		{`(?i:(?<n>a))\k<n>`, flags.UnicodeSets, true},
		{`(?i:(?<n>a))(?<n>b)`, flags.Flags(0), false},
		{`(?i:(?<n>a))(?<n>b)`, flags.Unicode, false},
		{`(?i:(?<n>a))(?<n>b)`, flags.UnicodeSets, false},
		{`(?i:(?<n>a))|(?<n>b)`, flags.Flags(0), true},
		{`(?i:(?<n>a))|(?<n>b)`, flags.Unicode, true},
		{`(?i:(?<n>a))|(?<n>b)`, flags.UnicodeSets, true},
		{`(?=(?i:a))`, flags.Flags(0), true},
		{`(?=(?i:a))`, flags.Unicode, true},
		{`(?=(?i:a))`, flags.UnicodeSets, true},
		{`(?-`, flags.Flags(0), false},
		{`(?-`, flags.Unicode, false},
		{`(?-`, flags.UnicodeSets, false},
		{`(?i-`, flags.Flags(0), false},
		{`(?i-`, flags.Unicode, false},
		{`(?i-`, flags.UnicodeSets, false},
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

func TestModifiers_Exec(t *testing.T) {
	cases := []struct {
		pattern string
		flags   flags.Flags
		input   string
		index   int      // byte offset of the match, or -1
		groups  []string // "<unset>" for a group that did not participate
	}{
		{`(?i:a)b`, flags.Flags(0), "Ab AB", 0, []string{"Ab"}},
		{`(?i:a)b`, flags.Flags(0), "AB", -1, nil},
		{`(?-i:a)b`, flags.IgnoreCase, "AB aB", 3, []string{"aB"}},
		{`a(?-i:b)`, flags.IgnoreCase, "AB Ab", 3, []string{"Ab"}},
		{`(?i:a(?-i:b)c)`, flags.Flags(0), "ABC AbC", 4, []string{"AbC"}},
		{`(?i-s:a.)`, flags.DotAll, "A\u000aAx", 2, []string{"Ax"}},
		{`(?s:.)`, flags.Flags(0), "\u000a", 0, []string{"\u000a"}},
		{`(?-s:.)`, flags.DotAll, "\u000ax", 1, []string{"x"}},
		{`(?m:^b)`, flags.Flags(0), "a\u000ab", 2, []string{"b"}},
		{`(?-m:^b)`, flags.Multiline, "a\u000ab", -1, nil},
		{`(?m:a$)`, flags.Flags(0), "a\u000ab", 0, []string{"a"}},
		{`(?-m:a$)`, flags.Multiline, "a\u000ab", -1, nil},
		{`(?m:^)x`, flags.Flags(0), "a\u000ax", 2, []string{"x"}},
		{`^(?m:x)`, flags.Flags(0), "a\u000ax", -1, nil},
		{`(a)(?i:\1)`, flags.Flags(0), "aA", 0, []string{"aA", "a"}},
		{`(a)(?-i:\1)`, flags.IgnoreCase, "aA aa", 3, []string{"aa", "a"}},
		{`(?i:(a))\1`, flags.Flags(0), "Aa AA", 3, []string{"AA", "A"}},
		{`(?i:[a-c]+)`, flags.Flags(0), "xAbC", 1, []string{"AbC"}},
		{`(?i:[^a])`, flags.Flags(0), "A b", 1, []string{" "}},
		{`(?-i:[a-c]+)`, flags.IgnoreCase, "ABcd", 2, []string{"c"}},
		{`(?i:\w)`, flags.Unicode, "\u017f", 0, []string{"\u017f"}},
		{`(?i:\W)`, flags.Unicode, "\u017f!", 2, []string{"!"}},
		{`(?i:\b)`, flags.Unicode, " \u017f", 1, []string{""}},
		{`(?-i:\w)`, flags.Unicode | flags.IgnoreCase, "\u017fx", 2, []string{"x"}},
		{`(?i:\p{Lu})`, flags.Unicode, "a", 0, []string{"a"}},
		{`(?i:\P{Lu})`, flags.Unicode, "A", 0, []string{"A"}},
		{`(?i:\P{Lu})`, flags.UnicodeSets, "A", -1, nil},
		{`(?i:[^a])`, flags.UnicodeSets, "A b", 1, []string{" "}},
		{`(?i:[\q{ab}])`, flags.UnicodeSets, "xAB", 1, []string{"AB"}},
		{`(?-i:[\q{ab}])`, flags.UnicodeSets | flags.IgnoreCase, "AB ab", 3, []string{"ab"}},
		{`(?i:\p{RGI_Emoji_Flag_Sequence}|k)`, flags.UnicodeSets, "\u212a", 0, []string{"\u212a"}},
		{`(?i:[\q{abc|ab}])`, flags.UnicodeSets, "ABC", 0, []string{"ABC"}},
		{`(?i:k)`, flags.Unicode, "\u212a", 0, []string{"\u212a"}},
		{`(?i:k)`, flags.Flags(0), "\u212a", -1, nil},
		{`(?i:k+)x`, flags.Unicode, "\u212akKx", 0, []string{"\u212akKx"}},
		{`(?<=(?i:a))b`, flags.Flags(0), "Ab", 1, []string{"b"}},
		{`(?<=(?i:(a)))b`, flags.Flags(0), "Ab", 1, []string{"b", "A"}},
		{`(?<!(?i:a))b`, flags.Flags(0), "Ab cb", 4, []string{"b"}},
		{`(?<=(?-i:a))b`, flags.IgnoreCase, "Ab ab", 4, []string{"b"}},
		{`(?i:a)*b`, flags.Flags(0), "aAaB aAab", 5, []string{"aAab"}},
		{`(?i:a|b){2}`, flags.Flags(0), "xBA", 1, []string{"BA"}},
		{`(?i:(?<n>a))\k<n>`, flags.Flags(0), "Aa", -1, nil},
		{`((?i:a)|b)+`, flags.Flags(0), "AbA", 0, []string{"AbA", "A"}},
		{`(?i:x(?-i:y(?i:z)))`, flags.Flags(0), "XyZ XYZ", 0, []string{"XyZ"}},
		{`(?m:(?s:^.$))`, flags.Flags(0), "a\u000a\u000ab", 0, []string{"a"}},
		{`(?ims:^A.$)`, flags.Flags(0), "x\u000aa\u000a", 2, []string{"a\u000a"}},
		{`(?:(?i:(a))|b)+`, flags.Flags(0), "Ab", 0, []string{"Ab", "<unset>"}},
		{`(?<=(?i:ab))c`, flags.Flags(0), "ABc xbc", 2, []string{"c"}},
		{`(?<=(?i:(\w)+))x`, flags.Flags(0), "ABx", 2, []string{"x", "A"}},
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

// Group names declared inside a modifier group are named groups like any other.
func TestModifiers_GroupNames(t *testing.T) {
	re := ecma262.MustCompile(`(?i:(?<n>a))(?<m>b)`, flags.Flags(0))
	names := re.SubexpNames()
	if len(names) != 3 || names[1] != "n" || names[2] != "m" {
		t.Errorf("SubexpNames = %q, want [\"\" \"n\" \"m\"]", names)
	}
	if got := re.FindStringSubmatch("Ab"); len(got) != 3 || got[re.SubexpIndex("n")] != "A" {
		t.Errorf("FindStringSubmatch = %q, want group n = \"A\"", got)
	}
}
