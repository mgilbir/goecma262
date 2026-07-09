# goecma262

A Go implementation of ECMA-262 (JavaScript) regular expressions with an API compatible with Go's standard `regexp` package.

## Features

This library implements ECMA-262 regular expressions with support for:

### Core Features
- ✅ Literal character matching
- ✅ `.` (dot) - any character (with `s` flag support for matching newlines)
- ✅ Character classes `[abc]`, `[^abc]`, `[a-z]`, `[0-9]`
- ✅ Shorthand character classes: `\d`, `\D`, `\w`, `\W`, `\s`, `\S`
- ✅ Anchors: `^`, `$`, `\b`, `\B`
- ✅ Quantifiers: `*`, `+`, `?`, `{n}`, `{n,}`, `{n,m}` (greedy and non-greedy)
- ✅ Alternation: `a|b|c`
- ✅ Capturing groups: `(abc)`
- ✅ Non-capturing groups: `(?:abc)`
- ✅ Backreferences: `\1`, `\2`, etc.

### ECMA-262 Specific Features
- ✅ Flags: `i` (ignore case), `g` (global), `m` (multiline), `s` (dotAll), `u` (unicode), `v` (unicodeSets), `y` (sticky), `d` (hasIndices)
- ✅ Named capture groups: `(?<name>abc)` and backreferences `\k<name>`
- ✅ Lookahead: `(?=...)` and `(?!...)`
- ✅ Lookbehind: `(?<=...)` and `(?<!...)` (including variable-length; see limitation on right-to-left capture semantics below)
- ✅ Unicode property escapes: `\p{...}` and `\P{...}` (all general categories, scripts via `Script=`, and common binary properties; requires `u`/`v`; unknown names are rejected)
- ✅ Hex escapes: `\xFF`
- ✅ Unicode escapes: `\uFFFF` and `\u{...}` (requires `u`/`v` for code point escapes)
- ✅ Control characters: `\cA`
- ✅ Special escapes: `\n`, `\r`, `\t`, `\f`, `\v`

### API Compatibility

The API is designed to match Go's `regexp` package:

```go
// Compile a regex
re, err := ecma262.Compile(`\d+`, flags.Flags(0))
if err != nil {
    log.Fatal(err)
}

// Or use MustCompile for compile-time constants
re := ecma262.MustCompile(`\w+`, flags.Flags(0))

// Matching
matched := re.MatchString("hello123")  // true
matched = re.Match([]byte("hello123")) // true

// Finding
match := re.FindString("hello123 world")     // "hello123"
matches := re.FindAllString("a1 b2 c3", -1)  // ["1", "2", "3"]

// Finding with submatches
submatches := re.FindStringSubmatch("hello123") // ["hello123", ...]

// Replacement (use g flag for all matches)
re, _ = ecma262.Compile(`\d+`, flags.Global)
result := re.ReplaceAllString("a1b2c3", "X")  // "aXbXcX"

// Splitting
parts := re.Split("a1b2c3", -1)  // ["a", "b", "c", ""]
```

## Installation

```bash
go get github.com/mgilbir/goecma262
```

## Usage Examples

### Basic Matching

```go
package main

import (
    "fmt"
    "github.com/mgilbir/goecma262"
    "github.com/mgilbir/goecma262/flags"
)

func main() {
    // Simple pattern
    re := ecma262.MustCompile(`hello`, flags.Flags(0))
    fmt.Println(re.MatchString("hello world")) // true
    
    // With case-insensitive flag
    re = ecma262.MustCompile(`hello`, flags.IgnoreCase)
    fmt.Println(re.MatchString("HELLO")) // true
    
    // Using multiple flags
    re = ecma262.MustCompile(`^line`, flags.Multiline|flags.IgnoreCase)
    fmt.Println(re.MatchString("First line\nSecond Line")) // true
}
```

### Capturing Groups

```go
re := ecma262.MustCompile(`(\d{4})-(\d{2})-(\d{2})`, flags.Flags(0))
matches := re.FindStringSubmatch("Date: 2024-03-15")
// matches = ["2024-03-15", "2024", "03", "15"]
```

