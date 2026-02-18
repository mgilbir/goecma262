package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type regexLiteral struct {
	pattern string
	flags   string
}

type testCase struct {
	name      string
	file      string
	pattern   string
	flags     string
	input     string
	want      bool
	expect    string
	hasExpect bool
}

func main() {
	var test262Root string
	var outPath string
	flag.StringVar(&test262Root, "test262", "/tmp/test262", "path to test262 repo")
	flag.StringVar(&outPath, "out", "tests/test262_generated_test.go", "output Go test file")
	flag.Parse()

	root := filepath.Join(test262Root, "test", "built-ins", "RegExp")
	info, err := os.Stat(root)
	if err != nil {
		fatalf("test262 root not found: %v", err)
	}
	if !info.IsDir() {
		fatalf("test262 root is not a directory: %s", root)
	}

	cases, stats, err := collectCases(root)
	if err != nil {
		fatalf("collect: %v", err)
	}

	sort.Slice(cases, func(i, j int) bool {
		if cases[i].file == cases[j].file {
			return cases[i].name < cases[j].name
		}
		return cases[i].file < cases[j].file
	})

	if err := writeTests(outPath, cases, stats); err != nil {
		fatalf("write: %v", err)
	}

	fmt.Printf("generated %d cases (%d files scanned, %d skipped) -> %s\n", len(cases), stats.filesScanned, stats.casesSkipped, outPath)
}

type collectStats struct {
	filesScanned int
	casesSkipped int
}

func collectCases(root string) ([]testCase, collectStats, error) {
	var cases []testCase
	stats := collectStats{}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".js" {
			return nil
		}

		stats.filesScanned++
		fileCases, skipped, err := parseFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		stats.casesSkipped += skipped
		cases = append(cases, fileCases...)
		return nil
	})
	if err != nil {
		return nil, stats, err
	}
	return cases, stats, nil
}

func parseFile(path string) ([]testCase, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	content := string(data)

	regexVars := map[string]regexLiteral{}
	stringVars := map[string]string{}
	decls, err := extractRegexDeclarations(content)
	if err != nil {
		return nil, 0, err
	}
	for name, lit := range decls {
		regexVars[name] = lit
	}
	strDecls, err := extractStringDeclarations(content)
	if err != nil {
		return nil, 0, err
	}
	for name, val := range strDecls {
		stringVars[name] = val
	}

	args, err := extractAssertSameValueArgs(content)
	if err != nil {
		return nil, 0, err
	}

	fileCases := []testCase{}
	skipped := 0
	base := filepath.ToSlash(path)
	for i, arg := range args {
		c, ok, err := convertAssertArgs(arg, regexVars, stringVars)
		if err != nil {
			return nil, 0, fmt.Errorf("parse assert: %w", err)
		}
		if !ok {
			skipped++
			continue
		}
		c.file = base
		c.name = fmt.Sprintf("%s#%d", filepath.Base(path), i+1)
		fileCases = append(fileCases, c)
	}
	return fileCases, skipped, nil
}

func extractRegexDeclarations(content string) (map[string]regexLiteral, error) {
	decls := make(map[string]regexLiteral)
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "var ") && !strings.HasPrefix(line, "let ") && !strings.HasPrefix(line, "const ") {
			continue
		}
		name, lit, ok, err := parseRegexDeclaration(line)
		if err != nil {
			return nil, err
		}
		if ok {
			decls[name] = lit
			continue
		}
		name, lit, ok, err = parseRegExpConstructor(line)
		if err != nil {
			return nil, err
		}
		if ok {
			decls[name] = lit
		}
	}
	return decls, nil
}

func extractStringDeclarations(content string) (map[string]string, error) {
	decls := make(map[string]string)
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "var ") && !strings.HasPrefix(line, "let ") && !strings.HasPrefix(line, "const ") {
			continue
		}
		name, val, ok, err := parseStringDeclaration(line)
		if err != nil {
			return nil, err
		}
		if ok {
			decls[name] = val
		}
	}
	return decls, nil
}

