package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type jsonCase struct {
	File        string  `json:"file"`
	Name        string  `json:"name"`
	Method      string  `json:"method"`
	Pattern     string  `json:"pattern"`
	Flags       string  `json:"flags"`
	Input       string  `json:"input"`
	Expected    any     `json:"expected"`
	MatchIndex  *int    `json:"matchIndex"`
	LastIndex   any     `json:"lastIndex"` // may be number, string, or object (coerced to int)
	ReplaceWith *string `json:"replaceWith"`
	SplitLimit  *int    `json:"splitLimit"`
}

type jsonRoot struct {
	Cases []jsonCase `json:"cases"`
}

type goCase struct {
	Name        string
	File        string
	Pattern     string
	Flags       string
	Input       string
	Kind        string
	Expect      string
	Index       int
	Limit       int
	LastIndex   int
	ReplaceWith string
}

func main() {
	var inPath string
	var outPath string
	flag.StringVar(&inPath, "in", "tests/test262_cases.json", "input JSON file")
	flag.StringVar(&outPath, "out", "tests/test262_generated_test.go", "output Go test file")
	flag.Parse()

	data, err := os.ReadFile(inPath)
	if err != nil {
		fatalf("read %s: %v", inPath, err)
	}

	var root jsonRoot
	if err := json.Unmarshal(data, &root); err != nil {
		fatalf("parse json: %v", err)
	}

	cases := make([]goCase, 0, len(root.Cases))
	for _, c := range root.Cases {
		gc, ok := convertCase(c)
		if !ok {
			continue
		}
		cases = append(cases, gc)
	}

	sort.Slice(cases, func(i, j int) bool {
		if cases[i].File == cases[j].File {
			return cases[i].Name < cases[j].Name
		}
		return cases[i].File < cases[j].File
	})

	if err := writeGo(outPath, cases); err != nil {
		fatalf("write: %v", err)
	}

	fmt.Printf("generated %d go cases -> %s\n", len(cases), outPath)
}

// coerceLastIndex converts a JSON lastIndex value (already pre-processed by
// coerceLastIndexForJSON in extract.js) to an int. The JS side already applies
// ECMA-262 ToIntegerOrInfinity semantics, so we just need to handle the
// JSON number type.
func coerceLastIndex(v any) int {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		if x != x { // NaN (shouldn't happen after JS processing)
			return 0
		}
		if x < 0 {
			return 0
		}
		if x > 2147483647 {
			return 2147483647
		}
		return int(x)
	}
	return 0
}

