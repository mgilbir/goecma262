// Package ecma262 implements ECMA-262 regular expressions for Go
// with an API compatible with the standard regexp package.
//
// A Regexp compiled without the g (global) or y (sticky) flag is immutable
// after construction and safe for concurrent use by multiple goroutines;
// each match operation creates its own VM instance with independent state.
// With g or y, the instance carries a lastIndex cursor that match operations
// read and advance (mirroring JavaScript semantics), so such instances must
// not be shared between goroutines without synchronization. See SetLastIndex.
//
// # Bounded matching
//
// Matching is a backtracking search with an explicit, heap-allocated stack, so
// no input can overflow the goroutine stack. Each match operation runs under a
// budget of steps and backtracking memory (see vm.ErrStepLimit for the exact
// bounds). By default the budget grows linearly with the input, so it stops
// super-linear (ReDoS) searches while searches that run in linear time — which
// the engine achieves for common shapes such as ^[a-z]+$, ^(a)+$ or ^(a|b)+$
// — complete at any input length. An operation that exceeds its budget has no
// answer, which is distinct from "no match":
//
//   - The error-returning forms — the package-level MatchString and Match, and
//     the methods whose names end in Err — return ErrStepLimit (test with
//     errors.Is) and no result.
//   - The other methods cannot return an error; they report an exceeded
//     budget as no match, and iterating methods (FindAll*, ReplaceAll*, Split)
//     stop at that point. Use the Err forms whenever the input or the pattern
//     is untrusted.
//
// # Characters above U+FFFF
//
// Without the u or v flag, ECMA-262 matches a string as a sequence of UTF-16
// code units, and so does this package: a character above U+FFFF, such as
// "😀", is two characters to such a pattern, its high surrogate and then its
// low surrogate. So /^..$/ matches "😀" and /^.$/ does not, /\uD83D/ matches
// its first half, and /😀{2}/ repeats only the second. With u or v a
// character is a code point, as in Go.
//
// A result that begins or ends between the two halves has no Go form: a Go
// string cannot hold half a pair, and the position between them is no byte
// offset. What must be expressible depends on what a method returns:
//
//   - positions (the *Index methods) must not fall between two halves;
//   - strings (FindString, FindAllString, FindStringSubmatch, Split, ...) must
//     not hold half a pair, so an empty match between the halves is "";
//   - the result of a replacement must not hold half a pair; as in JavaScript,
//     text ending in the first half and text starting with the second join;
//   - MatchString and MatchStringErr report only whether there is a match.
//
// The Err methods return ErrSurrogateSplit for a result with no Go form. The
// others report no match: FindAll* and Split stop there, and ReplaceAll*
// return the input unchanged. The lastIndex of a g or y pattern may fall
// between two halves; see SetLastIndex.
package ecma262

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/mgilbir/goecma262/compiler"
	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// ErrStepLimit is returned (possibly wrapped; test with errors.Is) when a match
// operation exceeds its execution budget. It is the same value as
// vm.ErrStepLimit, which documents the budget. It never means "no match": the
// input may or may not match, and the operation produced no result.
var ErrStepLimit = vm.ErrStepLimit

// ErrSurrogateSplit is returned for a result that has no Go form because it
// begins or ends between the two UTF-16 halves of a character above U+FFFF.
// Only patterns without the u or v flag, which match by UTF-16 code unit, can
// produce one: /^./ matches just the first half of "😀". See the package
// documentation for which results each method must express.
var ErrSurrogateSplit = errors.New("ecma262: result splits a character into UTF-16 surrogate halves")

