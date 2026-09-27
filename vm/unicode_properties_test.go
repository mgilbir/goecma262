package vm

import (
	"slices"
	"testing"
)

// specBinaryProperties is ECMA-262's table of binary Unicode properties
// (2025, "Binary Unicode property aliases"): property name, then alias.
var specBinaryProperties = [][2]string{
	{"ASCII", "ASCII"},
	{"ASCII_Hex_Digit", "AHex"},
	{"Alphabetic", "Alpha"},
	{"Any", "Any"},
	{"Assigned", "Assigned"},
	{"Bidi_Control", "Bidi_C"},
	{"Bidi_Mirrored", "Bidi_M"},
	{"Case_Ignorable", "CI"},
	{"Cased", "Cased"},
	{"Changes_When_Casefolded", "CWCF"},
	{"Changes_When_Casemapped", "CWCM"},
	{"Changes_When_Lowercased", "CWL"},
	{"Changes_When_NFKC_Casefolded", "CWKCF"},
	{"Changes_When_Titlecased", "CWT"},
	{"Changes_When_Uppercased", "CWU"},
	{"Dash", "Dash"},
	{"Default_Ignorable_Code_Point", "DI"},
	{"Deprecated", "Dep"},
	{"Diacritic", "Dia"},
	{"Emoji", "Emoji"},
	{"Emoji_Component", "EComp"},
	{"Emoji_Modifier", "EMod"},
	{"Emoji_Modifier_Base", "EBase"},
	{"Emoji_Presentation", "EPres"},
	{"Extended_Pictographic", "ExtPict"},
	{"Extender", "Ext"},
	{"Grapheme_Base", "Gr_Base"},
	{"Grapheme_Extend", "Gr_Ext"},
	{"Hex_Digit", "Hex"},
	{"IDS_Binary_Operator", "IDSB"},
	{"IDS_Trinary_Operator", "IDST"},
	{"ID_Continue", "IDC"},
	{"ID_Start", "IDS"},
	{"Ideographic", "Ideo"},
	{"Join_Control", "Join_C"},
	{"Logical_Order_Exception", "LOE"},
	{"Lowercase", "Lower"},
	{"Math", "Math"},
	{"Noncharacter_Code_Point", "NChar"},
	{"Pattern_Syntax", "Pat_Syn"},
	{"Pattern_White_Space", "Pat_WS"},
	{"Quotation_Mark", "QMark"},
	{"Radical", "Radical"},
	{"Regional_Indicator", "RI"},
	{"Sentence_Terminal", "STerm"},
	{"Soft_Dotted", "SD"},
	{"Terminal_Punctuation", "Term"},
	{"Unified_Ideograph", "UIdeo"},
	{"Uppercase", "Upper"},
	{"Variation_Selector", "VS"},
	{"White_Space", "space"},
	{"XID_Continue", "XIDC"},
	{"XID_Start", "XIDS"},
}

// TestBinaryPropertyNamesMatchSpec checks that exactly the names and aliases
// of the spec's table resolve to a binary property, except those listed as
// unsupported, which must not resolve.
func TestBinaryPropertyNamesMatchSpec(t *testing.T) {
	names := map[string]bool{}
	for _, p := range specBinaryProperties {
		names[p[0]], names[p[1]] = true, true
		unsupported := slices.Contains(unsupportedBinaryProperties, p[0])
		for _, n := range p {
			if _, ok := binaryProperty(n); ok == unsupported {
				t.Errorf("binaryProperty(%q) ok = %v; %s is unsupported: %v", n, ok, p[0], unsupported)
			}
		}
	}
	for n := range binaryProperties {
		if !names[n] {
			t.Errorf("binaryProperties has %q, which is not in the spec's table", n)
		}
	}
	for a, n := range binaryPropertyAliases {
		if !names[a] || !names[n] {
			t.Errorf("binaryPropertyAliases maps %q to %q, which are not in the spec's table", a, n)
		}
	}
	for _, n := range unsupportedBinaryProperties {
		if !names[n] {
			t.Errorf("unsupportedBinaryProperties lists %q, which is not in the spec's table", n)
		}
	}
}
