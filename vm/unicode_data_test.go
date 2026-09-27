package vm

import (
	"strings"
	"testing"
	"unicode"
)

// The generated emoji data must describe the same Unicode as Go's character
// tables, which every other property comes from.
func TestEmojiDataVersion(t *testing.T) {
	if !strings.HasPrefix(unicode.Version, emojiVersion+".") {
		t.Errorf("emoji data is Emoji %s but unicode.Version is %s: regenerate with tools/genemoji", emojiVersion, unicode.Version)
	}
}

// inRanges binary-searches, so each table must be sorted and disjoint.
func TestEmojiPropertyRangesSorted(t *testing.T) {
	for name, rs := range emojiProperties {
		for i, r := range rs {
			if r.Start > r.End || i > 0 && r.Start <= rs[i-1].End+1 {
				t.Errorf("%s: range %d (%X-%X) not sorted, disjoint and non-adjacent", name, i, r.Start, r.End)
			}
		}
	}
}

// The property names must come from the Unicode version of Go's tables.
func TestPropertyNamesVersion(t *testing.T) {
	if propertyNamesVersion != unicode.Version {
		t.Errorf("property names are Unicode %s but unicode.Version is %s: regenerate with tools/genpropnames", propertyNamesVersion, unicode.Version)
	}
}

func TestUCDTablesSorted(t *testing.T) {
	check := func(name string, rs []RuneRange) {
		for i, r := range rs {
			if r.Start > r.End || i > 0 && r.Start <= rs[i-1].End+1 {
				t.Errorf("%s: range %d (%X-%X) not sorted, disjoint and non-adjacent", name, i, r.Start, r.End)
			}
		}
	}
	for name, rs := range ucdProperties {
		check(name, rs)
	}
	for name, rs := range scriptExtensions {
		check("scx="+name, rs)
	}
	check("scriptExtensionsListed", scriptExtensionsListed)
}