// Regexp is the representation of a compiled ECMA-262 regular expression.
// Without the g or y flag it is safe for concurrent use by multiple
// goroutines. With g or y, MatchString, Match and FindStringSubmatch read
// and update the instance's lastIndex cursor (as SetLastIndex does), making
// the instance stateful; callers are responsible for synchronization.
type Regexp struct {
	expr      string
	flags     flags.Flags
	code      []vm.Instruction
	prog      *vm.Program // analysis of code, shared by every match operation
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

// Syntax selects how the compiler treats the legacy web-compatibility
// extensions (ECMA-262 Annex B).
type Syntax int

const (
	// SyntaxAnnexB accepts the Annex B web-compatibility extensions that
	// browsers apply to non-Unicode patterns: legacy octal escapes,
	// out-of-range numeric backreferences (as octal/literal), an invalid \c as
	// a literal, and a malformed {..} quantifier as literal characters. This is
	// the default, so a pattern that works in JavaScript's RegExp works here.
	SyntaxAnnexB Syntax = iota
	// SyntaxStrict rejects those constructs as compile errors, matching strict
	// ECMA-262. The u and v flags always force strict behavior regardless of
	// this setting (Annex B does not apply in Unicode mode).
	SyntaxStrict
)

type config struct {
	syntax Syntax
}

// Option configures a Compile call.
type Option func(*config)

// WithSyntax selects strict ECMA-262 or the default Annex B web-compatibility
// behavior. See Syntax.
func WithSyntax(s Syntax) Option {
	return func(c *config) { c.syntax = s }
}

// Compile parses a regular expression and returns, if successful, a Regexp
// object that can be used to match against text.
func Compile(expr string, f flags.Flags, opts ...Option) (*Regexp, error) {
	cfg := config{syntax: SyntaxAnnexB}
	for _, o := range opts {
		o(&cfg)
	}

	// Validate flag combinations before parsing so Compile and CompileFlags
	// reject the same inputs (u and v are mutually exclusive per ECMA-262).
	if f.Has(flags.Unicode) && f.Has(flags.UnicodeSets) {
		return nil, fmt.Errorf("incompatible flags: u and v")
	}

	// Annex B leniencies apply only outside Unicode mode.
	annexB := cfg.syntax == SyntaxAnnexB && !f.Has(flags.Unicode) && !f.Has(flags.UnicodeSets)

	// Parse the regex
	p := parser.New(expr, parser.Flags{
		IgnoreCase:  f.Has(flags.IgnoreCase),
		Unicode:     f.Has(flags.Unicode),
		UnicodeSets: f.Has(flags.UnicodeSets),
		DotAll:      f.Has(flags.DotAll),
		Multiline:   f.Has(flags.Multiline),
		AnnexB:      annexB,
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

	// Extract group names from AST, indexed by the parse-time group number so
	// that len(names) == numGroups+1 always holds.
	names := extractGroupNames(ast.Body, numGroups)

	return &Regexp{
		expr:  expr,
		flags: f,
		code:  code,
		prog: vm.NewProgram(code, f.Has(flags.IgnoreCase), f.Has(flags.Multiline), f.Has(flags.DotAll),
			f.Has(flags.Unicode) || f.Has(flags.UnicodeSets), false),
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
func MustCompile(expr string, f flags.Flags, opts ...Option) *Regexp {
	re, err := Compile(expr, f, opts...)
	if err != nil {
		panic(`regexp: Compile(` + quote(expr) + `): ` + err.Error())
	}
	return re
}

// CompileFlags is a convenience function that compiles a regex with flags from a string
func CompileFlags(expr string, flagStr string, opts ...Option) (*Regexp, error) {
	f, err := flags.Parse(flagStr)
	if err != nil {
		return nil, err
	}
	return Compile(expr, f, opts...)
}

// MatchString reports whether the string s contains any match of the regular expression.
// For patterns with the g or y flag, matching starts at re.lastIndex, and
// lastIndex is advanced to the end of the match (or reset to 0 on no match),
// mirroring JavaScript RegExp.prototype.test so repeated calls iterate.
func (re *Regexp) MatchString(s string) bool {
	if !re.global && !re.sticky {
		return re.doMatch(s, 0) != nil
	}
	groups := re.doMatch(s, re.lastIndex)
	if groups == nil || len(groups) < 2 {
		re.lastIndex = 0
		return false
	}
	re.lastIndex = groups[1]
	return true
}

// SetMaxSteps sets the maximum VM instruction steps for each match operation
// (a whole scan over start positions, lookarounds included). A value of 0
// restores the default, vm.DefaultMaxSteps plus vm.DefaultStepsPerByte per
// byte of input, which grows with the input so that linear-time searches are
// not cut off on long inputs; a positive value is a fixed limit regardless of
// input length.
//
// The step limit bounds backtracking as a ReDoS protection. When a match
// operation exceeds it, the methods without an error result (MatchString,
// FindString, FindAllString, ReplaceAllString, ...) report it as "no match" —
// they cannot distinguish a limit hit from a genuine non-match. Use the Err
// forms (MatchStringErr, FindStringSubmatchIndexErr,
// FindAllStringSubmatchIndexErr, ReplaceAllStringErr, SplitErr, ...) to
// observe ErrStepLimit instead.
func (re *Regexp) SetMaxSteps(max int) {
	if max < 0 {
		max = 0
	}
	re.maxSteps = max
}

// SetLastIndex sets the starting position for the next match operation.
// n is a byte offset into the input string, not a UTF-16 code-unit index
// as in JavaScript. Without the u or v flag a match can end between the two
// UTF-16 halves of a character above U+FFFF, which has no byte offset; that
// position is the offset of the character's first byte plus 2, which lies
// inside its four-byte UTF-8 sequence, and SetLastIndex accepts it back. For
// patterns with the g (global) or y (sticky) flag,
// this determines where searching begins; MatchString and FindStringSubmatch
// then advance it past each match (or reset it to 0 on no match). For
// patterns without these flags, lastIndex is ignored.
// This method makes the Regexp instance stateful; callers are responsible
// for synchronization when using across goroutines.
func (re *Regexp) SetLastIndex(n int) {
	re.lastIndex = n
}

// LastIndex returns the current lastIndex value: a byte offset into the input,
// or a position between the halves of a surrogate pair as described at
// SetLastIndex.
func (re *Regexp) LastIndex() int {
	return re.lastIndex
}

// MatchStringErr reports whether the string s contains any match of the regular expression.
// If matching exceeds the execution budget, it returns ErrStepLimit.
// Unlike MatchString, it always searches from the start of s: lastIndex is
// neither consulted nor updated, even with the g or y flag.
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
// If matching exceeds the execution budget, it returns ErrStepLimit.
func (re *Regexp) MatchErr(b []byte) (bool, error) {
	return re.MatchStringErr(string(b))
}

// MatchReader reports whether the text returned by the RuneReader contains any match.
// It reads the entire input into memory before matching, so it does not
// provide streaming behavior for large inputs.
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
	if len(groups) < 2 || !re.textOK(s, groups[0], groups[1]) {
		return ""
	}
	return s[groups[0]:groups[1]]
}

// Find returns a slice holding the text of the leftmost match in b of the regular expression.
// A return value of nil indicates no match.
func (re *Regexp) Find(b []byte) []byte {
	s := string(b)
	groups := re.doMatch(s, 0)
	if groups == nil {
		return nil
	}
	if len(groups) < 2 || !re.textOK(s, groups[0], groups[1]) {
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
	if groups == nil || len(groups) < 2 || !re.indicesOK(s, groups[:2]) {
		return nil
	}
	return []int{groups[0], groups[1]}
}

// FindStringIndexErr returns the location of the leftmost match in s.
// If matching exceeds the execution budget, it returns ErrStepLimit, and if
// the match begins or ends between the halves of a surrogate pair,
// ErrSurrogateSplit.
func (re *Regexp) FindStringIndexErr(s string) ([]int, error) {
	groups, err := re.doMatchWithError(s, 0)
	if err != nil {
		return nil, err
	}
	if groups == nil || len(groups) < 2 {
		return nil, nil
	}
	if !re.indicesOK(s, groups[:2]) {
		return nil, ErrSurrogateSplit
	}
	return []int{groups[0], groups[1]}, nil
}

// FindStringSubmatch returns a slice of strings holding the text of the leftmost
// match of the regular expression in s and the matches, if any, of its subexpressions.
// For patterns with the g or y flag, matching starts at re.lastIndex, and
// lastIndex is advanced to the end of the match (or reset to 0 on no match),
// mirroring JavaScript RegExp.prototype.exec so repeated calls iterate.
// A return value of nil indicates no match; a match whose text has no Go form
// (see ErrSurrogateSplit) is reported the same way, and resets lastIndex.
func (re *Regexp) FindStringSubmatch(s string) []string {
	startPos := 0
	if re.global || re.sticky {
		startPos = re.lastIndex
	}
	groups := re.doMatch(s, startPos)
	if groups == nil || !re.textsOK(s, groups) {
		if re.global || re.sticky {
			re.lastIndex = 0
		}
		return nil
	}
	if (re.global || re.sticky) && len(groups) >= 2 {
		re.lastIndex = groups[1]
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

// submatchIndex converts a raw capture-group slice into the standard
// index-pair form: a slice of length (numGroups+1)*2 where entry 2i/2i+1 is the
// [start, end) byte range of group i, or -1/-1 if the group did not participate.
//
// These index pairs are what JavaScript's `d` (hasIndices) flag exposes. In this
// API the index-returning methods are always available, so the `d` flag is not
// required to obtain them (it is accepted for compatibility but has no effect).
func (re *Regexp) submatchIndex(groups []int) []int {
	result := make([]int, (re.numGroups+1)*2)
	for i := range result {
		result[i] = -1
	}
	for i := 0; i <= re.numGroups; i++ {
		if i*2+1 < len(groups) {
			result[i*2] = groups[i*2]
			result[i*2+1] = groups[i*2+1]
		}
	}
	return result
}

// FindStringSubmatchIndex returns a slice holding the index pairs identifying the
// leftmost match of the regular expression in s and the matches, if any, of its subexpressions.
// A return value of nil indicates no match.
func (re *Regexp) FindStringSubmatchIndex(s string) []int {
	groups := re.doMatch(s, 0)
	if groups == nil || !re.indicesOK(s, groups) {
		return nil
	}
	return re.submatchIndex(groups)
}

// FindSubmatchIndex returns a slice holding the index pairs identifying the
// leftmost match of the regular expression in b and the matches, if any, of its
// subexpressions. A return value of nil indicates no match.
func (re *Regexp) FindSubmatchIndex(b []byte) []int {
	return re.FindStringSubmatchIndex(string(b))
}

// FindStringSubmatchIndexErr is like FindStringSubmatchIndex, but if matching
// exceeds the execution budget it returns ErrStepLimit instead of reporting
// no match, and if a position it would report falls between the halves of a
// surrogate pair, ErrSurrogateSplit. Like FindStringSubmatchIndex it searches
// from the start of s, ignoring lastIndex.
func (re *Regexp) FindStringSubmatchIndexErr(s string) ([]int, error) {
	groups, err := re.doMatchWithError(s, 0)
	if err != nil || groups == nil {
		return nil, err
	}
	if !re.indicesOK(s, groups) {
		return nil, ErrSurrogateSplit
	}
	return re.submatchIndex(groups), nil
}

// FindAllStringIndex returns a slice of all successive match locations of the
// regular expression, each as a two-element {start, end} slice of byte offsets.
// A return value of nil indicates no match.
func (re *Regexp) FindAllStringIndex(s string, n int) [][]int {
	matches := re.findAllMatches(s, n, re.matchIndicesOK(s))
	if len(matches) == 0 {
		return nil
	}
	result := make([][]int, len(matches))
	for i, g := range matches {
		result[i] = []int{g[0], g[1]}
	}
	return result
}

// FindAllIndex is the []byte version of FindAllStringIndex.
// A return value of nil indicates no match.
func (re *Regexp) FindAllIndex(b []byte, n int) [][]int {
	return re.FindAllStringIndex(string(b), n)
}

// FindAllStringSubmatchIndex returns a slice of all successive matches, each as
// a slice of index pairs for the match and its subexpressions (the same form as
// FindStringSubmatchIndex). A return value of nil indicates no match.
func (re *Regexp) FindAllStringSubmatchIndex(s string, n int) [][]int {
	matches := re.findAllMatches(s, n, re.allIndicesOK(s))
	if len(matches) == 0 {
		return nil
	}
	result := make([][]int, len(matches))
	for i, g := range matches {
		result[i] = re.submatchIndex(g)
	}
	return result
}

// FindAllSubmatchIndex is the []byte version of FindAllStringSubmatchIndex.
// A return value of nil indicates no match.
func (re *Regexp) FindAllSubmatchIndex(b []byte, n int) [][]int {
	return re.FindAllStringSubmatchIndex(string(b), n)
}

// FindAllStringSubmatchIndexErr is like FindAllStringSubmatchIndex, but if
// matching exceeds the execution budget it returns ErrStepLimit and no matches,
// rather than the matches found before the budget ran out, and likewise
// ErrSurrogateSplit if a position it would report falls between the halves of
// a surrogate pair. The budget applies to each successive match search.
func (re *Regexp) FindAllStringSubmatchIndexErr(s string, n int) ([][]int, error) {
	matches, err := re.findAllMatchesErr(s, n, re.allIndicesOK(s))
	if err != nil || len(matches) == 0 {
		return nil, err
	}
	result := make([][]int, len(matches))
	for i, g := range matches {
		result[i] = re.submatchIndex(g)
	}
	return result, nil
}

// findAllMatches returns the capture-group slices of every successive match,
// scanning left to right. It is the single source of truth for how the search
// cursor advances, so every iterating method (FindAll*, ReplaceAll*, Split)
// shares identical, correct behavior — in particular for zero-width matches.
//
// doMatch returns the leftmost match at or after the requested start, so a
// match may begin ahead of the cursor (e.g. a lookahead assertion). After each
// match the cursor advances to matchEnd, and for an empty match it advances one
// further character past matchEnd (a code unit without u or v, as in
// JavaScript) so the same zero-width position is not re-reported.
// limit < 0 means unlimited; limit == 0 returns no matches.
//
// ok, if not nil, reports whether the caller can express a match (see
// indicesOK and textsOK). If a search exceeds the execution budget, or a match
// fails ok, iteration stops there and the matches found so far are returned;
// findAllMatchesErr reports the error.
func (re *Regexp) findAllMatches(s string, limit int, ok func([]int) bool) [][]int {
	matches, _ := re.findAllMatchesErr(s, limit, ok)
	return matches
}

// findAllMatchesErr is findAllMatches, also returning the error that stopped
// the iteration, if any (with the matches found before it): ErrStepLimit, or
// ErrSurrogateSplit for a match that fails ok.
func (re *Regexp) findAllMatchesErr(s string, limit int, ok func([]int) bool) ([][]int, error) {
	if limit == 0 {
		return nil, nil
	}
	var matches [][]int
	searchStart := 0
	for limit < 0 || len(matches) < limit {
		if searchStart > len(s) {
			break
		}
		groups, err := re.doMatchWithError(s, searchStart)
		if err != nil {
			return matches, err
		}
		if groups == nil || len(groups) < 2 {
			break
		}
		if ok != nil && !ok(groups) {
			return matches, ErrSurrogateSplit
		}
		matches = append(matches, groups)

		matchStart, matchEnd := groups[0], groups[1]
		if matchEnd > matchStart {
			searchStart = matchEnd
		} else {
			// Empty match: advance one character past the match position.
			if matchEnd >= len(s) {
				break
			}
			searchStart = vm.NextPosition(s, matchEnd, re.unicode)
		}
	}
	return matches, nil
}

// FindAllString returns a slice of all successive matches of the regular expression.
// A return value of nil indicates no match.
func (re *Regexp) FindAllString(s string, n int) []string {
	matches := re.findAllMatches(s, n, func(g []int) bool { return re.textOK(s, g[0], g[1]) })
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
	matches := re.findAllMatches(s, n, func(g []int) bool { return re.textsOK(s, g) })
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
	matches := re.findAllMatches(src, limit, nil)
	out, ok := re.replaceMatches(src, repl, matches)
	if !ok {
		return src
	}
	return out
}

// ReplaceAllStringErr is like ReplaceAllString, but if matching exceeds the
// execution budget it returns ErrStepLimit and an empty string, rather than a
// copy of src in which only the matches found before the budget ran out were
// replaced, and it returns ErrSurrogateSplit where ReplaceAllString returns
// src unchanged.
func (re *Regexp) ReplaceAllStringErr(src, repl string) (string, error) {
	limit := -1
	if !re.global {
		limit = 1
	}
	matches, err := re.findAllMatchesErr(src, limit, nil)
	if err != nil {
		return "", err
	}
	out, ok := re.replaceMatches(src, repl, matches)
	if !ok {
		return "", ErrSurrogateSplit
	}
	return out, nil
}

// replaceMatches expands repl for each match and splices the results into
// src. ok is false if the result holds half of a surrogate pair (see splicer).
func (re *Regexp) replaceMatches(src, repl string, matches [][]int) (out string, ok bool) {
	b := &splicer{src: src, unicode: re.unicode}
	lastEnd := 0
	for _, groups := range matches {
		b.text(lastEnd, groups[0])
		re.expandRepl(repl, src, groups, b)
		lastEnd = groups[1]
	}
	b.text(lastEnd, len(src))
	return b.result()
}

// splicer assembles a replacement result from literal text and pieces of src.
// Without u or v a piece may begin or end between the two halves of a
// surrogate pair (see vm.BetweenSurrogates). The pieces are then joined as
// JavaScript concatenates UTF-16 strings: one that ends in a high surrogate
// and one that starts with a low surrogate form a pair again. A half left
// unpaired has no UTF-8 form, and result reports false.
type splicer struct {
	src     string
	unicode bool
	sb      strings.Builder
	high    rune // the high surrogate the last piece ended with, or 0
	broken  bool // a half was left unpaired
}

func (b *splicer) byte(c byte) {
	b.unpaired()
	b.sb.WriteByte(c)
}

func (b *splicer) literal(s string) {
	if s != "" {
		b.unpaired()
		b.sb.WriteString(s)
	}
}

// unpaired leaves the pending high surrogate, if any, without its low half.
func (b *splicer) unpaired() {
	if b.high != 0 {
		b.broken, b.high = true, 0
	}
}

// text appends src[start:end].
func (b *splicer) text(start, end int) {
	if start >= end {
		return
	}
	if b.unicode {
		b.sb.WriteString(b.src[start:end])
		return
	}
	if vm.BetweenSurrogates(b.src, start) {
		_, lo, first := pairAt(b.src, start)
		if b.high != 0 {
			b.sb.WriteRune(utf16.DecodeRune(b.high, lo))
			b.high = 0
		} else {
			b.broken = true
		}
		start = first + 4
	} else {
		b.unpaired()
	}
	if vm.BetweenSurrogates(b.src, end) {
		hi, _, first := pairAt(b.src, end)
		b.sb.WriteString(b.src[start:first])
		b.high = hi
		return
	}
	b.sb.WriteString(b.src[start:end])
}

func (b *splicer) result() (string, bool) {
	b.unpaired()
	return b.sb.String(), !b.broken
}

// pairAt returns the surrogate halves of the character that pos falls between
// (see vm.BetweenSurrogates) and the offset of its first byte.
func pairAt(s string, pos int) (hi, lo rune, first int) {
	first = pos - 2
	r, _ := utf8.DecodeRuneInString(s[first:])
	hi, lo = utf16.EncodeRune(r)
	return hi, lo, first
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
	matches, err := re.findAllMatchesErr(src, limit, func(g []int) bool { return re.textOK(src, g[0], g[1]) })
	if errors.Is(err, ErrSurrogateSplit) {
		return src
	}

	b := &splicer{src: src, unicode: re.unicode}
	lastEnd := 0
	for _, groups := range matches {
		b.text(lastEnd, groups[0])
		b.literal(fn(src[groups[0]:groups[1]]))
		lastEnd = groups[1]
	}
	b.text(lastEnd, len(src))
	out, ok := b.result()
	if !ok {
		return src
	}
	return out
}

// ReplaceAllFunc returns a copy of src in which all matches of the regexp
// have been replaced by the return value of the function fn applied to the matched byte slice
func (re *Regexp) ReplaceAllFunc(src []byte, fn func([]byte) []byte) []byte {
	return []byte(re.ReplaceAllStringFunc(string(src), func(s string) string {
		return string(fn([]byte(s)))
	}))
}

// expandRepl expands $ references in the replacement string into result.
func (re *Regexp) expandRepl(repl, src string, groups []int, result *splicer) {
	i := 0
	for i < len(repl) {
		if repl[i] != '$' {
			result.byte(repl[i])
			i++
			continue
		}

		// Found $
		i++ // skip $
		if i >= len(repl) {
			result.byte('$')
			break
		}

		switch repl[i] {
		case '$':
			// $$ → literal $
			result.byte('$')
			i++
		case '&':
			// $& → the matched text
			if len(groups) >= 2 && groups[0] >= 0 && groups[1] >= 0 {
				result.text(groups[0], groups[1])
			}
			i++
		case '`':
			// $` → text before the match
			if len(groups) >= 2 && groups[0] >= 0 {
				result.text(0, groups[0])
			}
			i++
		case '\'':
			// $' → text after the match
			if len(groups) >= 2 && groups[1] >= 0 {
				result.text(groups[1], len(src))
			}
			i++
		case '<':
			// $<name> → named capture group (per ECMA-262 GetSubstitution)
			nameStart := i + 1 // position right after '<'
			end := strings.IndexByte(repl[i+1:], '>')
			if end == -1 {
				// Unclosed $< — emit $< literally, continue from nameStart
				result.literal("$<")
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
					result.text(groups[groupIdx*2], groups[groupIdx*2+1])
				}
			} else {
				// No named groups: emit "$<" literally and re-process from nameStart.
				// This lets any $n references inside the name still expand.
				result.literal("$<")
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
						result.text(groups[chosen*2], groups[chosen*2+1])
					}
				} else {
					// Not a valid group reference (e.g. $0) — emit a literal $
					// and let the digits be copied verbatim on the next passes.
					result.byte('$')
				}
			} else {
				// Unknown $ sequence — emit literal $
				result.byte('$')
			}
		}
	}
}

// Split slices s into substrings separated by the expression and returns a
// slice of the substrings between those expression matches. It splits where
// JavaScript's String.prototype.split does: an empty match splits between
// characters but never at the start of s, at its end, or where the previous
// match ended, so an expression that can match the empty string splits s into
// characters, and splitting "" gives no substrings when the expression matches
// it. Unlike JavaScript, n follows Go's regexp: n > 0 returns at most n
// substrings, the last being the unsplit remainder; n == 0 returns nil; n < 0
// returns all of them. Capture groups are not interleaved into the result.
//
// If matching exceeds the execution budget, or a substring would begin or end
// between the halves of a surrogate pair (see ErrSurrogateSplit), Split stops
// splitting at the last separator that ends on a byte offset and returns the
// rest of s as the last substring; SplitErr reports it instead.
func (re *Regexp) Split(s string, n int) []string {
	parts, _ := re.split(s, n)
	return parts
}

// SplitErr is like Split, but if matching exceeds the execution budget it
// returns ErrStepLimit and no substrings, rather than splitting only at the
// matches found before the budget ran out, and likewise ErrSurrogateSplit if
// a substring would begin or end between the halves of a surrogate pair.
func (re *Regexp) SplitErr(s string, n int) ([]string, error) {
	parts, err := re.split(s, n)
	if err != nil {
		return nil, err
	}
	return parts, nil
}

// split implements ECMA-262's RegExp.prototype[@@split] position loop, which
// tries the separator at each position q in turn: a match at q that ends at p,
// where the current substring began, is empty and does not split, and no match
// is tried at the end of s. q advances by code unit without u or v, as in
// JavaScript, so a separator may begin or end between the halves of a
// surrogate pair; only a non-empty substring with such an edge has no Go form.
// On an exceeded budget, or such a substring, it returns the substrings up to
// the last separator that ended on a byte offset, with the rest of s from
// there as the last, and the error.
func (re *Regexp) split(s string, n int) ([]string, error) {
	if n == 0 {
		return nil, nil
	}
	if len(s) == 0 {
		groups, err := re.doMatchWithError(s, 0)
		if err != nil {
			return []string{s}, err
		}
		if groups != nil {
			return []string{}, nil
		}
		return []string{s}, nil
	}
	var parts []string
	p, q := 0, 0
	// keep parts precede keepP, the last p that is a byte offset.
	keep, keepP := 0, 0
	fail := func(err error) ([]string, error) {
		return append(parts[:keep], s[keepP:]), err
	}
	for q < len(s) && (n < 0 || len(parts) < n-1) {
		groups, err := re.doMatchWithError(s, q)
		if err != nil {
			return fail(err)
		}
		if groups == nil {
			if !re.sticky {
				break
			}
			// The separator is matched at each position in turn; a sticky
			// search tries only q, so move on as JavaScript does.
			q = vm.NextPosition(s, q, re.unicode)
			continue
		}
		start, end := groups[0], groups[1]
		if start >= len(s) {
			break
		}
		if end == p {
			// Empty, where the substring began (so start == q == p).
			q = vm.NextPosition(s, q, re.unicode)
			continue
		}
		if !re.textOK(s, p, start) {
			return fail(ErrSurrogateSplit)
		}
		parts = append(parts, s[p:start])
		p, q = end, end
		if re.unicode || !vm.BetweenSurrogates(s, p) {
			keep, keepP = len(parts), p
		}
	}
	if !re.textOK(s, p, len(s)) {
		return fail(ErrSurrogateSplit)
	}
	return append(parts, s[p:]), nil
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

// doMatch performs the actual matching and returns the capture groups. An
// exceeded execution budget is reported as no match; see doMatchWithError.
func (re *Regexp) doMatch(s string, startPos int) []int {
	groups, _ := re.doMatchWithError(s, startPos)
	return groups
}

// doMatchWithError returns the capture groups of the leftmost match at or
// after startPos (exactly at startPos with the sticky flag), or nil if there is
// none. If the search exceeds its execution budget it returns ErrStepLimit
// (possibly wrapped) and no groups. Without u or v it tries every code unit, so
// a position in the groups may fall between the halves of a surrogate pair
// (see vm.BetweenSurrogates); each caller checks the ones it reports.
func (re *Regexp) doMatchWithError(s string, startPos int) ([]int, error) {
	// Clamp the start position. A negative lastIndex is treated as 0; a
	// start position beyond the input yields no match (ECMA-262 semantics),
	// never a panic.
	if startPos < 0 {
		startPos = 0
	}
	if startPos > len(s) {
		return nil, nil
	}

	v := &vm.VM{
		Code:       re.code,
		Program:    re.prog,
		NumGroups:  re.numGroups,
		IgnoreCase: re.ignoreCase,
		Multiline:  re.multiline,
		DotAll:     re.dotAll,
		Unicode:    re.unicode,
		MaxSteps:   re.maxSteps,
	}

	// Sticky flag: only attempt match at startPos (anchored)
	if re.sticky {
		matched, _, groups := v.MatchAt(s, startPos)
		if v.Err != nil {
			return nil, v.Err
		}
		if !matched {
			return nil, nil
		}
		return groups, nil
	}

	// Try successive start positions; they share one budget.
	for pos := startPos; pos <= len(s); {
		matched, _, groups := v.MatchAt(s, pos)
		if v.Err != nil {
			return nil, v.Err
		}
		if matched {
			return groups, nil
		}
		if pos >= len(s) {
			break
		}
		pos = vm.NextPosition(s, pos, re.unicode)
	}

	return nil, nil
}

// indicesOK reports whether every position in idx (-1 for a group that did
// not participate) is a byte offset, rather than the position between the
// halves of a surrogate pair that matching without u or v can reach.
func (re *Regexp) indicesOK(s string, idx []int) bool {
	if re.unicode {
		return true
	}
	for _, i := range idx {
		if vm.BetweenSurrogates(s, i) {
			return false
		}
	}
	return true
}

// textsOK reports whether the text of every group in groups is a Go string:
// empty, or with neither edge between the halves of a surrogate pair.
func (re *Regexp) textsOK(s string, groups []int) bool {
	if re.unicode {
		return true
	}
	for i := 0; i+1 < len(groups); i += 2 {
		if !re.textOK(s, groups[i], groups[i+1]) {
			return false
		}
	}
	return true
}

// matchIndicesOK and allIndicesOK are indicesOK for findAllMatches: of the
// whole match only, or of every group.
func (re *Regexp) matchIndicesOK(s string) func([]int) bool {
	return func(g []int) bool { return re.indicesOK(s, g[:2]) }
}

func (re *Regexp) allIndicesOK(s string) func([]int) bool {
	return func(g []int) bool { return re.indicesOK(s, g) }
}

func (re *Regexp) textOK(s string, start, end int) bool {
	return re.unicode || start == end || !vm.BetweenSurrogates(s, start) && !vm.BetweenSurrogates(s, end)
}

// Convenience functions

// Match reports whether the byte slice b contains any match of the regular
// expression pattern with the given flags. err is non-nil if the pattern does
// not compile, or if matching exceeds the execution budget; in the latter case
// errors.Is(err, ErrStepLimit) holds and matched carries no information.
func Match(pattern string, f flags.Flags, b []byte) (matched bool, err error) {
	return MatchString(pattern, f, string(b))
}

// MatchString reports whether the string s contains any match of the regular
// expression pattern with the given flags. err is non-nil if the pattern does
// not compile, or if matching exceeds the execution budget; in the latter case
// errors.Is(err, ErrStepLimit) holds and matched carries no information.
//
// With the g or y flag, matching starts at lastIndex 0 of the freshly compiled
// Regexp, as for Regexp.MatchString.
func MatchString(pattern string, f flags.Flags, s string) (matched bool, err error) {
	re, err := Compile(pattern, f)
	if err != nil {
		return false, err
	}
	groups, err := re.doMatchWithError(s, 0)
	if err != nil {
		return false, err
	}
	return groups != nil, nil
}

// Helper functions

func quote(s string) string {
	if strings.ContainsAny(s, "`") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "`" + s + "`"
}

// extractGroupNames returns a slice of length numGroups+1 mapping each group
// index to its name ("" for unnamed groups and for group 0). Names are placed
// by the parse-time group index — the same numbering the compiler uses — so the
// result stays aligned with FindStringSubmatch/SubexpIndex, including for groups
// inside lookarounds and counted quantifiers.
func extractGroupNames(node parser.Node, numGroups int) []string {
	names := make([]string, numGroups+1)

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
			extract(v.Body)
		case *parser.NamedGroup:
			if v.Index >= 0 && v.Index < len(names) {
				names[v.Index] = v.Name
			}
			extract(v.Body)
		case *parser.NonCapturingGroup:
			extract(v.Body)
		case *parser.ModifierGroup:
			extract(v.Body)
		case *parser.Quantifier:
			extract(v.Body)
		case *parser.Lookahead:
			extract(v.Body)
		case *parser.NegativeLookahead:
			extract(v.Body)
		case *parser.Lookbehind:
			extract(v.Body)
		case *parser.NegativeLookbehind:
			extract(v.Body)
		}
	}

	extract(node)
	return names
}
