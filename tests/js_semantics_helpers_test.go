package ecma262_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/mgilbir/goecma262"
)

// undef stands for a capture that did not participate in the match, which
// JavaScript reports as undefined and FindStringSubmatch as "".
const undef = "\x00undefined"

// jsCase is one RegExp.prototype.exec call from lastIndex 0, with the result
// node gives for it: the match index as a UTF-16 index and every capture, or
// a nil want for no match.
type jsCase struct {
	pattern, flags, input string
	index                 int
	want                  []string
}

// execJS returns the match as JavaScript reports it: the UTF-16 index of the
// match and each capture, undef for one that did not participate.
func execJS(re *ecma262.Regexp, input string) (int, []string, error) {
	loc, err := re.FindStringSubmatchIndexErr(input)
	if err != nil || loc == nil {
		return 0, nil, err
	}
	groups := make([]string, len(loc)/2)
	for i := range groups {
		if loc[2*i] < 0 {
			groups[i] = undef
		} else {
			groups[i] = input[loc[2*i]:loc[2*i+1]]
		}
	}
	return len(utf16.Encode([]rune(input[:loc[0]]))), groups, nil
}

func showGroups(g []string) string {
	if g == nil {
		return "null"
	}
	parts := make([]string, len(g))
	for i, s := range g {
		if s == undef {
			parts[i] = "undefined"
		} else {
			parts[i] = fmt.Sprintf("%q", s)
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func runJSCases(t *testing.T, cases []jsCase) {
	t.Helper()
	for _, c := range cases {
		re, err := ecma262.CompileFlags(c.pattern, c.flags)
		if err != nil {
			t.Errorf("/%s/%s: compile error: %v", c.pattern, c.flags, err)
			continue
		}
		idx, got, err := execJS(re, c.input)
		if err != nil {
			t.Errorf("/%s/%s on %q: %v", c.pattern, c.flags, c.input, err)
			continue
		}
		if showGroups(got) != showGroups(c.want) || (got != nil && idx != c.index) {
			t.Errorf("/%s/%s on %q = %s at %d, want %s at %d",
				c.pattern, c.flags, c.input, showGroups(got), idx, showGroups(c.want), c.index)
		}
	}
}
