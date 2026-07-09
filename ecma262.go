// Package ecma262 implements ECMA-262 regular expressions for Go
// with an API compatible with the standard regexp package.
//
// The Regexp type is safe for concurrent use by multiple goroutines,
// as it holds only the compiled pattern (immutable after construction).
// Each match operation creates its own VM instance with independent state.
package ecma262

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mgilbir/goecma262/compiler"
	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// Regexp is the representation of a compiled ECMA-262 regular expression.
// It is safe for concurrent use by multiple goroutines unless SetLastIndex
// is used, which makes the instance stateful.
type Regexp struct {
	expr      string
	flags     flags.Flags
	code      []vm.Instruction
	numGroups int
	names     []string // group names

	// Configuration options
	ignoreCase bool
	multiline  bool
	dotAll     bool
	unicode    bool
	global     bool
	sticky     bool
	maxSteps   int

	// lastIndex is the starting position for the next match when using g or y flags.
	// It is set by SetLastIndex and used by stateful match operations.
	lastIndex int
}

// Compile parses a regular expression and returns, if successful,
// a Regexp object that can be used to match against text
func Compile(expr string, f flags.Flags) (*Regexp, error) {
	// Parse the regex
	p := parser.New(expr, parser.Flags{
		IgnoreCase:  f.Has(flags.IgnoreCase),
		Unicode:     f.Has(flags.Unicode),
		UnicodeSets: f.Has(flags.UnicodeSets),
		DotAll:      f.Has(flags.DotAll),
		Multiline:   f.Has(flags.Multiline),
	})

	// Validate flag combinations before parsing so Compile and CompileFlags
	// reject the same inputs (u and v are mutually exclusive per ECMA-262).
	if f.Has(flags.Unicode) && f.Has(flags.UnicodeSets) {
		return nil, fmt.Errorf("incompatible flags: u and v")
	}

	ast, err := p.Parse()
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	// Compile to VM code
	code, numGroups, err := compiler.Compile(ast)
	if err != nil {
		return nil, fmt.Errorf("compile error: %w", err)
	}

	// Extract group names from AST
	names := extractGroupNames(ast.Body)

	return &Regexp{
		expr:       expr,
		flags:      f,
		code:       code,
		numGroups:  numGroups,
		names:      names,
		ignoreCase: f.Has(flags.IgnoreCase),
		multiline:  f.Has(flags.Multiline),
		dotAll:     f.Has(flags.DotAll),
		unicode:    f.Has(flags.Unicode) || f.Has(flags.UnicodeSets),
		global:     f.Has(flags.Global),
		sticky:     f.Has(flags.Sticky),
		maxSteps:   0,
	}, nil
}

// MustCompile is like Compile but panics if the expression cannot be parsed.
// It simplifies safe initialization of global variables holding compiled regular expressions.
func MustCompile(expr string, f flags.Flags) *Regexp {
	re, err := Compile(expr, f)
	if err != nil {
		panic(`regexp: Compile(` + quote(expr) + `): ` + err.Error())
	}
	return re
}

// CompileFlags is a convenience function that compiles a regex with flags from a string
func CompileFlags(expr string, flagStr string) (*Regexp, error) {
	f, err := flags.Parse(flagStr)
	if err != nil {
		return nil, err
	}
	return Compile(expr, f)
}

// MatchString reports whether the string s contains any match of the regular expression.
// For patterns with the g or y flag, matching starts at re.lastIndex.
func (re *Regexp) MatchString(s string) bool {
	startPos := 0
	if re.global || re.sticky {
		startPos = re.lastIndex
	}
	return re.doMatch(s, startPos) != nil
}

// SetMaxSteps sets the maximum VM instruction steps for each match operation.
// A value of 0 uses the VM default limit.
func (re *Regexp) SetMaxSteps(max int) {
	if max < 0 {
		max = 0
	}
	re.maxSteps = max
}

// SetLastIndex sets the starting position for the next match operation.
// For patterns with the g (global) or y (sticky) flag, this determines
// where searching begins. For patterns without these flags, lastIndex is ignored.
// This method makes the Regexp instance stateful; callers are responsible
// for synchronization when using across goroutines.
func (re *Regexp) SetLastIndex(n int) {
	re.lastIndex = n
}