func parseRegexDeclaration(line string) (string, regexLiteral, bool, error) {
	line = strings.TrimSuffix(line, ";")
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return "", regexLiteral{}, false, nil
	}
	name := parts[1]
	eqIdx := strings.Index(line, "=")
	if eqIdx == -1 {
		return "", regexLiteral{}, false, nil
	}
	after := strings.TrimSpace(line[eqIdx+1:])
	if !strings.HasPrefix(after, "/") {
		return "", regexLiteral{}, false, nil
	}
	pattern, flags, _, ok, err := parseRegexLiteral(after, 0)
	if err != nil {
		return "", regexLiteral{}, false, err
	}
	if !ok {
		return "", regexLiteral{}, false, nil
	}
	return name, regexLiteral{pattern: pattern, flags: flags}, true, nil
}

func parseRegExpConstructor(line string) (string, regexLiteral, bool, error) {
	line = strings.TrimSuffix(line, ";")
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return "", regexLiteral{}, false, nil
	}
	name := parts[1]
	eqIdx := strings.Index(line, "=")
	if eqIdx == -1 {
		return "", regexLiteral{}, false, nil
	}
	after := strings.TrimSpace(line[eqIdx+1:])
	if !strings.HasPrefix(after, "new RegExp(") {
		return "", regexLiteral{}, false, nil
	}
	args := strings.TrimSuffix(strings.TrimPrefix(after, "new RegExp("), ")")
	argsParts, ok := splitTopLevelComma(args)
	if !ok {
		argsParts = []string{strings.TrimSpace(args)}
	}
	patternStr, ok, err := parseJSStringLiteral(strings.TrimSpace(argsParts[0]))
	if err != nil || !ok {
		return "", regexLiteral{}, false, err
	}
	flags := ""
	if len(argsParts) > 1 {
		flags, ok, err = parseJSStringLiteral(strings.TrimSpace(argsParts[1]))
		if err != nil || !ok {
			return "", regexLiteral{}, false, err
		}
	}
	return name, regexLiteral{pattern: patternStr, flags: flags}, true, nil
}

func parseStringDeclaration(line string) (string, string, bool, error) {
	line = strings.TrimSuffix(line, ";")
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return "", "", false, nil
	}
	name := parts[1]
	eqIdx := strings.Index(line, "=")
	if eqIdx == -1 {
		return "", "", false, nil
	}
	after := strings.TrimSpace(line[eqIdx+1:])
	val, ok, err := parseJSStringLiteral(after)
	if err != nil || !ok {
		return "", "", false, err
	}
	return name, val, true, nil
}

func extractAssertSameValueArgs(content string) ([]string, error) {
	var args []string
	needle := "assert.sameValue("
	idx := 0
	for {
		pos := strings.Index(content[idx:], needle)
		if pos == -1 {
			break
		}
		start := idx + pos + len(needle)
		end, ok := findMatchingParen(content, start-1)
		if !ok {
			return nil, fmt.Errorf("unterminated assert.sameValue")
		}
		args = append(args, strings.TrimSpace(content[start:end]))
		idx = end + 1
	}
	return args, nil
}

func findMatchingParen(s string, openIdx int) (int, bool) {
	depth := 0
	inSingle := false
	inDouble := false
	escaped := false
	for i := openIdx; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle || inDouble {
			continue
		}
		if isRegexLiteralStart(s, i) {
			end := scanRegexLiteralEnd(s, i)
			if end == -1 {
				return -1, false
			}
			i = end
			continue
		}
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return -1, false
}