func convertCase(c jsonCase) (goCase, bool) {
	li := coerceLastIndex(c.LastIndex)
	switch c.Method {
	case "test":
		b, ok := c.Expected.(bool)
		if !ok {
			return goCase{}, false
		}
		return goCase{
			Name:      c.Name,
			File:      c.File,
			Pattern:   c.Pattern,
			Flags:     c.Flags,
			Input:     c.Input,
			Kind:      "match",
			Expect:    strconv.FormatBool(b),
			LastIndex: li,
		}, true
	case "exec":
		if c.Expected == nil {
			// If matchIndex is set, this is an undefined group element from a successful exec
			// (extraction artifact from d-flag or unmatched-group tests) — skip it.
			if c.MatchIndex != nil {
				return goCase{}, false
			}
			return goCase{
				Name:      c.Name,
				File:      c.File,
				Pattern:   c.Pattern,
				Flags:     c.Flags,
				Input:     c.Input,
				Kind:      "match",
				Expect:    "false",
				LastIndex: li,
			}, true
		}
		s, ok := c.Expected.(string)
		if !ok {
			return goCase{}, false
		}
		index := 0
		if c.MatchIndex != nil {
			index = *c.MatchIndex
		}
		return goCase{
			Name:      c.Name,
			File:      c.File,
			Pattern:   c.Pattern,
			Flags:     c.Flags,
			Input:     c.Input,
			Kind:      "submatch",
			Expect:    s,
			Index:     index,
			LastIndex: li,
		}, true
	case "string_match":
		if c.Expected == nil {
			// If matchIndex is set, this is an undefined group element — skip it.
			if c.MatchIndex != nil {
				return goCase{}, false
			}
			return goCase{
				Name:      c.Name,
				File:      c.File,
				Pattern:   c.Pattern,
				Flags:     c.Flags,
				Input:     c.Input,
				Kind:      "match",
				Expect:    "false",
				LastIndex: li,
			}, true
		}
		s, ok := c.Expected.(string)
		if !ok {
			return goCase{}, false
		}
		if c.MatchIndex != nil {
			if strings.Contains(c.Flags, "g") {
				return goCase{
					Name:      c.Name,
					File:      c.File,
					Pattern:   c.Pattern,
					Flags:     c.Flags,
					Input:     c.Input,
					Kind:      "findall",
					Expect:    s,
					Index:     *c.MatchIndex,
					LastIndex: li,
				}, true
			}
			return goCase{
				Name:      c.Name,
				File:      c.File,
				Pattern:   c.Pattern,
				Flags:     c.Flags,
				Input:     c.Input,
				Kind:      "submatch",
				Expect:    s,
				Index:     *c.MatchIndex,
				LastIndex: li,
			}, true
		}
		if strings.Contains(c.Flags, "g") {
			return goCase{}, false
		}
		return goCase{
			Name:      c.Name,
			File:      c.File,
			Pattern:   c.Pattern,
			Flags:     c.Flags,
			Input:     c.Input,
			Kind:      "find",
			Expect:    s,
			LastIndex: li,
		}, true
	case "string_replace":
		s, ok := c.Expected.(string)
		if !ok || c.ReplaceWith == nil {
			return goCase{}, false
		}
		return goCase{
			Name:        c.Name,
			File:        c.File,
			Pattern:     c.Pattern,
			Flags:       c.Flags,
			Input:       c.Input,
			Kind:        "replace",
			Expect:      s,
			ReplaceWith: *c.ReplaceWith,
			LastIndex:   li,
		}, true
	case "string_split":
		s, ok := c.Expected.(string)
		if !ok || c.MatchIndex == nil {
			return goCase{}, false
		}
		limit := -1
		if c.SplitLimit != nil {
			limit = *c.SplitLimit
		}
		return goCase{
			Name:      c.Name,
			File:      c.File,
			Pattern:   c.Pattern,
			Flags:     c.Flags,
			Input:     c.Input,
			Kind:      "split",
			Expect:    s,
			Index:     *c.MatchIndex,
			Limit:     limit,
			LastIndex: li,
		}, true
	default:
		return goCase{}, false
	}
}

