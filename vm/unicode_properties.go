package vm

import (
	"strings"
	"unicode"
)

// Property names and values in \p{...} are matched exactly as ECMA-262 lists
// them (§22.2.2.9, tables "Non-binary Unicode property aliases", "Binary
// Unicode property aliases" and the value tables for General_Category and
// Script): case, underscores and spaces are significant, so \p{Lowercase}
// and \p{Lower} are valid while \p{lowercase}, \p{ Lowercase } and \p{WSpace}
// are SyntaxErrors.

// resolveUnicodeProperty maps a UnicodePropertyValueExpression to a rune
// predicate. ok is false if it is not a valid expression, or if it names a
// property this engine has no data for (see unsupportedBinaryProperties).
func resolveUnicodeProperty(prop string) (fn func(rune) bool, ok bool) {
	if name, value, found := strings.Cut(prop, "="); found {
		switch name {
		case "General_Category", "gc":
			return generalCategory(value)
		case "Script", "sc", "Script_Extensions", "scx":
			// Script_Extensions is approximated by Script: Go has no
			// Script_Extensions data.
			return script(value)
		}
		return nil, false
	}
	if fn, ok := generalCategory(prop); ok {
		return fn, true
	}
	return binaryProperty(prop)
}

// generalCategoryValues maps each General_Category value and alias to its
// short name, which keys unicode.Categories except for LC, Cn and C, derived
// below.
var generalCategoryValues = map[string]string{}

func init() {
	// Short name, long name, and further aliases, as in PropertyValueAliases.txt.
	for _, names := range [][]string{
		{"C", "Other"}, {"Cc", "Control", "cntrl"}, {"Cf", "Format"}, {"Cn", "Unassigned"},
		{"Co", "Private_Use"}, {"Cs", "Surrogate"},
		{"L", "Letter"}, {"LC", "Cased_Letter"}, {"Ll", "Lowercase_Letter"}, {"Lm", "Modifier_Letter"},
		{"Lo", "Other_Letter"}, {"Lt", "Titlecase_Letter"}, {"Lu", "Uppercase_Letter"},
		{"M", "Mark", "Combining_Mark"}, {"Mc", "Spacing_Mark"}, {"Me", "Enclosing_Mark"}, {"Mn", "Nonspacing_Mark"},
		{"N", "Number"}, {"Nd", "Decimal_Number", "digit"}, {"Nl", "Letter_Number"}, {"No", "Other_Number"},
		{"P", "Punctuation", "punct"}, {"Pc", "Connector_Punctuation"}, {"Pd", "Dash_Punctuation"},
		{"Pe", "Close_Punctuation"}, {"Pf", "Final_Punctuation"}, {"Pi", "Initial_Punctuation"},
		{"Po", "Other_Punctuation"}, {"Ps", "Open_Punctuation"},
		{"S", "Symbol"}, {"Sc", "Currency_Symbol"}, {"Sk", "Modifier_Symbol"}, {"Sm", "Math_Symbol"}, {"So", "Other_Symbol"},
		{"Z", "Separator"}, {"Zl", "Line_Separator"}, {"Zp", "Paragraph_Separator"}, {"Zs", "Space_Separator"},
	} {
		for _, n := range names {
			generalCategoryValues[n] = names[0]
		}
	}
}

func generalCategory(value string) (func(rune) bool, bool) {
	short, ok := generalCategoryValues[value]
	if !ok {
		return nil, false
	}
	switch short {
	case "LC":
		return func(r rune) bool { return unicode.In(r, unicode.Lu, unicode.Ll, unicode.Lt) }, true
	case "Cn":
		return isUnassigned, true
	case "C":
		return func(r rune) bool {
			return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Co, unicode.Cs) || isUnassigned(r)
		}, true
	}
	t := unicode.Categories[short]
	if t == nil {
		return nil, false
	}
	return func(r rune) bool { return unicode.Is(t, r) }, true
}