func convertAssertArgs(args string, regexVars map[string]regexLiteral, stringVars map[string]string) (testCase, bool, error) {
	parts, ok := splitTopLevelComma(args)
	if !ok || len(parts) < 2 {
		return testCase{}, false, nil
	}
	expr := strings.TrimSpace(parts[0])
	expected := strings.TrimSpace(parts[1])

	switch expected {
	case "true", "false":
		want := expected == "true"
		return parseTestExpr(expr, regexVars, stringVars, want)
	case "null":
		return parseExecNullExpr(expr, regexVars, stringVars)
	default:
		// Support assert.sameValue(re.exec(input)[0], "expected")
		expectedStr, ok, err := parseJSStringLiteral(strings.TrimSpace(expected))
		if err != nil || !ok {
			return testCase{}, false, err
		}
		return parseExecExpectedExpr(expr, regexVars, stringVars, expectedStr)
	}
}

func splitTopLevelComma(s string) ([]string, bool) {
	depth := 0
	inSingle := false
	inDouble := false
	escaped := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle || inDouble {
			continue
		}
		if isRegexLiteralStart(s, i) {
			end := scanRegexLiteralEnd(s, i)
			if end == -1 {
				return nil, false
			}
			i = end
			continue
		}
		switch ch {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				return []string{strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])}, true
			}
		}
	}
	return nil, false
}

func isRegexLiteralStart(s string, i int) bool {
	if s[i] != '/' {
		return false
	}
	if i+1 < len(s) && (s[i+1] == '/' || s[i+1] == '*') {
		return false
	}
	if i == 0 {
		return true
	}
	prev := s[i-1]
	if prev == '(' || prev == ',' || prev == '=' || prev == ':' || prev == '[' || prev == '{' || prev == '!' {
		return true
	}
	if prev == ' ' || prev == '\n' || prev == '\t' || prev == '\r' {
		return true
	}
	return false
}

func scanRegexLiteralEnd(s string, start int) int {
	i := start + 1
	inClass := false
	escaped := false
	for i < len(s) {
		ch := s[i]
		if escaped {
			escaped = false
			i++
			continue
		}
		if ch == '\\' {
			escaped = true
			i++
			continue
		}
		if ch == '[' {
			inClass = true
			i++
			continue
		}
		if ch == ']' {
			inClass = false
			i++
			continue
		}
		if ch == '/' && !inClass {
			return i
		}
		i++
	}
	return -1
}

func parseTestExpr(expr string, regexVars map[string]regexLiteral, stringVars map[string]string, want bool) (testCase, bool, error) {
	if strings.HasPrefix(expr, "/") {
		pattern, flags, rest, ok, err := parseRegexLiteral(expr, 0)
		if err != nil || !ok {
			return testCase{}, false, err
		}
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, ".test(") {
			return testCase{}, false, nil
		}
		inputExpr := strings.TrimSuffix(strings.TrimPrefix(rest, ".test("), ")")
		input, ok, err := parseInputExpr(strings.TrimSpace(inputExpr), stringVars)
		if err != nil || !ok {
			return testCase{}, false, err
		}
		return testCase{pattern: pattern, flags: flags, input: input, want: want}, true, nil
	}

	parts := strings.SplitN(expr, ".test(", 2)
	if len(parts) != 2 {
		return testCase{}, false, nil
	}
	name := strings.TrimSpace(parts[0])
	lit, ok := regexVars[name]
	if !ok {
		return testCase{}, false, nil
	}
	inputExpr := strings.TrimSuffix(parts[1], ")")
	input, ok, err := parseInputExpr(strings.TrimSpace(inputExpr), stringVars)
	if err != nil || !ok {
		return testCase{}, false, err
	}
	return testCase{pattern: lit.pattern, flags: lit.flags, input: input, want: want}, true, nil
}

func parseExecNullExpr(expr string, regexVars map[string]regexLiteral, stringVars map[string]string) (testCase, bool, error) {
	parts := strings.SplitN(expr, ".exec(", 2)
	if len(parts) != 2 {
		return testCase{}, false, nil
	}
	name := strings.TrimSpace(parts[0])
	lit, ok := regexVars[name]
	if !ok {
		return testCase{}, false, nil
	}
	inputExpr := strings.TrimSuffix(parts[1], ")")
	input, ok, err := parseInputExpr(strings.TrimSpace(inputExpr), stringVars)
	if err != nil || !ok {
		return testCase{}, false, err
	}
	return testCase{pattern: lit.pattern, flags: lit.flags, input: input, want: false}, true, nil
}