// LastIndex returns the current lastIndex value.
func (re *Regexp) LastIndex() int {
	return re.lastIndex
}

// MatchStringErr reports whether the string s contains any match of the regular expression.
// If matching exceeds the VM step limit, it returns vm.ErrStepLimit.
func (re *Regexp) MatchStringErr(s string) (bool, error) {
	groups, err := re.doMatchWithError(s, 0)
	if err != nil {
		return false, err
	}
	return groups != nil, nil
}

// Match reports whether the byte slice b contains any match of the regular expression
func (re *Regexp) Match(b []byte) bool {
	return re.MatchString(string(b))
}

// MatchErr reports whether the byte slice b contains any match of the regular expression.
// If matching exceeds the VM step limit, it returns vm.ErrStepLimit.
func (re *Regexp) MatchErr(b []byte) (bool, error) {
	return re.MatchStringErr(string(b))
}

// MatchReader reports whether the text returned by the RuneReader contains any match
func (re *Regexp) MatchReader(r io.RuneReader) bool {
	var sb strings.Builder
	for {
		rn, _, err := r.ReadRune()
		if err != nil {
			break
		}
		sb.WriteRune(rn)
	}
	return re.MatchString(sb.String())
}

// FindString returns a string holding the text of the leftmost match in s of the regular expression.
// If there is no match, the return value is an empty string.
func (re *Regexp) FindString(s string) string {
	groups := re.doMatch(s, 0)
	if groups == nil {
		return ""
	}
	if len(groups) < 2 {
		return ""
	}
	return s[groups[0]:groups[1]]
}

// Find returns a slice holding the text of the leftmost match in b of the regular expression.
// A return value of nil indicates no match.
func (re *Regexp) Find(b []byte) []byte {
	groups := re.doMatch(string(b), 0)
	if groups == nil {
		return nil
	}
	if len(groups) < 2 {
		return nil
	}
	start, end := groups[0], groups[1]
	return b[start:end]
}

// FindIndex returns a two-element slice of integers defining the location of
// the leftmost match in b. The match itself is at b[loc[0]:loc[1]].
// A return value of nil indicates no match.
func (re *Regexp) FindIndex(b []byte) []int {
	return re.FindStringIndex(string(b))
}

// FindStringIndex returns a two-element slice of integers defining the location of
// the leftmost match in s. The match itself is at s[loc[0]:loc[1]].
// A return value of nil indicates no match.
func (re *Regexp) FindStringIndex(s string) []int {
	groups := re.doMatch(s, 0)
	if groups == nil || len(groups) < 2 {
		return nil
	}
	return []int{groups[0], groups[1]}
}

// FindStringIndexErr returns the location of the leftmost match in s.
// If matching exceeds the VM step limit, it returns vm.ErrStepLimit.
func (re *Regexp) FindStringIndexErr(s string) ([]int, error) {
	groups, err := re.doMatchWithError(s, 0)
	if err != nil {
		return nil, err
	}
	if groups == nil || len(groups) < 2 {
		return nil, nil
	}
	return []int{groups[0], groups[1]}, nil
}

// FindStringSubmatch returns a slice of strings holding the text of the leftmost
// match of the regular expression in s and the matches, if any, of its subexpressions.
// For patterns with the g or y flag, matching starts at re.lastIndex.
// A return value of nil indicates no match.
func (re *Regexp) FindStringSubmatch(s string) []string {
	startPos := 0
	if re.global || re.sticky {
		startPos = re.lastIndex
	}
	groups := re.doMatch(s, startPos)
	if groups == nil {
		return nil
	}

	totalGroups := re.numGroups + 1
	result := make([]string, totalGroups)
	for i := 0; i < totalGroups; i++ {
		if i*2+1 < len(groups) && groups[i*2] >= 0 && groups[i*2+1] >= 0 {
			result[i] = s[groups[i*2]:groups[i*2+1]]
		}
	}
	return result
}

