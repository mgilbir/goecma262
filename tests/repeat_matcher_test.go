package ecma262_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// An iteration taken after a quantifier's minimum is met fails if it matches
// the empty string (ECMA-262 RepeatMatcher: "if min = 0 and y's endIndex =
// x's endIndex, return failure"), so the matcher backtracks into the body for
// a longer alternative or stops iterating — and the captures of the rejected
// iteration are undone with it. Mandatory iterations may be empty.
//
// Every expectation is node's answer.
func TestRepeatMatcherEmptyIteration(t *testing.T) {
	runJSCases(t, []jsCase{
		// The empty optional iteration is rejected, so the group never
		// participates.
		{`(c{0,2})*`, "", "", 0, []string{"", undef}},
		{`(a*)?`, "", "b", 0, []string{"", undef}},
		{`(a*)*`, "", "b", 0, []string{"", undef}},
		{`(\b)*x`, "", "x", 0, []string{"x", undef}},
		{`(?:(?=(a)))*a`, "", "a", 0, []string{"a", undef}},
		{`(?:a|())*?b`, "", "aab", 0, []string{"aab", undef}},
		{`(?:(a)|b|())*c`, "", "abc", 0, []string{"abc", undef, undef}},
		// A mandatory iteration may be empty.
		{`(a*)+`, "", "b", 0, []string{"", ""}},
		{`(a*){2,3}`, "", "b", 0, []string{"", ""}},
		{`(a?){2,3}`, "", "", 0, []string{"", ""}},
		// Rejecting the empty iteration makes the body try a longer path.
		{`([^\w]|\t*?){0,2}`, "su", "\t0", 0, []string{"\t", "\t"}},
		{`(\w??|_)*`, "i", "aKc", 0, []string{"aKc", "c"}},
		{`((.*?|_{1,3}?)??|a{2}){1,2}`, "", "xyz", 0, []string{"x", "x", "x"}},
		{`(a|b*?)+c`, "", "abbc", 0, []string{"abbc", "b"}},
		{`(a{0,2}){1,3}?c`, "", "aac", 0, []string{"aac", "aa"}},
		// Captures reset at every iteration; the last one's survive.
		{`(z)((a+)?(b+)?(c))*`, "", "zaacbbbcac", 0, []string{"zaacbbbcac", "z", "ac", "a", undef, "c"}},
		{`((a)|b)+`, "", "ab", 0, []string{"ab", "b", undef}},
		{`(?:x*(y)?)*z`, "", "xyxz", 0, []string{"xyxz", undef}},
		// Backtracking into an earlier iteration restores that iteration's
		// start: the empty alternative is still rejected there.
		{`(?:a|())*(?<!a)`, "", "ab", 0, []string{"", undef}},
		{`(?:b|a|())*(?<!a)`, "", "ba", 0, []string{"b", undef}},
		// A + starts with a mandatory iteration even when an earlier run of the
		// same code left a start position behind: here the outer loop runs
		// the lookahead twice at 0, and the second time the inner + must
		// still accept its empty first iteration.
		{`(?:(?=(a*)+)){2}`, "", "", 0, []string{"", ""}},
		{`(?:(?=(a*)+)x??)+`, "", "x", 0, []string{"x", ""}},
		// Found by the differential fuzzer. The search visits the same loop
		// state once with the iteration still empty and once after progress;
		// only the second may continue, so the two must not be conflated.
		{`(((\S{2}|\D{0,2})??|[ab]??)+|\.*){1,3}?`, "", "\t_#a", 0, []string{"\t_#a", "\t_#a", "#a", "#a"}},
		{`((\w*?|[ab]{1,2})+|([^a]{2}|\w)+?){1,3}?(?<!\n{1,2})`, "su", "x- ca1", 0, []string{"x", "x", "x", undef}},
		{`(((_*?|\W??))*?|c)+`, "s", "#!!ya", 0, []string{"#!!", "!", "!", "!"}},
		// The same rule holds right to left, inside a lookbehind.
		{`(?<=(a*)*)b`, "", "b", 0, []string{"b", undef}},
		{`(?<=(?:(a)|())*)b`, "", "ab", 1, []string{"b", "a", undef}},
	})
}

// Loops whose body can match empty carry a progress check, and the failure
// memo keys on it. Keying on the iteration's start position itself would make
// the states quadratic in the input, which exhausts the default budget here.
func TestRepeatMatcherEmptyLoopsStayLinear(t *testing.T) {
	long := strings.Repeat("x", 100_000)
	for _, c := range []struct {
		pattern, input string
		want           bool
	}{
		{`^(?:x*)*y`, long, false},
		{`^(?:x*)*$`, long, true},
		{`^(?:x|y?)*$`, long, true},
		{`^(x?)*$`, long, true},
		{`(?:x*|y)*z`, long, false},
	} {
		re := ecma262.MustCompile(c.pattern, flags.Flags(0))
		got, err := re.MatchStringErr(c.input)
		if err != nil || got != c.want {
			t.Errorf("/%s/ on %d bytes = (%v, %v), want (%v, nil)", c.pattern, len(c.input), got, err, c.want)
		}
	}
}

// A loop inside a lookbehind records where its iterations start. That record
// belongs to the lookbehind body: carried out with the captures, it survives
// into the next evaluation of the same body, where a + inside it then rejects
// a first iteration that is allowed to be empty. Found by a search comparing
// the two behaviours; the expectation is node's.
func TestRepeatMatcherMarksStayInsideLookaround(t *testing.T) {
	re := ecma262.MustCompile(`(?:(?<=(?:^|a)+(?:$|.)+?)a??|(?:$)*?)+`, flags.Global)
	got := fmt.Sprint(re.FindAllStringIndex("abba", -1))
	if want := "[[0 0] [1 1] [2 2] [3 4] [4 4]]"; got != want {
		t.Errorf("FindAllStringIndex = %s, want %s", got, want)
	}
}