### Named Capture Groups (ECMA-262 Feature)

```go
re := ecma262.MustCompile(`(?<year>\d{4})-(?<month>\d{2})-(?<day>\d{2})`, flags.Flags(0))
matches := re.FindStringSubmatch("Date: 2024-03-15")
// Group names: re.SubexpNames() = ["", "year", "month", "day"]
```

### Lookahead

```go
// Positive lookahead - match digits followed by dollars
re := ecma262.MustCompile(`\d+(?= dollars)`, flags.Flags(0))
match := re.FindString("Price: 42 dollars") // "42"

// Negative lookahead - match digits NOT followed by dollars
re = ecma262.MustCompile(`\d+(?! dollars)`, flags.Flags(0))
match = re.FindString("Price: 42 euros") // "42"
```

### Unicode Property Escapes (ECMA-262 Feature)

Unicode property escapes require the `u` (Unicode) or `v` (UnicodeSets) flag.

```go
re := ecma262.MustCompile(`\p{digit}+`, flags.Unicode)
fmt.Println(re.MatchString("42"))           // true
fmt.Println(re.MatchString("\u09EA"))       // true (Bengali digit ৪)

re = ecma262.MustCompile(`\p{Nd}+`, flags.Unicode)
fmt.Println(re.MatchString("\u09EA"))       // true

re = ecma262.MustCompile(`\p{General_Category=Decimal_Number}+`, flags.Unicode)
fmt.Println(re.MatchString("\u09EA"))       // true
```

Without `u`/`v`, `\p`/`\P` are treated as identity escapes (literal `p`/`P`).

### Using All ECMA-262 Flags

```go
import "github.com/mgilbir/goecma262/flags"

// Parse flags from string
f, _ := flags.Parse("gimsuy")
re, _ := ecma262.Compile(`pattern`, f)

// Or use individual flags
re, _ := ecma262.Compile(`pattern`, flags.IgnoreCase|flags.Multiline|flags.DotAll)
```

## Flags

| Flag | Description |
|------|-------------|
| `i` | Ignore case - case-insensitive matching |
| `g` | Global - find all matches (used by FindAll functions) |
| `m` | Multiline - `^` and `$` match start/end of lines |
| `s` | DotAll - `.` matches newline characters |
| `u` | Unicode - enable Unicode features (required for `\p{...}` and `\u{...}`) |
| `v` | UnicodeSets - extended Unicode features (cannot use with `u`) |
| `y` | Sticky - match only from lastIndex position |
| `d` | HasIndices - parsed and accepted, but match indices are **not** exposed in the API (see Known Limitations) |

## Architecture

The implementation consists of several components:

1. **Parser** (`parser/`): Recursive descent parser that converts regex patterns into an AST
2. **Compiler** (`compiler/`): Transforms AST into VM bytecode instructions
3. **VM** (`vm/`): Virtual machine that executes bytecode using a backtracking algorithm
4. **Flags** (`flags/`): ECMA-262 flag handling
5. **Main API** (`ecma262.go`): User-facing API compatible with Go's regexp package

The VM uses a thread-based backtracking approach similar to the one described in Russ Cox's articles on regular expressions.

## Performance

Basic performance on an AMD Ryzen 9 6900HX (`go test ./tests/ -bench . -benchmem`):

```
BenchmarkMatch-16            782073    1536 ns/op    1000 B/op    28 allocs/op
BenchmarkCompileAndMatch-16  399681    2691 ns/op    3256 B/op    50 allocs/op
```

## Testing

Run the test suite:

```bash
go test ./tests/...
```

Run with benchmarks:

```bash
go test ./tests/... -bench=. -benchmem
```

## Test262 Compliance