// isUnassigned reports whether r has General_Category Cn: no other category.
// (Recent Go versions have Cn tables and include Cn in C; older ones have
// neither, so neither is used.)
func isUnassigned(r rune) bool {
	return !unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Z,
		unicode.Cc, unicode.Cf, unicode.Co, unicode.Cs)
}

// script resolves a Script value: a name keying unicode.Scripts, or an alias
// from scriptAliases. Unknown, the script of the code points that have none,
// is the complement of Go's script tables.
func script(value string) (func(rune) bool, bool) {
	if long, ok := scriptAliases[value]; ok {
		value = long
	}
	if value == "Unknown" {
		return isUnknownScript, true
	}
	t := unicode.Scripts[value]
	if t == nil {
		return nil, false
	}
	return func(r rune) bool { return unicode.Is(t, r) }, true
}

var allScripts = func() []*unicode.RangeTable {
	out := make([]*unicode.RangeTable, 0, len(unicode.Scripts))
	for _, t := range unicode.Scripts {
		out = append(out, t)
	}
	return out
}()

func isUnknownScript(r rune) bool {
	return !unicode.In(r, allScripts...)
}

// scriptAliases maps the short names and other aliases of scripts to their
// long names, from the sc entries of PropertyValueAliases-17.0.0.txt. Aliases
// of scripts that Go's unicode package does not have (Katakana_Or_Hiragana,
// which no code point has, or scripts newer than its Unicode version) resolve
// to no table and are rejected.
var scriptAliases = map[string]string{
	"Adlm": "Adlam",
	"Aghb": "Caucasian_Albanian",
	"Arab": "Arabic",
	"Armi": "Imperial_Aramaic",
	"Armn": "Armenian",
	"Avst": "Avestan",
	"Bali": "Balinese",
	"Bamu": "Bamum",
	"Bass": "Bassa_Vah",
	"Batk": "Batak",
	"Beng": "Bengali",
	"Berf": "Beria_Erfe",
	"Bhks": "Bhaiksuki",
	"Bopo": "Bopomofo",
	"Brah": "Brahmi",
	"Brai": "Braille",
	"Bugi": "Buginese",
	"Buhd": "Buhid",
	"Cakm": "Chakma",
	"Cans": "Canadian_Aboriginal",
	"Cari": "Carian",
	"Cher": "Cherokee",
	"Chrs": "Chorasmian",
	"Copt": "Coptic",
	"Cpmn": "Cypro_Minoan",
	"Cprt": "Cypriot",
	"Cyrl": "Cyrillic",
	"Deva": "Devanagari",
	"Diak": "Dives_Akuru",
	"Dogr": "Dogra",
	"Dsrt": "Deseret",
	"Dupl": "Duployan",
	"Egyp": "Egyptian_Hieroglyphs",
	"Elba": "Elbasan",
	"Elym": "Elymaic",
	"Ethi": "Ethiopic",
	"Gara": "Garay",
	"Geor": "Georgian",
	"Glag": "Glagolitic",
	"Gong": "Gunjala_Gondi",
	"Gonm": "Masaram_Gondi",
	"Goth": "Gothic",
	"Gran": "Grantha",
	"Grek": "Greek",
	"Gujr": "Gujarati",
	"Gukh": "Gurung_Khema",
	"Guru": "Gurmukhi",
	"Hang": "Hangul",
	"Hani": "Han",
	"Hano": "Hanunoo",
	"Hatr": "Hatran",
	"Hebr": "Hebrew",
	"Hira": "Hiragana",
	"Hluw": "Anatolian_Hieroglyphs",
	"Hmng": "Pahawh_Hmong",
	"Hmnp": "Nyiakeng_Puachue_Hmong",
	"Hrkt": "Katakana_Or_Hiragana",
	"Hung": "Old_Hungarian",
	"Ital": "Old_Italic",
	"Java": "Javanese",
	"Kali": "Kayah_Li",
	"Kana": "Katakana",
	"Khar": "Kharoshthi",
	"Khmr": "Khmer",
	"Khoj": "Khojki",
	"Kits": "Khitan_Small_Script",
	"Knda": "Kannada",
	"Krai": "Kirat_Rai",
	"Kthi": "Kaithi",
	"Lana": "Tai_Tham",
	"Laoo": "Lao",
	"Latn": "Latin",
	"Lepc": "Lepcha",
	"Limb": "Limbu",
	"Lina": "Linear_A",
	"Linb": "Linear_B",
	"Lyci": "Lycian",
	"Lydi": "Lydian",
	"Mahj": "Mahajani",
	"Maka": "Makasar",
	"Mand": "Mandaic",
	"Mani": "Manichaean",
	"Marc": "Marchen",
	"Medf": "Medefaidrin",
	"Mend": "Mende_Kikakui",
	"Merc": "Meroitic_Cursive",
	"Mero": "Meroitic_Hieroglyphs",
	"Mlym": "Malayalam",
	"Mong": "Mongolian",
	"Mroo": "Mro",
	"Mtei": "Meetei_Mayek",
	"Mult": "Multani",
	"Mymr": "Myanmar",
	"Nagm": "Nag_Mundari",
	"Nand": "Nandinagari",
	"Narb": "Old_North_Arabian",
	"Nbat": "Nabataean",
	"Nkoo": "Nko",
	"Nshu": "Nushu",
	"Ogam": "Ogham",
	"Olck": "Ol_Chiki",
	"Onao": "Ol_Onal",
	"Orkh": "Old_Turkic",
	"Orya": "Oriya",
	"Osge": "Osage",
	"Osma": "Osmanya",
	"Ougr": "Old_Uyghur",
	"Palm": "Palmyrene",
	"Pauc": "Pau_Cin_Hau",
	"Perm": "Old_Permic",
	"Phag": "Phags_Pa",
	"Phli": "Inscriptional_Pahlavi",
	"Phlp": "Psalter_Pahlavi",
	"Phnx": "Phoenician",
	"Plrd": "Miao",
	"Prti": "Inscriptional_Parthian",
	"Qaac": "Coptic",
	"Qaai": "Inherited",
	"Rjng": "Rejang",
	"Rohg": "Hanifi_Rohingya",
	"Runr": "Runic",
	"Samr": "Samaritan",
	"Sarb": "Old_South_Arabian",
	"Saur": "Saurashtra",
	"Sgnw": "SignWriting",
	"Shaw": "Shavian",
	"Shrd": "Sharada",
	"Sidd": "Siddham",
	"Sidt": "Sidetic",
	"Sind": "Khudawadi",
	"Sinh": "Sinhala",
	"Sogd": "Sogdian",
	"Sogo": "Old_Sogdian",
	"Sora": "Sora_Sompeng",
	"Soyo": "Soyombo",
	"Sund": "Sundanese",
	"Sunu": "Sunuwar",
	"Sylo": "Syloti_Nagri",
	"Syrc": "Syriac",
	"Tagb": "Tagbanwa",
	"Takr": "Takri",
	"Tale": "Tai_Le",
	"Talu": "New_Tai_Lue",
	"Taml": "Tamil",
	"Tang": "Tangut",
	"Tavt": "Tai_Viet",
	"Tayo": "Tai_Yo",
	"Telu": "Telugu",
	"Tfng": "Tifinagh",
	"Tglg": "Tagalog",
	"Thaa": "Thaana",
	"Tibt": "Tibetan",
	"Tirh": "Tirhuta",
	"Tnsa": "Tangsa",
	"Todr": "Todhri",
	"Tols": "Tolong_Siki",
	"Tutg": "Tulu_Tigalari",
	"Ugar": "Ugaritic",
	"Vaii": "Vai",
	"Vith": "Vithkuqi",
	"Wara": "Warang_Citi",
	"Wcho": "Wancho",
	"Xpeo": "Old_Persian",
	"Xsux": "Cuneiform",
	"Yezi": "Yezidi",
	"Yiii": "Yi",
	"Zanb": "Zanabazar_Square",
	"Zinh": "Inherited",
	"Zyyy": "Common",
	"Zzzz": "Unknown",
}