// FindSubmatch returns a slice of slices holding the text of the leftmost match
// of the regular expression in b and the matches, if any, of its subexpressions.
// A return value of nil indicates no match.
func (re *Regexp) FindSubmatch(b []byte) [][]byte {
	strs := re.FindStringSubmatch(string(b))
	if strs == nil {
		return nil
	}
	result := make([][]byte, len(strs))
	for i, s := range strs {
		result[i] = []byte(s)
	}
	return result
}

// FindStringSubmatchIndex returns a slice holding the index pairs identifying the
// leftmost match of the regular expression in s and the matches, if any, of its subexpressions.
// A return value of nil indicates no match.
func (re *Regexp) FindStringSubmatchIndex(s string) []int {
	groups := re.doMatch(s, 0)
	if groups == nil {
		return nil
	}

	result := make([]int, (re.numGroups+1)*2)
	for i := 0; i <= re.numGroups; i++ {
		if i*2+1 < len(groups) {
			result[i*2] = groups[i*2]
			result[i*2+1] = groups[i*2+1]
		}
	}
	return result
}

// findAllMatches returns the capture-group slices of every successive match,
// scanning left to right. It is the single source of truth for how the search
// cursor advances, so every iterating method (FindAll*, ReplaceAll*, Split)
// shares identical, correct behavior — in particular for zero-width matches.
//
// doMatch returns the leftmost match at or after the requested start, so a
// match may begin ahead of the cursor (e.g. a lookahead assertion). After each
// match the cursor advances to matchEnd, and for an empty match it advances one
// further rune past matchEnd so the same zero-width position is not re-reported.
// limit < 0 means unlimited; limit == 0 returns no matches.
func (re *Regexp) findAllMatches(s string, limit int) [][]int {
	if limit == 0 {
		return nil
	}
	var matches [][]int
	searchStart := 0
	for limit < 0 || len(matches) < limit {
		if searchStart > len(s) {
			break
		}
		groups := re.doMatch(s, searchStart)
		if groups == nil || len(groups) < 2 {
			break
		}
		matches = append(matches, groups)

		matchStart, matchEnd := groups[0], groups[1]
		if matchEnd > matchStart {
			searchStart = matchEnd
		} else {
			// Empty match: advance one rune past the match position.
			if matchEnd >= len(s) {
				break
			}
			_, size := utf8.DecodeRuneInString(s[matchEnd:])
			searchStart = matchEnd + size
		}
	}
	return matches
}

// FindAllString returns a slice of all successive matches of the regular expression.
// A return value of nil indicates no match.
func (re *Regexp) FindAllString(s string, n int) []string {
	matches := re.findAllMatches(s, n)
	if len(matches) == 0 {
		return nil
	}
	result := make([]string, len(matches))
	for i, g := range matches {
		result[i] = s[g[0]:g[1]]
	}
	return result
}

// FindAll returns a slice of all successive matches of the regular expression.
// A return value of nil indicates no match.
func (re *Regexp) FindAll(b []byte, n int) [][]byte {
	strs := re.FindAllString(string(b), n)
	if strs == nil {
		return nil
	}
	result := make([][]byte, len(strs))
	for i, s := range strs {
		result[i] = []byte(s)
	}
	return result
}

// FindAllStringSubmatch returns a slice of all successive matches of the regular expression.
// It returns the matches and submatches.
// A return value of nil indicates no match.
func (re *Regexp) FindAllStringSubmatch(s string, n int) [][]string {
	matches := re.findAllMatches(s, n)
	if len(matches) == 0 {
		return nil
	}
	result := make([][]string, len(matches))
	for mi, groups := range matches {
		match := make([]string, re.numGroups+1)
		for i := 0; i <= re.numGroups; i++ {
			if i*2+1 < len(groups) && groups[i*2] >= 0 && groups[i*2+1] >= 0 {
				match[i] = s[groups[i*2]:groups[i*2+1]]
			}
		}
		result[mi] = match
	}
	return result
}

