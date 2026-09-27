package ecma262_test

import (
	"reflect"
	"testing"

	"github.com/mgilbir/goecma262"
)

// unset marks a capture group that did not participate in the match.
const unset = "\x00unset"

// TestRepeatMatcher pins ECMA-262 RepeatMatcher semantics: an iteration beyond
// the quantifier's minimum that matches the empty string fails (so /(a*)*/
// leaves its group unset, and /(?:a*?)*/ keeps looking for a longer
// iteration), and the groups inside the quantified atom are cleared at the
// start of each iteration (so a group reports its last participating
// iteration, and a backreference to it sees it unset after an iteration that
// did not set it). The first six rows are the spec's own examples. Expected
// results are V8's (Node.js 26.9.0); JavaScriptCore (Bun 1.3.0) agrees on
// every row.
func TestRepeatMatcher(t *testing.T) {
	cases := []struct {
		pattern, flags, input string
		want                  []string // exec result; unset for an undefined group; nil for no match
	}{
		{`(z)((a+)?(b+)?(c))*`, "", "zaacbbbcac", []string{"zaacbbbcac", "z", "ac", "a", unset, "c"}}, // spec: captures reset each iteration
		{`(a*)*`, "", "b", []string{"", unset}},                                                       // spec: empty iteration fails, group stays undefined
		{`(a*)b\1+`, "", "baaaac", []string{"b", ""}},                                                 // spec: \1+ of an empty group matches once
		{`(?=(a+))`, "", "baaabac", []string{"", "aaa"}},                                              // spec
		{`(?=(a+))a*b\1`, "", "baaabac", []string{"aba", "a"}},                                        // spec
		{`(.*?)a(?!(a+)b\2c)\2(.*)`, "", "baaabaac", []string{"baaabaac", "ba", unset, "abaac"}},      // spec
		{`()*`, "", "a", []string{"", unset}},
		{`()+`, "", "a", []string{"", ""}}, // required iteration may be empty
		{`()?`, "", "a", []string{"", unset}},
		{`(){0,3}`, "", "a", []string{"", unset}},
		{`(){2}`, "", "a", []string{"", ""}},
		{`(){1,3}`, "", "a", []string{"", ""}},
		{`()*?`, "", "a", []string{"", unset}},
		{`(a?)*`, "", "aab", []string{"aa", "a"}},
		{`(a?)+`, "", "b", []string{"", ""}},
		{`(a?){2,4}`, "", "ab", []string{"a", ""}},
		{`(?:a*?)*`, "", "aa", []string{"aa"}},
		{`(?:a*?)+`, "", "aa", []string{"aa"}},
		{`(?:a|)*b`, "", "aab", []string{"aab"}},
		{`(?:|a)*b`, "", "aab", []string{"aab"}},
		{`(?:|a)*`, "", "aa", []string{"aa"}},
		{`(?:|a)+`, "", "aa", []string{"aa"}},
		{`(?:|a){2,}`, "", "aa", []string{"aa"}},
		{`(?:a|)*?b`, "", "aab", []string{"aab"}},
		{`(a|)+b`, "", "aab", []string{"aab", "a"}},
		{`(|a)+b`, "", "aab", []string{"aab", "a"}},
		{`(?:\b)*x`, "", "ax", []string{"x"}},
		{`(?:\b)*?(?:s+)(([a-z]))`, "", "s\u00e9", nil}, // was a step-budget error
		{`(?:(?=a))*a`, "", "a", []string{"a"}},
		{`(?:$)*`, "", "a", []string{""}},
		{`(?:^)+a`, "m", "b\na", []string{"a"}},
		{`(a*)*b`, "", "aab", []string{"aab", "aa"}},
		{`(a*)+b`, "", "aab", []string{"aab", "aa"}},
		{`(a*?)*`, "", "aa", []string{"aa", "a"}},
		{`(?:(a)|b)*`, "", "ab", []string{"ab", unset}},
		{`(?:(a)|b)*`, "", "ba", []string{"ba", "a"}},
		{`(?:(a)|(b))+`, "", "abab", []string{"abab", unset, "b"}},
		{`(?:(a)|b){2}`, "", "ab", []string{"ab", unset}},
		{`(?:(a)|b){1,3}`, "", "abb", []string{"abb", unset}},
		{`((a)|b)+`, "", "ab", []string{"ab", "b", unset}},
		{`(?:(a)b|a(c))*`, "", "abac", []string{"abac", unset, "c"}},
		{`(?:(?:(a)|b)c)*`, "", "acbc", []string{"acbc", unset}},
		{`(?:(a)|\1)*`, "", "a", []string{"a", "a"}}, // an empty iteration after a capturing one
		{`(?:(a)|\1)*b`, "", "ab", []string{"ab", "a"}},
		{`(?:\1|(a))*`, "", "aa", []string{"aa", "a"}},
		{`(?:(a)|b)*\1`, "", "abx", []string{"ab", unset}},
		{`(?:(a)|b)*\1`, "", "aba", []string{"ab", unset}},
		{`(?:(a)|b\1)*`, "", "aba", []string{"aba", "a"}},
		{`(?:\1(a)|b)*`, "", "aab", []string{"aab", unset}},
		{`(?:(a)\1?|b)+`, "", "aab", []string{"aab", unset}},
		{`^(?:(a)|\1b)+$`, "", "ab", []string{"ab", unset}},
		{`(a)?(?:\1b)*`, "", "abab", []string{"a", "a"}},
		{`((a)|(b))*?c`, "", "abc", []string{"abc", "b", unset, "b"}},
		{`(?:(?:a*)*)*b`, "", "aab", []string{"aab"}},
		{`((a*)*)*`, "", "aa", []string{"aa", "aa", "aa"}},
		{`(?:(a*)?)*x`, "", "aax", []string{"aax", "aa"}},
		{`(?:(a)*)*`, "", "aa", []string{"aa", "a"}},
		{`(?:(?:(a)|b)*c)*`, "", "abcbc", []string{"abcbc", unset}},
		{`((a|b)*)*c`, "", "abc", []string{"abc", "ab", "b"}},
		{`(?:(?=(a)))*a`, "", "a", []string{"a", unset}},
		{`(?:(?=(a))a)*`, "", "aa", []string{"aa", "a"}},
		{`(?:a(?=(b)))*`, "", "abab", []string{"a", "b"}},
		{`(?:(?<=(a))b)*`, "", "abab", []string{"", unset}},
		{`(?<=(?:(a)|b)*)c`, "", "abc", []string{"c", "a"}}, // lookbehind: right to left
		{`(?<=(a*)*)b`, "", "aab", []string{"b", "aa"}},
		{`(?<=((a)|b)+)c`, "", "bac", []string{"c", "b", unset}},
		{`(a??){2}`, "", "a", []string{"", ""}},
		{`(a??)*`, "", "aa", []string{"aa", "a"}},
		{`(a{0,1}?)+`, "", "aa", []string{"aa", "a"}},
		{`(?:a??){2,3}b`, "", "ab", []string{"ab"}},
		{`(\B){0,2}a`, "", "ba", []string{"a", unset}},
	}
	for _, tc := range cases {
		re, err := ecma262.CompileFlags(tc.pattern, tc.flags)
		if err != nil {
			t.Errorf("/%s/%s: %v", tc.pattern, tc.flags, err)
			continue
		}
		idx, err := re.FindStringSubmatchIndexErr(tc.input)
		if err != nil {
			t.Errorf("/%s/%s on %+q: %v", tc.pattern, tc.flags, tc.input, err)
			continue
		}
		var got []string
		for i := 0; i+1 < len(idx); i += 2 {
			if idx[i] < 0 {
				got = append(got, unset)
			} else {
				got = append(got, tc.input[idx[i]:idx[i+1]])
			}
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("/%s/%s.exec(%+q) = %+q, want %+q", tc.pattern, tc.flags, tc.input, got, tc.want)
		}
	}
}