// binaryProperties maps each ECMA-262 binary property this engine supports to
// its predicate: Go's table of the same name, or a derivation from Go's
// tables. Case_Ignorable, Changes_When_Lowercased, Changes_When_Titlecased,
// Changes_When_Uppercased and Default_Ignorable_Code_Point are close
// approximations of their Unicode definitions, and XID_Start and
// XID_Continue are taken to be ID_Start and ID_Continue.
var binaryProperties = map[string]func(rune) bool{
	"ASCII":                        func(r rune) bool { return r >= 0 && r <= 0x7F },
	"Any":                          func(r rune) bool { return true },
	"Assigned":                     func(r rune) bool { return !isUnassigned(r) },
	"Alphabetic":                   isAlphabetic,
	"Cased":                        isCased,
	"Case_Ignorable":               isCaseIgnorable,
	"Changes_When_Lowercased":      func(r rune) bool { return unicode.ToLower(r) != r },
	"Changes_When_Titlecased":      func(r rune) bool { return unicode.ToTitle(r) != r },
	"Changes_When_Uppercased":      func(r rune) bool { return unicode.ToUpper(r) != r },
	"Default_Ignorable_Code_Point": isDefaultIgnorable,
	"Grapheme_Extend":              isGraphemeExtend,
	"ID_Continue":                  isIDContinue,
	"ID_Start":                     isIDStart,
	"Lowercase":                    func(r rune) bool { return unicode.In(r, unicode.Ll, unicode.Other_Lowercase) },
	"Math":                         func(r rune) bool { return unicode.In(r, unicode.Sm, unicode.Other_Math) },
	"Uppercase":                    func(r rune) bool { return unicode.In(r, unicode.Lu, unicode.Other_Uppercase) },
	"XID_Continue":                 isIDContinue,
	"XID_Start":                    isIDStart,
}