func parseExecExpectedExpr(expr string, regexVars map[string]regexLiteral, stringVars map[string]string, expected string) (testCase, bool, error) {
	if !strings.Contains(expr, ".exec(") || !strings.Contains(expr, ")[0]") {
		return testCase{}, false, nil
	}
	expr = strings.Replace(expr, ")[0]", ")", 1)
	parts := strings.SplitN(expr, ".exec(", 2)
	if len(parts) != 2 {
		return testCase{}, false, nil
	}
	name := strings.TrimSpace(parts[0])
	var lit regexLiteral
	if strings.HasPrefix(name, "/") {
		pattern, flags, rest, ok, err := parseRegexLiteral(name, 0)
		if err != nil || !ok {
			return testCase{}, false, err
		}
		if strings.TrimSpace(rest) != "" {
			return testCase{}, false, nil
		}
		lit = regexLiteral{pattern: pattern, flags: flags}
	} else {
		var ok bool
		lit, ok = regexVars[name]
		if !ok {
			return testCase{}, false, nil
		}
	}
	inputExpr := strings.TrimSuffix(parts[1], ")")
	input, ok, err := parseInputExpr(strings.TrimSpace(inputExpr), stringVars)
	if err != nil || !ok {
		return testCase{}, false, err
	}
	return testCase{pattern: lit.pattern, flags: lit.flags, input: input, want: true, expect: expected, hasExpect: true}, true, nil
}

func parseRegexLiteral(s string, start int) (string, string, string, bool, error) {
	if start >= len(s) || s[start] != '/' {
		return "", "", "", false, nil
	}
	i := start + 1
	patternStart := i
	inClass := false
	escaped := false
	for i < len(s) {
		ch := s[i]
		if escaped {
			escaped = false
			i++
			continue
		}
		if ch == '\\' {
			escaped = true
			i++
			continue
		}
		if ch == '[' {
			inClass = true
			i++
			continue
		}
		if ch == ']' {
			inClass = false
			i++
			continue
		}
		if ch == '/' && !inClass {
			pattern := s[patternStart:i]
			i++
			flagsStart := i
			for i < len(s) && isASCIIAlpha(s[i]) {
				i++
			}
			flags := s[flagsStart:i]
			return pattern, flags, s[i:], true, nil
		}
		i++
	}
	return "", "", "", false, errors.New("unterminated regex literal")
}

func parseJSStringLiteral(s string) (string, bool, error) {
	if len(s) < 2 {
		return "", false, nil
	}
	quote := s[0]
	if quote != '\'' && quote != '"' {
		return "", false, nil
	}
	if s[len(s)-1] != quote {
		return "", false, nil
	}
	content := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(content); i++ {
		ch := content[i]
		if ch != '\\' {
			b.WriteByte(ch)
			continue
		}
		if i+1 >= len(content) {
			return "", false, errors.New("unterminated escape")
		}
		i++
		switch content[i] {
		case '\\', '\'', '"':
			b.WriteByte(content[i])
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case 'f':
			b.WriteByte('\f')
		case 'b':
			b.WriteByte('\b')
		case '0':
			b.WriteByte(0)
		case 'x':
			if i+2 >= len(content) {
				return "", false, errors.New("invalid hex escape")
			}
			val, err := strconv.ParseUint(content[i+1:i+3], 16, 8)
			if err != nil {
				return "", false, err
			}
			b.WriteByte(byte(val))
			i += 2
		case 'u':
			if i+1 < len(content) && content[i+1] == '{' {
				end := strings.IndexByte(content[i+2:], '}')
				if end == -1 {
					return "", false, errors.New("unterminated unicode escape")
				}
				hex := content[i+2 : i+2+end]
				val, err := strconv.ParseUint(hex, 16, 32)
				if err != nil {
					return "", false, err
				}
				b.WriteRune(rune(val))
				i += 2 + end
				continue
			}
			if i+4 >= len(content) {
				return "", false, errors.New("invalid unicode escape")
			}
			val, err := strconv.ParseUint(content[i+1:i+5], 16, 16)
			if err != nil {
				return "", false, err
			}
			b.WriteRune(rune(val))
			i += 4
		case '\n':
			// line continuation
		case '\r':
			// line continuation
			if i+1 < len(content) && content[i+1] == '\n' {
				i++
			}
		default:
			// Unknown escape: keep literal char
			b.WriteByte(content[i])
		}
	}
	return b.String(), true, nil
}

