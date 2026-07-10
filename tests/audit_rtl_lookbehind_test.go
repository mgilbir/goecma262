package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

// Right-to-left lookbehind: a quantified capture inside a lookbehind takes the
// LEFTMOST iteration's value (ECMA-262 evaluates the body right-to-left).
// Expected values verified against Node.
func TestAudit_LookbehindQuantifiedCapture(t *testing.T) {
	cases := []struct {
		pattern string
		input   string
		want    string // group 1
		f       flags.Flags
	}{
		{`(?<=(?<a>\w){3})f`, "abcdef", "c", flags.Unicode},
		{`(?<=(?<a>\w){4})f`, "abcdef", "b", flags.Unicode},
		{`(?<=(?<a>\w){3})f`, "abcdef", "c", flags.Flags(0)},
		{`(?<=(?<a>\w)+)f`, "abcdef", "a", flags.Unicode},
		{`(?<=(?<a>\w)+)f`, "abcdef", "a", flags.Flags(0)},
	}
	for _, tc := range cases {
		re := ecma262.MustCompile(tc.pattern, tc.f)
		m := re.FindStringSubmatch(tc.input)
		if m == nil {
			t.Errorf("%s: expected match", tc.pattern)
			continue
		}
		if m[1] != tc.want {
			t.Errorf("%s: group 1 = %q, want %q", tc.pattern, m[1], tc.want)
		}
	}
}

// A backreference to a group defined inside the same lookbehind is well-defined
// under right-to-left evaluation (the group is matched before the backref).
func TestAudit_LookbehindSelfBackref(t *testing.T) {
	re := ecma262.MustCompile(`(?<=\1(\w+))c`, flags.Flags(0))
	m := re.FindStringSubmatch("aac")
	if m == nil || m[0] != "c" || m[1] != "a" {
		t.Fatalf(`(?<=\1(\w+))c on "aac" = %q, want ["c" "a"]`, m)
	}
	if re.MatchString("abc") {
		t.Error(`(?<=\1(\w+))c should NOT match "abc"`)
	}
}

// Multiple groups in a lookbehind keep their natural left-to-right values.
func TestAudit_LookbehindGroupOrder(t *testing.T) {
	re := ecma262.MustCompile(`(?<=(a)(b))c`, flags.Flags(0))
	m := re.FindStringSubmatch("abc")
	if m == nil || m[1] != "a" || m[2] != "b" {
		t.Fatalf(`(?<=(a)(b))c groups = %q, want [c a b]`, m)
	}
}

// Regression guards: ordinary, variable-length, and negative lookbehinds still
// behave correctly after the right-to-left rewrite.
func TestAudit_LookbehindRegressions(t *testing.T) {
	if got := ecma262.MustCompile(`(?<=\$)\d+`, flags.Flags(0)).FindString("$100"); got != "100" {
		t.Errorf(`(?<=\$)\d+ = %q, want "100"`, got)
	}
	if !ecma262.MustCompile(`(?<=a+)b`, flags.Flags(0)).MatchString("aab") {
		t.Error(`(?<=a+)b should match "aab"`)
	}
	if !ecma262.MustCompile(`(?<!\$)\d+`, flags.Flags(0)).MatchString("€100") {
		t.Error(`(?<!\$)\d+ should match "€100"`)
	}
	if ecma262.MustCompile(`(?<=ab)c`, flags.Flags(0)).MatchString("xc") {
		t.Error(`(?<=ab)c should NOT match "xc"`)
	}
	if !ecma262.MustCompile(`(?<=abc)d`, flags.Flags(0)).MatchString("abcd") {
		t.Error(`(?<=abc)d should match "abcd"`)
	}
}