func writeGo(outPath string, cases []goCase) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := fmt.Fprintln(f, "// Code generated by tools/test262_from_json; DO NOT EDIT."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, "package ecma262_test\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, "import (\n\t\"os\"\n\t\"testing\"\n\n\t\"github.com/mgilbir/goecma262\"\n\t\"github.com/mgilbir/goecma262/flags\"\n)\n"); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(f, "var test262GeneratedCases = []struct {"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, "\tname string\n\tfile string\n\tpattern string\n\tflags string\n\tinput string\n\tkind string\n\texpect string\n\tindex int\n\tlimit int\n\tlastIndex int\n\treplaceWith string\n} {"); err != nil {
		return err
	}

	for _, c := range cases {
		line := fmt.Sprintf("\t{%s, %s, %s, %s, %s, %s, %s, %d, %d, %d, %s},",
			strconv.Quote(c.Name),
			strconv.Quote(c.File),
			strconv.Quote(c.Pattern),
			strconv.Quote(c.Flags),
			strconv.Quote(c.Input),
			strconv.Quote(c.Kind),
			strconv.Quote(c.Expect),
			c.Index,
			c.Limit,
			c.LastIndex,
			strconv.Quote(c.ReplaceWith),
		)
		if _, err := fmt.Fprintln(f, line); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(f, "}\n"); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(f, "func TestTest262Generated(t *testing.T) {"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, "\tstrict := os.Getenv(\"TEST262_STRICT\") == \"1\""); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, "\tfor _, tc := range test262GeneratedCases {"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, "\t\ttc := tc\n\t\tt.Run(tc.name, func(t *testing.T) {"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, "\t\t\tf, err := flags.Parse(tc.flags)"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, "\t\t\tif err != nil {"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, "\t\t\t\tif strict {\n\t\t\t\t\tt.Fatalf(\"%s: invalid flags %q: %v\", tc.file, tc.flags, err)\n\t\t\t\t}\n\t\t\t\tt.Skipf(\"%s: invalid flags %q: %v\", tc.file, tc.flags, err)\n\t\t\t\treturn\n\t\t\t}"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, "\t\t\tre, err := ecma262.Compile(tc.pattern, f)"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, "\t\t\tif err != nil {\n\t\t\t\tif strict {\n\t\t\t\t\tt.Fatalf(\"%s: compile error for %q: %v\", tc.file, tc.pattern, err)\n\t\t\t\t}\n\t\t\t\tt.Skipf(\"%s: compile error for %q: %v\", tc.file, tc.pattern, err)\n\t\t\t\treturn\n\t\t\t}"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, "\t\t\tre.SetLastIndex(tc.lastIndex)"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, "\t\t\tswitch tc.kind {\n\t\t\tcase \"match\":\n\t\t\t\tgot := re.MatchString(tc.input)\n\t\t\t\twant := tc.expect == \"true\"\n\t\t\t\tif got != want {\n\t\t\t\t\tt.Fatalf(\"%s: /%s/%s.MatchString(%q) = %v, want %v\", tc.file, tc.pattern, tc.flags, tc.input, got, want)\n\t\t\t\t}\n\t\t\tcase \"find\":\n\t\t\t\tgot := re.FindString(tc.input)\n\t\t\t\tif got != tc.expect {\n\t\t\t\t\tt.Fatalf(\"%s: /%s/%s.FindString(%q) = %q, want %q\", tc.file, tc.pattern, tc.flags, tc.input, got, tc.expect)\n\t\t\t\t}\n\t\t\tcase \"submatch\":\n\t\t\t\tgot := re.FindStringSubmatch(tc.input)\n\t\t\t\tif got == nil || tc.index >= len(got) {\n\t\t\t\t\tt.Fatalf(\"%s: /%s/%s.FindStringSubmatch(%q) missing index %d\", tc.file, tc.pattern, tc.flags, tc.input, tc.index)\n\t\t\t\t}\n\t\t\t\tif got[tc.index] != tc.expect {\n\t\t\t\t\tt.Fatalf(\"%s: /%s/%s.FindStringSubmatch(%q)[%d] = %q, want %q\", tc.file, tc.pattern, tc.flags, tc.input, tc.index, got[tc.index], tc.expect)\n\t\t\t\t}\n\t\t\tcase \"findall\":\n\t\t\t\tgot := re.FindAllString(tc.input, -1)\n\t\t\t\tif tc.index >= len(got) {\n\t\t\t\t\tt.Fatalf(\"%s: /%s/%s.FindAllString(%q) missing index %d\", tc.file, tc.pattern, tc.flags, tc.input, tc.index)\n\t\t\t\t}\n\t\t\t\tif got[tc.index] != tc.expect {\n\t\t\t\t\tt.Fatalf(\"%s: /%s/%s.FindAllString(%q)[%d] = %q, want %q\", tc.file, tc.pattern, tc.flags, tc.input, tc.index, got[tc.index], tc.expect)\n\t\t\t\t}\n\t\t\tcase \"replace\":\n\t\t\t\tgot := re.ReplaceAllString(tc.input, tc.replaceWith)\n\t\t\t\tif got != tc.expect {\n\t\t\t\t\tt.Fatalf(\"%s: /%s/%s.ReplaceAllString(%q, %q) = %q, want %q\", tc.file, tc.pattern, tc.flags, tc.input, tc.replaceWith, got, tc.expect)\n\t\t\t\t}\n\t\t\tcase \"split\":\n\t\t\t\tgot := re.Split(tc.input, tc.limit)\n\t\t\t\tif tc.index >= len(got) {\n\t\t\t\t\tt.Fatalf(\"%s: /%s/%s.Split(%q, %d) missing index %d\", tc.file, tc.pattern, tc.flags, tc.input, tc.limit, tc.index)\n\t\t\t\t}\n\t\t\t\tif got[tc.index] != tc.expect {\n\t\t\t\t\tt.Fatalf(\"%s: /%s/%s.Split(%q, %d)[%d] = %q, want %q\", tc.file, tc.pattern, tc.flags, tc.input, tc.limit, tc.index, got[tc.index], tc.expect)\n\t\t\t\t}\n\t\t\tdefault:\n\t\t\t\tt.Skipf(\"%s: unsupported case kind %s\", tc.file, tc.kind)\n\t\t\t}\n\t\t})\n\t}\n}\n"); err != nil {
		return err
	}

	return nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