The implementation is tested against the official ECMAScript [Test262](https://github.com/tc39/test262) suite.
The test cases are extracted from the `test/built-ins/RegExp` subtree and compiled into
`tests/test262_generated_test.go` (66 136 cases as of the last regeneration).

**Current result: all 66 136 cases pass or are explicitly skipped.**

21 cases are permanently skipped because they require JavaScript runtime semantics or
hit implementation limits that cannot be resolved in a static Go API:

| Category | Count | Reason |
|---|---|---|
| Functional replace (`functional-replace-*.js`) | 8 | Replacement argument is a JS arrow function; Go has no JS runtime to execute it |
| RegExp subclass (`groups-object-subclass*.js`) | 4 | Tests override `Symbol.replace` and inject a custom JS groups object; not representable in Go |
| Lookbehind right-to-left captures (`lookbehind.js`, `back-references-to-captures.js`) | 7 | ECMA-262 evaluates lookbehind bodies right-to-left; our left-to-right implementation produces incorrect capture groups when a quantified capture sits inside the lookbehind |
| Deeply nested patterns (`S15.10.2.8_A3_T15.js`, `S15.10.2.8_A3_T16.js`) | 2 | Patterns with 200+ nesting levels hit the compile-time `MaxNestingDepth` limit |

These skips are recorded in `tests/test262_skip_test.go`.  That file is **not** overwritten
by the test generator, so the skip list survives regeneration.

### Regenerating the test suite

If you update the Test262 repository or extend the extractor, regenerate with:

```bash
# 1. Extract cases from the Test262 source tree
node tools/test262_convert/extract.js \
    --test262 /path/to/test262 \
    --out tests/test262_cases.json

# 2. Generate the Go test file
go run ./tools/test262_from_json/ \
    -in  tests/test262_cases.json \
    -out tests/test262_generated_test.go
```

`tests/test262_cases.json` is listed in `.gitignore` (it is large and reproducible).
`tests/test262_generated_test.go` **is** committed so that `go test` works without
the Node.js extraction step.

### Adding known failures

If a regeneration surfaces a new test that cannot pass in Go, add it to
`tests/test262_skip_test.go`:

```go
var test262KnownFailures = map[string]string{
    // existing entries ...
    "new-test-name.js#42": "reason this cannot be implemented in Go",
}
```

The map key is the `tc.name` value printed by `go test -v`.  The value is a human-readable
explanation shown in the skip message.

Set `TEST262_STRICT=1` to promote all compile/flag errors from `t.Skip` to `t.Fatal`,
which is useful for catching regressions during development:

```bash
TEST262_STRICT=1 go test ./tests/ -run TestTest262Generated
```

## Known Limitations

1. **Lookbehind capture semantics** - Both fixed- and variable-length lookbehinds match, but the body is evaluated left-to-right while ECMA-262 specifies right-to-left. Capture groups inside a quantified lookbehind therefore return the last (rightmost) iteration's value instead of the first (leftmost); e.g. `(?<=(?<a>\w){3})f` on `"abcdef"` captures `"e"` where ECMA-262 requires `"c"`.
2. **Unicode property escapes** - All general categories, scripts (via `Script=`/`Script_Extensions=`), and the common lone binary properties are supported; some rarer binary properties may be missing (and are reported as errors rather than silently matching nothing).
3. **HasIndices flag** (`d`) - Parsed and accepted, but match indices are not exposed in the API.
4. **Nesting depth / program size** - Patterns nested more than 200 levels deep, or that compile to more than 200,000 instructions, are rejected at compile time.
5. **Case folding** - Case-insensitive matching uses Unicode simple case folding under the `u` flag and ASCII-only folding otherwise; non-ASCII case folding is not applied in non-`u` mode.

## Contributing

Contributions are welcome! Areas that need work:

- Right-to-left lookbehind evaluation (ECMA-262 compliant capture semantics)
- Broader Unicode binary-property coverage
- HasIndices (`d` flag) match index exposure in the API
- Non-`u`-mode non-ASCII case folding
- Performance optimizations

## License

MIT License - see LICENSE file for details

## References

- [ECMA-262 Specification](https://tc39.es/ecma262/)
- [Test262 Test Suite](https://github.com/tc39/test262)
- [Go regexp package](https://pkg.go.dev/regexp)
- [Russ Cox's Regular Expression Articles](https://swtch.com/~rsc/regexp/)
