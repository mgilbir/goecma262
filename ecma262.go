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
	"unicode/utf8"

	"github.com/mgilbir/goecma262/compiler"
	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// Regexp is the representation of a compiled ECMA-262 regular expression.
// It is safe for concurrent use by multiple goroutines.
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

// MatchString reports whether the string s contains any match of the regular expression
func (re *Regexp) MatchString(s string) bool {
	return re.doMatch(s, 0) != nil
}

// SetMaxSteps sets the maximum VM instruction steps for each match operation.
// A value of 0 uses the VM default limit.
func (re *Regexp) SetMaxSteps(max int) {
	if max < 0 {
		max = 0
	}
	re.maxSteps = max
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
// A return value of nil indicates no match.
func (re *Regexp) FindStringSubmatch(s string) []string {
	groups := re.doMatch(s, 0)
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

// FindAllString returns a slice of all successive matches of the regular expression.
// A return value of nil indicates no match.
func (re *Regexp) FindAllString(s string, n int) []string {
	if n == 0 {
		return nil
	}

	var result []string
	pos := 0

	for n < 0 || len(result) < n {
		groups := re.doMatch(s, pos)
		if groups == nil || len(groups) < 2 {
			break
		}

		matchStart := groups[0]
		matchEnd := groups[1]

		result = append(result, s[matchStart:matchEnd])

		if matchStart == matchEnd {
			// Empty match - advance by one rune
			if pos >= len(s) {
				break
			}
			_, size := utf8.DecodeRuneInString(s[pos:])
			pos += size
		} else {
			pos = matchEnd
		}
	}

	if len(result) == 0 {
		return nil
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
	if n == 0 {
		return nil
	}

	var result [][]string
	pos := 0

	for n < 0 || len(result) < n {
		groups := re.doMatch(s, pos)
		if groups == nil {
			break
		}

		matchStart := groups[0]
		matchEnd := groups[1]

		match := make([]string, re.numGroups+1)
		for i := 0; i <= re.numGroups; i++ {
			if i*2+1 < len(groups) && groups[i*2] >= 0 && groups[i*2+1] >= 0 {
				match[i] = s[groups[i*2]:groups[i*2+1]]
			}
		}
		result = append(result, match)

		if matchStart == matchEnd {
			// Empty match - advance by one rune
			if pos >= len(s) {
				break
			}
			_, size := utf8.DecodeRuneInString(s[pos:])
			pos += size
		} else {
			pos = matchEnd
		}
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
	var result strings.Builder
	lastEnd := 0
	pos := 0

	for {
		groups := re.doMatch(src, pos)
		if groups == nil || len(groups) < 2 {
			break
		}

		matchStart := groups[0]
		matchEnd := groups[1]

		result.WriteString(src[lastEnd:matchStart])
		result.WriteString(re.expandRepl(repl, src, groups))

		if matchStart == matchEnd {
			if pos >= len(src) {
				lastEnd = matchEnd
				break
			}
			_, size := utf8.DecodeRuneInString(src[pos:])
			result.WriteString(src[pos : pos+size])
			pos += size
			lastEnd = pos
		} else {
			lastEnd = matchEnd
			pos = matchEnd
		}

		// Without the global flag, only replace the first match
		if !re.global {
			break
		}
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
	var result strings.Builder
	lastEnd := 0
	pos := 0

	for {
		groups := re.doMatch(src, pos)
		if groups == nil || len(groups) < 2 {
			break
		}

		matchStart := groups[0]
		matchEnd := groups[1]

		result.WriteString(src[lastEnd:matchStart])
		result.WriteString(fn(src[matchStart:matchEnd]))

		if matchStart == matchEnd {
			if pos >= len(src) {
				lastEnd = matchEnd
				break
			}
			_, size := utf8.DecodeRuneInString(src[pos:])
			result.WriteString(src[pos : pos+size])
			pos += size
			lastEnd = pos
		} else {
			lastEnd = matchEnd
			pos = matchEnd
		}
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
			// $<name> → named capture group
			i++ // skip <
			end := strings.IndexByte(repl[i:], '>')
			if end == -1 {
				// Unclosed $< — emit literally
				result.WriteString("$<")
				continue
			}
			name := repl[i : i+end]
			i += end + 1 // skip name and >

			// If the pattern has no named groups, $<name> is a literal (per spec).
			// Check whether any named group exists (re.names contains non-empty entries beyond index 0).
			hasNamedGroups := false
			for gi := 1; gi < len(re.names); gi++ {
				if re.names[gi] != "" {
					hasNamedGroups = true
					break
				}
			}
			if !hasNamedGroups {
				// No named groups: emit $<name> literally
				result.WriteString("$<")
				result.WriteString(name)
				result.WriteString(">")
				continue
			}

			// Find group by name (skip group 0 which always has empty name)
			groupIdx := -1
			for gi := 1; gi < len(re.names); gi++ {
				if re.names[gi] == name {
					groupIdx = gi
					break
				}
			}
			// If name not found or group didn't participate in match, expand to ""
			if groupIdx >= 0 && groupIdx*2+1 < len(groups) &&
				groups[groupIdx*2] >= 0 && groups[groupIdx*2+1] >= 0 {
				result.WriteString(src[groups[groupIdx*2]:groups[groupIdx*2+1]])
			}
		default:
			if repl[i] >= '0' && repl[i] <= '9' {
				// $n or $nn → capture group
				numStr := string(repl[i])
				i++
				// Try two-digit group number
				if i < len(repl) && repl[i] >= '0' && repl[i] <= '9' {
					twoDigit, _ := strconv.Atoi(numStr + string(repl[i]))
					if twoDigit <= re.numGroups {
						numStr += string(repl[i])
						i++
					}
				}
				num, _ := strconv.Atoi(numStr)
				if num >= 0 && num*2+1 < len(groups) &&
					groups[num*2] >= 0 && groups[num*2+1] >= 0 {
					result.WriteString(src[groups[num*2]:groups[num*2+1]])
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

	var result []string
	pos := 0
	lastEnd := 0

	for len(result) < n-1 {
		groups := re.doMatch(s, pos)
		if groups == nil || len(groups) < 2 {
			break
		}

		matchStart := groups[0]
		matchEnd := groups[1]

		result = append(result, s[lastEnd:matchStart])

		if matchStart == matchEnd {
			// Empty match - advance by one rune
			if pos >= len(s) {
				break
			}
			_, size := utf8.DecodeRuneInString(s[pos:])
			pos += size
			lastEnd = pos
		} else {
			lastEnd = matchEnd
			pos = matchEnd
		}
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
	v := &vm.VM{
		Code:       re.code,
		NumGroups:  re.numGroups,
		IgnoreCase: re.ignoreCase,
		Multiline:  re.multiline,
		DotAll:     re.dotAll,
		Unicode:    re.unicode,
		MaxSteps:   re.maxSteps,
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
	v := &vm.VM{
		Code:       re.code,
		NumGroups:  re.numGroups,
		IgnoreCase: re.ignoreCase,
		Multiline:  re.multiline,
		DotAll:     re.dotAll,
		Unicode:    re.unicode,
		MaxSteps:   re.maxSteps,
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
