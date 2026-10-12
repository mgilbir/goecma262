package ecma262_test

import (
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"testing"

	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/parser"
)

// parseShapes are the constructs that allocate the most per byte of source,
// each repeated to make a pattern.
var parseShapes = []struct{ shape, flags string }{
	{`a`, ""}, {`.`, ""}, {`|`, ""}, {`^$`, ""}, {`a*`, ""}, {`a??`, ""}, {`a{1,2}`, ""}, {`()`, ""}, {`(a)`, ""},
	{`(?:)`, ""}, {`(?=)`, ""}, {`(?<=)`, ""}, {`(?i:)`, ""}, {`(?i-m:a)`, ""}, {`[]`, ""}, {`[a]`, ""}, {`[a-z]`, ""},
	{`[\d]`, ""}, {`[a-]`, ""}, {`[\b]`, ""}, {`\d`, ""}, {`\b`, ""}, {`\1()`, ""}, {`\0`, ""}, {`\cA`, ""}, {`\x41`, ""},
	{`😀`, ""}, {`[😀]`, ""}, {`(?<n%d>)`, ""}, {`(?<n%d>)\k<n%d>`, ""}, {`(?<a%d>)|(?<a%d>)`, ""}, {`[aaaaaaaaaa]`, ""},
	{`a`, "u"}, {`\u{1F600}`, "u"}, {`[\u{1F600}]`, "u"}, {`\p{L}`, "u"}, {`[\p{L}]`, "u"}, {`\P{Script=Greek}`, "u"},
	{`[a]`, "v"}, {`[[a]]`, "v"}, {`[[[]]]`, "v"}, {`[a&&b]`, "v"}, {`[a--b]`, "v"}, {`[\q{}]`, "v"}, {`[\q{a}]`, "v"},
	{`[\q{ab}]`, "v"}, {`[\q{a|b|c}]`, "v"}, {`[\q{|||||}]`, "v"}, {`[\q{abcdefgh}]`, "v"}, {`\p{RGI_Emoji}`, "v"},
	{`[\p{RGI_Emoji}]`, "v"}, {`[ab]`, "v"}, {`[^a]`, "v"},
}

// parseAllocated returns the bytes New and Parse allocate for pattern.
func parseAllocated(pattern string, f parser.Flags) (int64, error) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	before := ms.TotalAlloc
	_, err := parser.New(pattern, f).Parse()
	runtime.ReadMemStats(&ms)
	return int64(ms.TotalAlloc - before), err
}

func parserFlags(fl string) parser.Flags {
	f, _ := flags.Parse(fl)
	u, v := f.Has(flags.Unicode), f.Has(flags.UnicodeSets)
	return parser.Flags{IgnoreCase: f.Has(flags.IgnoreCase), Unicode: u, UnicodeSets: v,
		DotAll: f.Has(flags.DotAll), Multiline: f.Has(flags.Multiline), AnnexB: !u && !v}
}

func checkParseAlloc(t *testing.T, pattern, fl string, worst *float64) {
	t.Helper()
	// A first parse builds the tables shared by every pattern (property
	// names, members of properties of strings), which are built once per
	// process; the second measures what a parse costs.
	parser.New(pattern, parserFlags(fl)).Parse()
	got, err := parseAllocated(pattern, parserFlags(fl))
	if bound := parser.AllocBound(len(pattern)); got > bound {
		t.Fatalf("parsing %d bytes (/%.40s/%s, err %v) allocated %d, over AllocBound %d", len(pattern), pattern, fl, err, got, bound)
	}
	if per := float64(got) / float64(max(len(pattern), 1)); len(pattern) >= 1000 && per > *worst {
		*worst = per
	}
}

func TestParseAllocBound(t *testing.T) {
	var worst float64
	for _, sh := range parseShapes {
		for _, n := range []int{1, 2, 3, 5, 8, 50, 300, 5000} {
			var sb strings.Builder
			for i := 0; i < n; i++ {
				if strings.Contains(sh.shape, "%d") {
					fmt.Fprintf(&sb, strings.ReplaceAll(sh.shape, "%d", "%[1]d"), i)
				} else {
					sb.WriteString(sh.shape)
				}
			}
			checkParseAlloc(t, sb.String(), sh.flags, &worst)
		}
	}
	for _, fl := range []string{"", "i", "u", "v"} {
		rng := rand.New(rand.NewSource(int64(len(fl)) + 99))
		for i := 0; i < 500; i++ {
			g := &patternGen{rng: rng, u: fl == "u", v: fl == "v"}
			checkParseAlloc(t, g.disjunction(3), fl, &worst)
		}
	}
	// Invalid patterns fail part way, having allocated less.
	for _, p := range []string{strings.Repeat("(", 300), strings.Repeat("a", 5000) + "(", "[" + strings.Repeat("a", 5000)} {
		checkParseAlloc(t, p, "", &worst)
	}
	t.Logf("at most %.1f bytes per byte of patterns of 1000 bytes or more", worst)
}