// TestRepeatMatcherWork bounds the steps some loops whose body can match
// empty take. The failure memo keys an iteration mark only by whether it
// equals the position (see vm.memoVisit), so such loops stay linear. Each
// budget is about 20% above what these take (1,775, 1,578 and 11 steps). The
// last pattern exhausted any budget before the empty check, cycling through
// its lazy \b loop. None of them matches (V8 agrees).
func TestRepeatMatcherWork(t *testing.T) {
	cases := []struct {
		pattern, flags, input string
		steps                 int
	}{
		{`(?:(?:a|a|(?:a|\b)*){2,}){1,3}((c+?){1,3})`, "u", "aaaa", 2150},
		{`(?:(?=c|b)|()*|(?:|.)+?){2,}(?:b){2}`, "", "ababaa", 1900},
		{`(?:\b)*?(?:s+)(([a-z]))`, "m", "sé", 100},
	}
	for _, tc := range cases {
		re := ecma262.MustCompile(tc.pattern, mustFlags(t, tc.flags))
		re.SetMaxSteps(tc.steps)
		got, err := re.FindStringSubmatchIndexErr(tc.input)
		if err != nil || got != nil {
			t.Errorf("/%s/%s on %+q within %d steps = %v, %v; want no match", tc.pattern, tc.flags, tc.input, tc.steps, got, err)
		}
	}
}