func parseInputExpr(expr string, stringVars map[string]string) (string, bool, error) {
	if expr == "" {
		return "", false, nil
	}
	if expr[0] == '\'' || expr[0] == '"' {
		return parseJSStringLiteral(expr)
	}
	if val, ok := stringVars[expr]; ok {
		return val, true, nil
	}
	return "", false, nil
}

func isASCIIAlpha(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func writeTests(outPath string, cases []testCase, stats collectStats) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	if err := writeHeader(w, stats); err != nil {
		return err
	}

	if _, err := io.WriteString(w, "var test262GeneratedCases = []struct {\n\tname string\n\tfile string\n\tpattern string\n\tflags string\n\tinput string\n\twant bool\n\texpect string\n\thasExpect bool\n}{\n"); err != nil {
		return err
	}
	for _, c := range cases {
		if _, err := io.WriteString(w, fmt.Sprintf("\t{%s, %s, %s, %s, %s, %t, %s, %t},\n",
			strconv.Quote(c.name),
			strconv.Quote(c.file),
			strconv.Quote(c.pattern),
			strconv.Quote(c.flags),
			strconv.Quote(c.input),
			c.want,
			strconv.Quote(c.expect),
			c.hasExpect)); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "}\n\n"); err != nil {
		return err
	}

	if _, err := io.WriteString(w, testRunner); err != nil {
		return err
	}

	return w.Flush()
}

func writeHeader(w io.Writer, stats collectStats) error {
	_, err := io.WriteString(w, "// Code generated by tools/test262_convert; DO NOT EDIT.\n")
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, fmt.Sprintf("// Source: tc39/test262/test/built-ins/RegExp (files scanned: %d, skipped cases: %d)\n\n", stats.filesScanned, stats.casesSkipped))
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "package ecma262_test\n\n")
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "import (\n\t\"os\"\n\t\"testing\"\n\n\t\"github.com/mgilbir/goecma262\"\n\t\"github.com/mgilbir/goecma262/flags\"\n)\n\n")
	return err
}

var testRunner = `func TestTest262Generated(t *testing.T) {
	strict := os.Getenv("TEST262_STRICT") == "1"
	for _, tc := range test262GeneratedCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f, err := flags.Parse(tc.flags)
			if err != nil {
				if strict {
					t.Fatalf("%s: invalid flags %q: %v", tc.file, tc.flags, err)
				}
				t.Skipf("%s: invalid flags %q: %v", tc.file, tc.flags, err)
				return
			}
			re, err := ecma262.Compile(tc.pattern, f)
			if err != nil {
				if strict {
					t.Fatalf("%s: compile error for %q: %v", tc.file, tc.pattern, err)
				}
				t.Skipf("%s: compile error for %q: %v", tc.file, tc.pattern, err)
				return
			}
			if tc.hasExpect {
				got := re.FindString(tc.input)
				if got != tc.expect {
					t.Fatalf("%s: /%s/%s.FindString(%q) = %q, want %q", tc.file, tc.pattern, tc.flags, tc.input, got, tc.expect)
				}
				return
			}
			got := re.MatchString(tc.input)
			if got != tc.want {
				t.Fatalf("%s: /%s/%s.MatchString(%q) = %v, want %v", tc.file, tc.pattern, tc.flags, tc.input, got, tc.want)
			}
		})
	}
}
`

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// Ensure this file compiles when using rune-literal helpers.
var _ = utf8.RuneSelf
