// Command difftest is a live differential fuzzer: it asks a real JavaScript
// engine (node) what it does with each generated case and requires this
// engine to agree exactly — on whether the pattern is valid, on the match
// position, on every capture group, and on replace and split results.
//
//	go run ./tools/difftest -n 200000 -seed 1
//	go run ./tools/difftest -cases cases.txt     # request lines from a file
//
// It speaks the line protocol of tools/difftest/fuzz-oracle.mjs, where every
// string travels as base64 of its UTF-16 code units.
//
// Positions are byte offsets here and UTF-16 indices in JavaScript, and are
// converted. A result with a position between the two halves of a surrogate
// pair (possible without u or v) has no byte offset; the engine reports it as
// ErrSurrogateSplit and the oracle as "U", and those must agree like any other
// answer.
//
// Two differences are inherent to a Go API and are counted, not reported:
//
//   - A pattern, input or replacement containing a lone surrogate has no
//     UTF-8 form, so it cannot be expressed as a Go string at all.
//   - A case this engine abandons because it exceeded its step budget has no
//     answer to compare. Such cases are screened out before node sees them,
//     since node has no budget and a catastrophic pattern would hang it.
//
// Split is compared only where Regexp.Split's documented contract and
// String.prototype.split coincide: a negative or zero limit, and a pattern
// without capture groups. Split's n follows Go's regexp (at most n substrings,
// the last being the unsplit remainder) where JavaScript truncates, and it
// does not interleave captures into the result.
//
// Known V8 defects, which the oracle marks with a prefix character, are
// skipped and counted; see fuzz-oracle.mjs and corpus.mjs for each one. So is
// a case node does not answer within -stall: V8 has no step budget, so a
// pattern this engine finishes cheaply can run in node indefinitely.
package main

import (
	"bufio"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf16"

	ecma262 "github.com/mgilbir/goecma262"
)

type testCase struct {
	op      byte // 'x' exec, 'a' matchAll, 'r' replace, 's' split
	pattern string
	flags   string
	input   string
	extra   string // replacement for 'r', decimal limit for 's'
}

func (c testCase) String() string {
	detail := ""
	switch c.op {
	case 'r':
		detail = " replace with " + strconv.Quote(c.extra)
	case 's':
		detail = " split limit=" + c.extra
	}
	return fmt.Sprintf("/%s/%s on %s [%c]%s", c.pattern, c.flags, strconv.Quote(c.input), c.op, detail)
}

// ---------------------------------------------------------------- transport