// binaryPropertyTables are the ECMA-262 binary properties that are Go
// unicode.Properties tables of the same name.
var binaryPropertyTables = []string{
	"ASCII_Hex_Digit", "Bidi_Control", "Dash", "Deprecated", "Diacritic", "Extender", "Hex_Digit",
	"IDS_Binary_Operator", "IDS_Trinary_Operator", "Ideographic", "Join_Control", "Logical_Order_Exception",
	"Noncharacter_Code_Point", "Pattern_Syntax", "Pattern_White_Space", "Quotation_Mark", "Radical",
	"Regional_Indicator", "Sentence_Terminal", "Soft_Dotted", "Terminal_Punctuation", "Unified_Ideograph",
	"Variation_Selector", "White_Space",
}

// unsupportedBinaryProperties are valid in ECMA-262 but have no data here;
// they are compile errors rather than properties that silently match nothing.
var unsupportedBinaryProperties = []string{
	"Bidi_Mirrored", "Changes_When_Casefolded", "Changes_When_Casemapped", "Changes_When_NFKC_Casefolded",
	"Emoji", "Emoji_Component", "Emoji_Modifier", "Emoji_Modifier_Base", "Emoji_Presentation",
	"Extended_Pictographic", "Grapheme_Base",
}

// binaryPropertyAliases maps the ECMA-262 short names of binary properties to
// their long names.
var binaryPropertyAliases = map[string]string{
	"AHex": "ASCII_Hex_Digit", "Alpha": "Alphabetic", "Bidi_C": "Bidi_Control", "Bidi_M": "Bidi_Mirrored",
	"CI": "Case_Ignorable", "CWCF": "Changes_When_Casefolded", "CWCM": "Changes_When_Casemapped",
	"CWKCF": "Changes_When_NFKC_Casefolded", "CWL": "Changes_When_Lowercased", "CWT": "Changes_When_Titlecased",
	"CWU": "Changes_When_Uppercased", "DI": "Default_Ignorable_Code_Point", "Dep": "Deprecated", "Dia": "Diacritic",
	"EBase": "Emoji_Modifier_Base", "EComp": "Emoji_Component", "EMod": "Emoji_Modifier", "EPres": "Emoji_Presentation",
	"ExtPict": "Extended_Pictographic", "Ext": "Extender", "Gr_Base": "Grapheme_Base", "Gr_Ext": "Grapheme_Extend",
	"Hex": "Hex_Digit", "IDC": "ID_Continue", "IDS": "ID_Start", "IDSB": "IDS_Binary_Operator",
	"IDST": "IDS_Trinary_Operator", "Ideo": "Ideographic", "Join_C": "Join_Control", "LOE": "Logical_Order_Exception",
	"Lower": "Lowercase", "NChar": "Noncharacter_Code_Point", "Pat_Syn": "Pattern_Syntax", "Pat_WS": "Pattern_White_Space",
	"QMark": "Quotation_Mark", "RI": "Regional_Indicator", "SD": "Soft_Dotted", "STerm": "Sentence_Terminal",
	"Term": "Terminal_Punctuation", "UIdeo": "Unified_Ideograph", "Upper": "Uppercase", "VS": "Variation_Selector",
	"space": "White_Space", "XIDC": "XID_Continue", "XIDS": "XID_Start",
}

