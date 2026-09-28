package parser

import (
	"strings"
	"testing"
)

// Quantifier bounds saturate at maxBound, so their order must be decided from
// the digits. These are tested here rather than through Compile, which rejects
// any bound over its repetition limit and so cannot tell the two errors apart.
func TestQuantifierBoundsCompareExactly(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		ok      bool
	}{
		{`a{2000000001,2000000000}`, false},  // same length, both saturated
		{`a{20000000000,2000000000}`, false}, // different lengths, both saturated
		{`a{99999999999999999999,1}`, false},
		{`a{0002,1}`, false}, // leading zeros do not count as magnitude
		{`a{2000000000,2000000001}`, true},
		{`a{1,99999999999999999999}`, true},
		{`a{0002,2}`, true},
	} {
		_, err := New(tc.pattern, Flags{AnnexB: true}).Parse()
		if tc.ok && err != nil {
			t.Errorf("%s: unexpected error %v", tc.pattern, err)
		}
		if !tc.ok && (err == nil || !strings.Contains(err.Error(), "out of order")) {
			t.Errorf("%s: want out-of-order error, got %v", tc.pattern, err)
		}
	}
}