func encode(s string) string {
	units := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(units))
	for i, u := range units {
		b[2*i] = byte(u)
		b[2*i+1] = byte(u >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func decodeUnits(s string) ([]uint16, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return u, nil
}

// unitsToString converts UTF-16 to a Go string, or reports false if the text
// contains a lone surrogate, which has no UTF-8 form.
func unitsToString(u []uint16) (string, bool) {
	for i := 0; i < len(u); i++ {
		c := u[i]
		switch {
		case c >= 0xD800 && c <= 0xDBFF:
			if i+1 >= len(u) || u[i+1] < 0xDC00 || u[i+1] > 0xDFFF {
				return "", false
			}
			i++
		case c >= 0xDC00 && c <= 0xDFFF:
			return "", false
		}
	}
	return string(utf16.Decode(u)), true
}

// unitIndex converts a byte offset in s to a UTF-16 index.
func unitIndex(s string, byteOff int) int {
	n := 0
	for _, r := range s[:byteOff] {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// ------------------------------------------------------------- generation

var (
	atoms = []string{
		"a", "b", "c", "x", ".", `\d`, `\D`, `\w`, `\W`, `\s`, `\S`,
		"[ab]", "[^a]", "[a-c]", `[\d-]`, `[^\w]`, `\b`, `\B`, "^", "$",
		`\n`, `\t`, `\.`, "-", "_", "1", " ",
	}
	quants = []string{
		"", "", "", "", "*", "+", "?", "*?", "+?", "??",
		"{2}", "{1,2}", "{0,2}", "{2,}", "{1,3}?",
	}
	flagSets = []string{"", "", "i", "m", "s", "u", "iu", "ms", "im", "is", "su", "imsu", "v", "vi", "vs"}
	// Class-set fragments, only meaningful under the v flag.
	classSetAtoms = []string{
		"[[a][b]]", "[a--b]", "[[a-z]--[aeiou]]", "[a&&b]", "[[a-c]&&[b-d]]",
		`[\q{ab}]`, `[\q{a|bc}]`, `[\q{}]`, `[a\q{ab}]`, "[^[a][b]]",
		`[\p{L}--\p{Lu}]`, "[&]", `[\-]`, `[\q{ab|a}]`,
		`\p{RGI_Emoji}`, `[\p{Basic_Emoji}]`, `[\p{Emoji_Keycap_Sequence}\q{ab}]`,
	}
	inputAlphabet = []rune("aabbccxyz01 _-\n\t.äÄſKΣσ😀️⃣‍#")
	// Characters that appear in regex syntax. Random strings over this
	// alphabet reach grammar the structured generator does not know about.
	syntaxSoup   = "abc019 _-^$.*+?()[]{}|/\\<>=!:,&#~%@`'\"wWdDsSbBpPkqQuUxXvVnrtf"
	replacements = []string{
		"X", "", "-", "$&", "[$&]", "$1", "$2", "$12", "$0", "$99", "$<g0>",
		"$<nope>", "$`", "$'", "$$", "$$1", "a$&b$1c", "$<g1>$&",
	}
	splitLimits = []string{"-1", "-1", "-1", "0", "1", "2", "3", "5"}
)

func pick[T any](r *rand.Rand, s []T) T { return s[r.Intn(len(s))] }

func quantifiable(atom string) bool {
	return atom != "^" && atom != "$" && atom != `\b` && atom != `\B`
}

func genPattern(r *rand.Rand, depth int, classSets bool) string {
	x := r.Float64()
	if depth <= 0 || x < 0.42 {
		pool := atoms
		if classSets && r.Intn(3) == 0 {
			pool = classSetAtoms
		}
		a := pick(r, pool)
		if quantifiable(a) {
			a += pick(r, quants)
		}
		return a
	}
	sub := func() string { return genPattern(r, depth-1, classSets) }
	switch {
	case x < 0.58:
		return sub() + sub()
	case x < 0.70:
		return "(" + sub() + "|" + sub() + ")" + pick(r, quants)
	case x < 0.79:
		return "(" + sub() + ")" + pick(r, quants)
	case x < 0.85:
		return "(?:" + sub() + ")" + pick(r, quants)
	case x < 0.86:
		return fmt.Sprintf("(?<g%d>", r.Intn(3)) + sub() + ")"
	case x < 0.88:
		all := []string{"i", "m", "s"}
		r.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
		nAdd := r.Intn(3)
		add := strings.Join(all[:nAdd], "")
		rest := all[nAdd:]
		nRemove := r.Intn(2)
		if nRemove > len(rest) {
			nRemove = len(rest)
		}
		remove := strings.Join(rest[:nRemove], "")
		spec := add
		if remove != "" {
			spec = add + "-" + remove
		}
		return "(?" + spec + ":" + sub() + ")"
	case x < 0.91:
		return "(?=" + sub() + ")"
	case x < 0.94:
		return "(?!" + sub() + ")"
	case x < 0.96:
		return "(?<=" + sub() + ")"
	case x < 0.98:
		return "(?<!" + sub() + ")"
	default:
		return "(" + sub() + `)\1`
	}
}

func genJunkPattern(r *rand.Rand) string {
	n := 1 + r.Intn(13)
	b := make([]byte, n)
	for i := range b {
		b[i] = syntaxSoup[r.Intn(len(syntaxSoup))]
	}
	return string(b)
}

func genInput(r *rand.Rand) string {
	n := r.Intn(12)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteRune(pick(r, inputAlphabet))
	}
	return sb.String()
}

func genCase(r *rand.Rand) testCase {
	fl := pick(r, flagSets)
	var pattern string
	if r.Intn(8) == 0 {
		pattern = genJunkPattern(r)
	} else {
		pattern = genPattern(r, 3, strings.Contains(fl, "v"))
	}
	switch r.Intn(8) {
	case 0, 1:
		if !strings.Contains(fl, "g") {
			fl += "g"
		}
		return testCase{op: 'a', pattern: pattern, flags: fl, input: genInput(r)}
	case 2, 3:
		return testCase{op: 'r', pattern: pattern, flags: fl, input: genInput(r), extra: pick(r, replacements)}
	case 4:
		return testCase{op: 's', pattern: pattern, flags: fl, input: genInput(r), extra: pick(r, splitLimits)}
	default:
		return testCase{op: 'x', pattern: pattern, flags: fl, input: genInput(r)}
	}
}

// ---------------------------------------------------------------- running

// modifierGroup finds a regexp modifier group such as (?i:, (?-m: or (?s-i:.
var modifierGroup = regexp.MustCompile(`\(\?[ims]*(-[ims]*)?:`)

// splitContract marks a split case outside the comparable contract; see the
// package comment.
const splitContract = "S?"

// run computes this engine's answer in the oracle's response format.
func run(c testCase) (string, error) {
	re, err := ecma262.CompileFlags(c.pattern, c.flags)
	if err != nil {
		return "E", nil
	}
	s := c.input
	renderMatch := func(idx []int) string {
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d %d", unitIndex(s, idx[0]), len(idx)/2)
		for g := 0; g < len(idx)/2; g++ {
			if idx[2*g] < 0 {
				sb.WriteString(" -")
			} else {
				sb.WriteString(" " + encode(s[idx[2*g]:idx[2*g+1]]))
			}
		}
		return sb.String()
	}
	switch c.op {
	case 'x':
		idx, err := re.FindStringSubmatchIndexErr(s)
		if err != nil {
			return "", err
		}
		if idx == nil {
			return "N", nil
		}
		return "M " + renderMatch(idx), nil
	case 'a':
		all, err := re.FindAllStringSubmatchIndexErr(s, -1)
		if err != nil {
			return "", err
		}
		parts := make([]string, len(all))
		for i, m := range all {
			parts[i] = renderMatch(m)
		}
		return fmt.Sprintf("A %d %s", len(all), strings.Join(parts, " | ")), nil
	case 'r':
		out, err := re.ReplaceAllStringErr(s, c.extra)
		if err != nil {
			return "", err
		}
		return "R " + encode(out), nil
	default:
		limit, _ := strconv.Atoi(c.extra)
		if limit > 0 || re.NumSubexp() > 0 {
			return splitContract, nil
		}
		parts, err := re.SplitErr(s, limit)
		if err != nil {
			return "", err
		}
		enc := make([]string, len(parts))
		for i, p := range parts {
			enc[i] = encode(p)
		}
		return fmt.Sprintf("S %d %s", len(parts), strings.Join(enc, " ")), nil
	}
}

// ---------------------------------------------------------------- main

func main() {
	n := flag.Int("n", 20000, "number of random cases")
	seed := flag.Int64("seed", 1, "random seed")
	casesFile := flag.String("cases", "", "read request lines from this file instead of generating")
	oracle := flag.String("oracle", "tools/difftest/fuzz-oracle.mjs", "oracle script")
	show := flag.Int("show", 60, "failures to print")
	stall := flag.Duration("stall", 10*time.Second, "kill node and skip the case after this long without an answer")
	only := flag.String("only", "", "restrict to these ops (e.g. xa)")
	noV := flag.Bool("nov", false, "drop cases using the v flag")
	noMod := flag.Bool("nomod", false, "drop patterns containing a modifier group")
	flag.Parse()

	var cases []testCase
	unrepresentable := 0
	if *casesFile != "" {
		f, err := os.Open(*casesFile)
		if err != nil {
			fatal(err)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			fields := strings.Split(sc.Text(), " ")
			if len(fields) < 4 {
				continue
			}
			var strs [4]string
			ok := true
			for i := 1; i < len(fields) && i <= 4; i++ {
				u, err := decodeUnits(fields[i])
				if err != nil {
					fatal(err)
				}
				s, good := unitsToString(u)
				ok = ok && good
				strs[i-1] = s
			}
			if !ok {
				unrepresentable++
				continue
			}
			cases = append(cases, testCase{op: fields[0][0], pattern: strs[0], flags: strs[1], input: strs[2], extra: strs[3]})
		}
		f.Close()
	} else {
		r := rand.New(rand.NewSource(*seed))
		for i := 0; i < *n; i++ {
			cases = append(cases, genCase(r))
		}
	}
	{
		kept := cases[:0]
		for _, c := range cases {
			if *only != "" && strings.IndexByte(*only, c.op) < 0 {
				continue
			}
			if *noV && strings.Contains(c.flags, "v") {
				continue
			}
			if *noMod && modifierGroup.MatchString(c.pattern) {
				continue
			}
			kept = append(kept, c)
		}
		cases = kept
	}

	// Screen: compute our answers first, dropping cases we cannot finish.
	ours := make([]string, 0, len(cases))
	kept := cases[:0]
	screened := 0
	for _, c := range cases {
		out, err := run(c)
		if errors.Is(err, ecma262.ErrSurrogateSplit) {
			out, err = "U", nil
		}
		if err != nil {
			screened++
			continue
		}
		kept = append(kept, c)
		ours = append(ours, out)
	}
	cases = kept

	theirs := askOracle(*oracle, cases, *stall)

	skips := map[byte]int{}
	var failures []string
	byKind := map[string]int{}
	noGoForm := 0 // agreed answers that were "U"
	for i, c := range cases {
		exp := theirs[i]
		if exp == "T" || exp == oracleHung {
			skips[exp[0]]++
			continue
		}
		if strings.ContainsRune("!~%&@", rune(exp[0])) {
			skips[exp[0]]++
			continue
		}
		got := ours[i]
		if got == splitContract {
			skips['S']++
			continue
		}
		if strings.TrimRight(got, " ") == strings.TrimRight(exp, " ") {
			if got == "U" {
				noGoForm++
			}
			continue
		}
		kind := classify(c, got, exp)
		byKind[kind]++
		failures = append(failures, fmt.Sprintf("[%s] %s\n    node: %s\n    ours: %s", kind, c, render(exp), render(got)))
	}

	fmt.Printf("%d cases compared (%d agreeing that the result has no Go form); %d unrepresentable (lone surrogate), %d screened (step budget)\n",
		len(cases), noGoForm, unrepresentable, screened)
	if len(skips) > 0 {
		fmt.Printf("skipped (V8 defects by oracle mark; S = split outside the Go contract; H = node hung; T = node threw): %v\n", fmtSkips(skips))
	}
	if len(failures) == 0 {
		fmt.Println("OK: every case agrees with node")
		return
	}
	fmt.Printf("FAIL: %d cases disagree\n", len(failures))
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return byKind[kinds[i]] > byKind[kinds[j]] })
	for _, k := range kinds {
		fmt.Printf("  %6d %s\n", byKind[k], k)
	}
	fmt.Println()
	for i, f := range failures {
		if i >= *show {
			fmt.Printf("... and %d more\n", len(failures)-*show)
			break
		}
		fmt.Println(f)
	}
	os.Exit(1)
}

func fmtSkips(m map[byte]int) string {
	var parts []string
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%c=%d", k, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// render decodes base64 fields in a response for display.
func render(resp string) string {
	fs := strings.Fields(resp)
	for i, f := range fs {
		if i == 0 || f == "-" || f == "|" {
			continue
		}
		if _, err := strconv.Atoi(f); err == nil && (i == 1 || i == 2) {
			continue
		}
		if u, err := decodeUnits(f); err == nil {
			if s, ok := unitsToString(u); ok {
				fs[i] = strconv.Quote(s)
			}
		}
	}
	return strings.Join(fs, " ")
}

func classify(c testCase, got, exp string) string {
	switch {
	case exp == "E":
		return "we accept, node rejects"
	case got == "E":
		return "we reject, node accepts"
	}
	return fmt.Sprintf("%c differs", c.op)
}

// oracleHung marks a case V8 did not finish within the stall timeout.
const oracleHung = "H"

// askOracle returns node's answer for every case. V8 has no step limit and
// cannot be interrupted from JavaScript, so a pattern this engine finishes
// cheaply can still run in node indefinitely: when node stops answering for
// the stall timeout, it is killed, the case it was stuck on is recorded as
// oracleHung, and a fresh node carries on from the next case.
func askOracle(script string, cases []testCase, stall time.Duration) []string {
	var results []string
	for len(results) < len(cases) {
		rest := cases[len(results):]
		got := runOracle(script, rest, stall)
		results = append(results, got...)
		if len(got) < len(rest) {
			fmt.Fprintf(os.Stderr, "oracle stalled; skipping %v\n", rest[len(got)])
			results = append(results, oracleHung)
		}
	}
	return results
}

// runOracle answers cases in order with one node process, returning early if
// node stalls or exits.
func runOracle(script string, cases []testCase, stall time.Duration) []string {
	cmd := exec.Command("node", script)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fatal(err)
	}
	if err := cmd.Start(); err != nil {
		fatal(err)
	}
	go func() {
		w := bufio.NewWriter(stdin)
		for _, c := range cases {
			fmt.Fprintf(w, "%c %s %s %s", c.op, encode(c.pattern), encode(c.flags), encode(c.input))
			if c.op == 'r' || c.op == 's' {
				fmt.Fprintf(w, " %s", encode(c.extra))
			}
			if w.WriteByte('\n') != nil {
				break // node was killed
			}
		}
		w.Flush()
		stdin.Close()
	}()

	var progress atomic.Int64
	done := make(chan struct{})
	go func() {
		last := int64(-1)
		for {
			select {
			case <-done:
				return
			case <-time.After(stall):
			}
			now := progress.Load()
			if now == last {
				cmd.Process.Kill()
				return
			}
			last = now
		}
	}()

	var results []string
	rd := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := rd.ReadString('\n')
		if line = strings.TrimRight(line, "\n"); line != "" {
			results = append(results, line)
			progress.Add(1)
		}
		if err != nil {
			break
		}
	}
	close(done)
	cmd.Wait()
	return results
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "difftest:", err)
	os.Exit(2)
}