// ReplaceAllString returns a copy of src in which all matches of the regexp are
// replaced by the replacement string repl.
// Inside repl, $ signs are interpreted as in the ECMA-262 specification:
//
//	$$  → literal $
//	$&  → the matched text
//	$`  → text before the match
//	$'  → text after the match
//	$n  → nth capture group (1-indexed)
//	$nn → nth capture group (two digits)
//	$<name> → named capture group
func (re *Regexp) ReplaceAllString(src, repl string) string {
	// Without the global flag only the first match is replaced.
	limit := -1
	if !re.global {
		limit = 1
	}
	matches := re.findAllMatches(src, limit)

	var result strings.Builder
	lastEnd := 0
	for _, groups := range matches {
		result.WriteString(src[lastEnd:groups[0]])
		result.WriteString(re.expandRepl(repl, src, groups))
		lastEnd = groups[1]
	}
	result.WriteString(src[lastEnd:])
	return result.String()
}

// ReplaceAll returns a copy of src in which all matches of the regexp are
// replaced by the replacement slice repl
func (re *Regexp) ReplaceAll(src, repl []byte) []byte {
	return []byte(re.ReplaceAllString(string(src), string(repl)))
}

// ReplaceAllStringFunc returns a copy of src in which all matches of the regexp
// have been replaced by the return value of the function fn applied to the matched string
func (re *Regexp) ReplaceAllStringFunc(src string, fn func(string) string) string {
	// Match ReplaceAllString: without the global flag only the first match is
	// replaced.
	limit := -1
	if !re.global {
		limit = 1
	}
	matches := re.findAllMatches(src, limit)

	var result strings.Builder
	lastEnd := 0
	for _, groups := range matches {
		result.WriteString(src[lastEnd:groups[0]])
		result.WriteString(fn(src[groups[0]:groups[1]]))
		lastEnd = groups[1]
	}
	result.WriteString(src[lastEnd:])
	return result.String()
}

// ReplaceAllFunc returns a copy of src in which all matches of the regexp
// have been replaced by the return value of the function fn applied to the matched byte slice
func (re *Regexp) ReplaceAllFunc(src []byte, fn func([]byte) []byte) []byte {
	return []byte(re.ReplaceAllStringFunc(string(src), func(s string) string {
		return string(fn([]byte(s)))
	}))
}

// expandRepl expands $ references in the replacement string
func (re *Regexp) expandRepl(repl, src string, groups []int) string {
	var result strings.Builder
	i := 0
	for i < len(repl) {
		if repl[i] != '$' {
			result.WriteByte(repl[i])
			i++
			continue
		}

		// Found $
		i++ // skip $
		if i >= len(repl) {
			result.WriteByte('$')
			break
		}

		switch repl[i] {
		case '$':
			// $$ → literal $
			result.WriteByte('$')
			i++
		case '&':
			// $& → the matched text
			if len(groups) >= 2 && groups[0] >= 0 && groups[1] >= 0 {
				result.WriteString(src[groups[0]:groups[1]])
			}
			i++
		case '`':
			// $` → text before the match
			if len(groups) >= 2 && groups[0] >= 0 {
				result.WriteString(src[:groups[0]])
			}
			i++
		case '\'':
			// $' → text after the match
			if len(groups) >= 2 && groups[1] >= 0 {
				result.WriteString(src[groups[1]:])
			}
			i++
		case '<':
			// $<name> → named capture group (per ECMA-262 GetSubstitution)
			nameStart := i + 1 // position right after '<'
			end := strings.IndexByte(repl[i+1:], '>')
			if end == -1 {
				// Unclosed $< — emit $< literally, continue from nameStart
				result.WriteString("$<")
				i = nameStart
				continue
			}
			name := repl[nameStart : nameStart+end]
			nameEnd := nameStart + end + 1 // position after '>'

			// Check whether any named group exists.
			hasNamedGroups := false
			for gi := 1; gi < len(re.names); gi++ {
				if re.names[gi] != "" {
					hasNamedGroups = true
					break
				}
			}

			if hasNamedGroups {
				// Pattern has named groups: consume the whole $<name> and look up.
				// For ES2022 duplicate names, find whichever group with this name participated.
				i = nameEnd
				groupIdx := -1
				for gi := 1; gi < len(re.names); gi++ {
					if re.names[gi] == name {
						// Prefer the group that actually participated in this match
						if gi*2+1 < len(groups) && groups[gi*2] >= 0 && groups[gi*2+1] >= 0 {
							groupIdx = gi
							break
						}
						// Remember first match as fallback
						if groupIdx < 0 {
							groupIdx = gi
						}
					}
				}
				if groupIdx >= 0 && groupIdx*2+1 < len(groups) &&
					groups[groupIdx*2] >= 0 && groups[groupIdx*2+1] >= 0 {
					result.WriteString(src[groups[groupIdx*2]:groups[groupIdx*2+1]])
				}
			} else {
				// No named groups: emit "$<" literally and re-process from nameStart.
				// This lets any $n references inside the name still expand.
				result.WriteString("$<")
				i = nameStart
			}
		default:
			if repl[i] >= '0' && repl[i] <= '9' {
				// $n or $nn → capture group, per ECMA-262 GetSubstitution.
				// A two-digit number is preferred when it names a valid group;
				// otherwise a single digit is tried. Only 1..numGroups are valid
				// references — $0, $00 and out-of-range numbers are literal.
				chosen, consume := -1, 0
				if i+1 < len(repl) && repl[i+1] >= '0' && repl[i+1] <= '9' {
					if n2, err := strconv.Atoi(repl[i : i+2]); err == nil && n2 >= 1 && n2 <= re.numGroups {
						chosen, consume = n2, 2
					}
				}
				if chosen < 0 {
					if n1 := int(repl[i] - '0'); n1 >= 1 && n1 <= re.numGroups {
						chosen, consume = n1, 1
					}
				}
				if chosen >= 1 {
					i += consume
					if chosen*2+1 < len(groups) && groups[chosen*2] >= 0 && groups[chosen*2+1] >= 0 {
						result.WriteString(src[groups[chosen*2]:groups[chosen*2+1]])
					}
				} else {
					// Not a valid group reference (e.g. $0) — emit a literal $
					// and let the digits be copied verbatim on the next passes.
					result.WriteByte('$')
				}
			} else {
				// Unknown $ sequence — emit literal $
				result.WriteByte('$')
			}
		}
	}
	return result.String()
}