func init() {
	for _, name := range binaryPropertyTables {
		t := unicode.Properties[name]
		if t == nil {
			panic("vm: unicode.Properties has no " + name)
		}
		binaryProperties[name] = func(r rune) bool { return unicode.Is(t, r) }
	}
}

func binaryProperty(name string) (func(rune) bool, bool) {
	if long, ok := binaryPropertyAliases[name]; ok {
		name = long
	}
	fn, ok := binaryProperties[name]
	return fn, ok
}

// Alphabetic is Lu, Ll, Lt, Lm, Lo, Nl and Other_Alphabetic.
func isAlphabetic(r rune) bool {
	return unicode.In(r, unicode.L, unicode.Nl, unicode.Other_Alphabetic)
}

// Cased is Lowercase, Uppercase and Lt.
func isCased(r rune) bool {
	return unicode.In(r, unicode.Ll, unicode.Lu, unicode.Lt, unicode.Other_Lowercase, unicode.Other_Uppercase)
}

func isCaseIgnorable(r rune) bool {
	// Approximation of Case_Ignorable: the combining/format/modifier categories
	// (the Word_Break refinements are omitted).
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// ID_Start is L, Nl and Other_ID_Start, less Pattern_Syntax and
// Pattern_White_Space.
func isIDStart(r rune) bool {
	return unicode.In(r, unicode.L, unicode.Nl, unicode.Other_ID_Start) &&
		!unicode.In(r, unicode.Pattern_Syntax, unicode.Pattern_White_Space)
}

// ID_Continue is ID_Start, Mn, Mc, Nd, Pc and Other_ID_Continue, less
// Pattern_Syntax and Pattern_White_Space.
func isIDContinue(r rune) bool {
	return isIDStart(r) ||
		unicode.In(r, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc, unicode.Other_ID_Continue) &&
			!unicode.In(r, unicode.Pattern_Syntax, unicode.Pattern_White_Space)
}

// Grapheme_Extend is Me, Mn and Other_Grapheme_Extend.
func isGraphemeExtend(r rune) bool {
	return unicode.In(r, unicode.Me, unicode.Mn, unicode.Other_Grapheme_Extend)
}

func isDefaultIgnorable(r rune) bool {
	// Approximation of Default_Ignorable_Code_Point.
	return unicode.In(r, unicode.Other_Default_Ignorable_Code_Point, unicode.Cf, unicode.Variation_Selector)
}
