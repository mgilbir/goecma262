package flags

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// parseWithMap is Parse as it was, with a map of the characters seen, kept
// as the reference for its results.
func parseWithMap(s string) (Flags, error) {
	var f Flags
	seen := map[rune]bool{}
	for _, c := range s {
		if seen[c] {
			return 0, &DuplicateFlagError{Flag: c}
		}
		seen[c] = true
		switch c {
		case 'i':
			f |= IgnoreCase
		case 'g':
			f |= Global
		case 'm':
			f |= Multiline
		case 's':
			f |= DotAll
		case 'u':
			f |= Unicode
		case 'v':
			f |= UnicodeSets
		case 'y':
			f |= Sticky
		case 'd':
			f |= HasIndices
		default:
			return 0, &InvalidFlagError{Flag: c}
		}
	}
	if f&Unicode != 0 && f&UnicodeSets != 0 {
		return 0, &IncompatibleFlagsError{Flags: "u and v"}
	}
	return f, nil
}

func checkLikeReference(t *testing.T, s string) {
	t.Helper()
	got, gerr := Parse(s)
	want, werr := parseWithMap(s)
	if got != want || !reflect.DeepEqual(gerr, werr) {
		t.Fatalf("Parse(%q) = %v, %v; want %v, %v", s, got, gerr, want, werr)
	}
}

// Every string of up to five characters from the flags, a non-flag, an
// upper-case flag and invalid UTF-8.
func TestParseMatchesReferenceExhaustively(t *testing.T) {
	alphabet := []string{"d", "g", "i", "m", "s", "u", "v", "y", "x", "I", "\xff", "é"}
	var gen func(prefix string, n int)
	gen = func(prefix string, n int) {
		checkLikeReference(t, prefix)
		if n == 0 {
			return
		}
		for _, a := range alphabet {
			gen(prefix+a, n-1)
		}
	}
	gen("", 5)
}

func TestParseMatchesReferenceOnLongStrings(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		var sb strings.Builder
		for n := rng.Intn(40); n > 0; n-- {
			sb.WriteByte("dgimsuvyx"[rng.Intn(9)])
		}
		checkLikeReference(t, sb.String())
	}
}

// A long flags string from untrusted input costs nothing in proportion to
// its length: on v0.3.0, 384 KiB of x allocated 4.9 MB.
func TestParseAllocatesNothingForItsInput(t *testing.T) {
	cases := map[string]int{
		strings.Repeat("x", 384<<10):            1, // the error
		"dgimsy" + strings.Repeat("g", 384<<10): 1,
		"dgimsuy":                               0,
		strings.Repeat("é", 1<<10):              1,
	}
	for s, want := range cases {
		if got := testing.AllocsPerRun(10, func() { Parse(s) }); got > float64(want) {
			t.Errorf("Parse of %d bytes (%.10q…): %.0f allocations, want at most %d", len(s), s, got, want)
		}
	}
}