// Split slices s into substrings separated by the expression and returns
// a slice of the substrings between those expression matches
func (re *Regexp) Split(s string, n int) []string {
	if n == 0 {
		return nil
	}

	if n < 0 {
		n = len(s) + 1
	}

	matches := re.findAllMatches(s, -1)

	var result []string
	lastEnd := 0
	for _, groups := range matches {
		if len(result) >= n-1 {
			break
		}
		result = append(result, s[lastEnd:groups[0]])
		lastEnd = groups[1]
	}

	result = append(result, s[lastEnd:])
	return result
}

// NumSubexp returns the number of parenthesized subexpressions in this Regexp
func (re *Regexp) NumSubexp() int {
	return re.numGroups
}

// SubexpNames returns the names of the parenthesized subexpressions in this Regexp
func (re *Regexp) SubexpNames() []string {
	return re.names
}

// SubexpIndex returns the index of the first subexpression with the given name
// or -1 if there is no subexpression with that name
func (re *Regexp) SubexpIndex(name string) int {
	for i, n := range re.names {
		if n == name {
			return i
		}
	}
	return -1
}

// String returns the source text used to compile the regular expression
func (re *Regexp) String() string {
	return re.expr
}

// UnmarshalText implements encoding.TextUnmarshaler
func (re *Regexp) UnmarshalText(text []byte) error {
	newRE, err := Compile(string(text), flags.Flags(0))
	if err != nil {
		return err
	}
	*re = *newRE
	return nil
}

// doMatch performs the actual matching and returns the capture groups
func (re *Regexp) doMatch(s string, startPos int) []int {
	// Clamp the start position. A negative lastIndex is treated as 0; a
	// start position beyond the input yields no match (ECMA-262 semantics),
	// never a panic.
	if startPos < 0 {
		startPos = 0
	}
	if startPos > len(s) {
		return nil
	}

	v := &vm.VM{
		Code:       re.code,
		NumGroups:  re.numGroups,
		IgnoreCase: re.ignoreCase,
		Multiline:  re.multiline,
		DotAll:     re.dotAll,
		Unicode:    re.unicode,
		MaxSteps:   re.maxSteps,
	}

	// Sticky flag: only attempt match at startPos (anchored)
	if re.sticky {
		matched, _, groups := v.Match(s, startPos)
		if v.Err != nil || !matched {
			return nil
		}
		return groups
	}

	// Try different starting positions to find a match
	for pos := startPos; pos <= len(s); {
		matched, _, groups := v.Match(s, pos)
		if v.Err != nil {
			return nil
		}
		if matched {
			return groups
		}

		// Advance by one rune (or byte if at end)
		if pos >= len(s) {
			break
		}
		_, size := utf8.DecodeRuneInString(s[pos:])
		pos += size
	}

	return nil
}

