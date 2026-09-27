package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
)

// TestUnicodeCaseInsensitiveClassEscapes pins case-insensitive matching of
// \w, \W, \b, \B and \p{..}/\P{..} in Unicode mode. A class matches a
// character when some member has the same simple case folding (ECMA-262
// CharacterSetMatcher with Canonicalize), so under i with u or v:
//   - \w also matches U+017F (ſ, folds to s) and U+212A (KELVIN SIGN, folds
//     to k), \W does not, and \b and \B treat them as word characters
//     (WordCharacters adds the characters that fold into [A-Za-z0-9_]);
//   - \p{Lu} matches a, and \P{Lu} matches A (under u \P is the plain
//     complement, matched with folding), while under v \P{Lu} is the
//     complement of the case-folded set and matches neither.
//
// Without i, or without u and v, nothing changes. Expected values are V8's
// (Node.js 26.9.0), which implements these rules.
func TestUnicodeCaseInsensitiveClassEscapes(t *testing.T) {
	cases := []struct {
		pattern, flags, input string
		match                 bool
	}{
		{`\w`, "iu", "\u017f", true},
		{`\w`, "iu", "\u212a", true},
		{`\w`, "iu", "K", true},
		{`\w`, "iu", "s", true},
		{`\w`, "iu", "!", false},
		{`\W`, "iu", "\u017f", false},
		{`\W`, "iu", "\u212a", false},
		{`\W`, "iu", "K", false},
		{`\W`, "iu", "s", false},
		{`\W`, "iu", "!", true},
		{`\w`, "iv", "\u017f", true},
		{`\w`, "iv", "\u212a", true},
		{`\w`, "iv", "K", true},
		{`\w`, "iv", "s", true},
		{`\w`, "iv", "!", false},
		{`\W`, "iv", "\u017f", false},
		{`\W`, "iv", "\u212a", false},
		{`\W`, "iv", "K", false},
		{`\W`, "iv", "s", false},
		{`\W`, "iv", "!", true},
		{`[\w]`, "iu", "\u017f", true},
		{`[\w]`, "iu", "\u212a", true},
		{`[\w]`, "iu", "K", true},
		{`[\W]`, "iu", "\u017f", false},
		{`[\W]`, "iu", "\u212a", false},
		{`[\W]`, "iu", "K", false},
		{`[\W]`, "iu", "!", true},
		{`[^\W]`, "iu", "\u017f", true},
		{`[^\W]`, "iu", "\u212a", true},
		{`[^\W]`, "iu", "K", true},
		{`[^\W]`, "iu", "!", false},
		{`\bs`, "iu", "\u017fs", true},
		{`\bs`, "iu", " s", true},
		{`\Bs`, "iu", "\u017fs", true},
		{`\Bs`, "iu", " s", false},
		{`\w`, "i", "\u017f", false},
		{`\w`, "i", "\u212a", false},
		{`\w`, "i", "K", true},
		{`\p{Lu}`, "iu", "a", true},
		{`\p{Lu}`, "iu", "A", true},
		{`\p{Lu}`, "iu", "1", false},
		{`\P{Lu}`, "iu", "a", true},
		{`\P{Lu}`, "iu", "A", true},
		{`\P{Lu}`, "iu", "1", true},
		{`\p{Lu}`, "iv", "a", true},
		{`\p{Lu}`, "iv", "A", true},
		{`\p{Lu}`, "iv", "1", false},
		{`\P{Lu}`, "iv", "a", false},
		{`\P{Lu}`, "iv", "A", false},
		{`\P{Lu}`, "iv", "1", true},
		{`\P{Ll}`, "iu", "a", true},
		{`\P{Ll}`, "iu", "A", true},
		{`\P{Ll}`, "iu", "1", true},
		{`\P{Ll}`, "iv", "a", false},
		{`\P{Ll}`, "iv", "A", false},
		{`\P{Ll}`, "iv", "1", true},
		{`[^\P{Ll}]`, "iu", "a", false},
		{`[^\P{Ll}]`, "iu", "A", false},
		{`[^\P{Ll}]`, "iu", "1", false},
		{`[^\P{Ll}]`, "iv", "a", true},
		{`[^\P{Ll}]`, "iv", "A", true},
		{`[^\P{Ll}]`, "iv", "1", false},
		{`[\P{Ll}x]`, "iv", "a", false},
		{`[\P{Ll}x]`, "iv", "A", false},
		{`[\P{Ll}x]`, "iv", "1", true},
		{`[\P{Ll}x]`, "iv", "x", true},
		{`[\P{Ll}x]`, "iv", "X", true},
		{`[\P{Ll}x]`, "iu", "a", true},
		{`[\P{Ll}x]`, "iu", "A", true},
		{`[\P{Ll}x]`, "iu", "1", true},
		{`[\P{Ll}x]`, "iu", "x", true},
		{`[\P{Ll}x]`, "iu", "X", true},
		{`\p{Lowercase}`, "iu", "A", true},
		{`\p{Lowercase}`, "iu", "\u017f", true},
		{`\p{Lowercase}`, "iu", "\u212a", true},
		{`\p{Lowercase}`, "iu", "K", true},
		{`\P{Lowercase}`, "iu", "a", true},
		{`\P{Lowercase}`, "iu", "\u017f", true},
		{`\P{Lowercase}`, "iu", "\u212a", true},
		{`\P{Lowercase}`, "iu", "K", true},
		{`\P{Lowercase}`, "iv", "a", false},
		{`\P{Lowercase}`, "iv", "\u017f", false},
		{`\P{Lowercase}`, "iv", "\u212a", false},
		{`\P{Lowercase}`, "iv", "K", false},
	}
	for _, tc := range cases {
		re, err := ecma262.CompileFlags(tc.pattern, tc.flags)
		if err != nil {
			t.Errorf("/%s/%s: %v", tc.pattern, tc.flags, err)
			continue
		}
		if got := re.MatchString(tc.input); got != tc.match {
			t.Errorf("/%s/%s.MatchString(%+q) = %v, want %v", tc.pattern, tc.flags, tc.input, got, tc.match)
		}
	}
}