// doMatchWithError performs the actual matching and returns the capture groups.
// If the VM exceeds its step limit, returns vm.ErrStepLimit.
func (re *Regexp) doMatchWithError(s string, startPos int) ([]int, error) {
	if startPos < 0 {
		startPos = 0
	}
	if startPos > len(s) {
		return nil, nil
	}

	v := &vm.VM{
		Code:       re.code,
		NumGroups:  re.numGroups,
		IgnoreCase: re.ignoreCase,
		Multiline:  re.multiline,
		DotAll:     re.dotAll,
		Unicode:    re.unicode,
		MaxSteps:   re.maxSteps,
	}

	// Sticky flag: only attempt match at startPos (anchored)
	if re.sticky {
		matched, _, groups := v.Match(s, startPos)
		if v.Err != nil {
			return nil, v.Err
		}
		if !matched {
			return nil, nil
		}
		return groups, nil
	}

	for pos := startPos; pos <= len(s); {
		matched, _, groups := v.Match(s, pos)
		if v.Err != nil {
			return nil, v.Err
		}
		if matched {
			return groups, nil
		}
		if pos >= len(s) {
			break
		}
		_, size := utf8.DecodeRuneInString(s[pos:])
		pos += size
	}

	return nil, nil
}

// Convenience functions

// Match reports whether the byte slice b contains any match of the regular expression pattern
// with the given flags
func Match(pattern string, f flags.Flags, b []byte) (matched bool, err error) {
	re, err := Compile(pattern, f)
	if err != nil {
		return false, err
	}
	return re.Match(b), nil
}

// MatchString reports whether the string s contains any match of the regular expression pattern
// with the given flags
func MatchString(pattern string, f flags.Flags, s string) (matched bool, err error) {
	re, err := Compile(pattern, f)
	if err != nil {
		return false, err
	}
	return re.MatchString(s), nil
}

// isValidIdentifierName reports whether s is a valid ECMAScript IdentifierName.
// An IdentifierName starts with $, _, or a Unicode letter (ID_Start),
// followed by $, _, digits, or Unicode letters/combining marks (ID_Continue).
func isValidIdentifierName(s string) bool {
	if s == "" {
		return false
	}
	first := true
	for _, r := range s {
		if first {
			if r == '$' || r == '_' || unicode.IsLetter(r) {
				first = false
				continue
			}
			return false
		}
		if r == '$' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) ||
			r == 0x200C || r == 0x200D {
			continue
		}
		return false
	}
	return !first
}

// Helper functions

func quote(s string) string {
	if strings.ContainsAny(s, "`") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "`" + s + "`"
}

func extractGroupNames(node parser.Node) []string {
	// Extract group names from AST
	names := []string{""} // Group 0 has no name

	var extract func(parser.Node)
	extract = func(n parser.Node) {
		switch v := n.(type) {
		case *parser.Disjunction:
			for _, alt := range v.Alternatives {
				extract(alt)
			}
		case *parser.Sequence:
			for _, elem := range v.Elements {
				extract(elem)
			}
		case *parser.Group:
			names = append(names, "")
			extract(v.Body)
		case *parser.NamedGroup:
			names = append(names, v.Name)
			extract(v.Body)
		case *parser.NonCapturingGroup:
			extract(v.Body)
		case *parser.Quantifier:
			extract(v.Body)
		case *parser.Lookahead, *parser.NegativeLookahead, *parser.Lookbehind, *parser.NegativeLookbehind:
			// Lookarounds don't add capture groups
		}
	}

	extract(node)
	return names
}
