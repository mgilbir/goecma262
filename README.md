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
- ⚠️ Lookbehind: `(?<=...)` and `(?<!...)` (basic support)
- ⚠️ Unicode property escapes: `\p{...}` and `\P{...}` (partial)
- ✅ Hex escapes: `\xFF`
- ✅ Unicode escapes: `\uFFFF` and `\u{...}`
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

// Replacement
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
| `u` | Unicode - enable Unicode features |
| `v` | UnicodeSets - extended Unicode features (cannot use with `u`) |
| `y` | Sticky - match only from lastIndex position |
| `d` | HasIndices - include match indices in results |

## Architecture

The implementation consists of several components:

1. **Parser** (`parser/`): Recursive descent parser that converts regex patterns into an AST
2. **Compiler** (`compiler/`): Transforms AST into VM bytecode instructions
3. **VM** (`vm/`): Virtual machine that executes bytecode using a backtracking algorithm
4. **Flags** (`flags/`): ECMA-262 flag handling
5. **Main API** (`ecma262.go`): User-facing API compatible with Go's regexp package

The VM uses a thread-based backtracking approach similar to the one described in Russ Cox's articles on regular expressions.

## Performance

Basic performance on an AMD Ryzen 9 6900HX:

```
BenchmarkMatch-16            583240    1973 ns/op    1848 B/op    30 allocs/op
BenchmarkCompileAndMatch-16  364164    3034 ns/op    3552 B/op    49 allocs/op
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

## Known Limitations

1. **Lookbehind assertions** - Basic support is implemented but edge cases may not be fully covered
2. **Unicode property escapes** - Limited to common properties (Letter, Number, Punctuation, etc.)
3. **Sticky flag** (`y`) - Not fully implemented in matching semantics
4. **HasIndices flag** (`d`) - Flag is parsed but indices are not exposed in API yet
5. **Atomic groups and possessive quantifiers** - Not implemented
6. **Subroutine calls** - Not implemented

## Test262 Integration

The implementation can be tested against the official ECMAScript Test262 suite. To do this:

1. Clone the Test262 repository
2. Write a test runner that converts Test262 format to Go tests
3. Run against the regex-related test cases

Future work includes creating a proper Test262 harness for comprehensive compliance testing.

## Contributing

Contributions are welcome! Areas that need work:

- Complete lookbehind implementation
- Full Unicode property support
- Performance optimizations
- Test262 compliance improvements
- Additional ECMA-262 features

## License

MIT License - see LICENSE file for details

## References

- [ECMA-262 Specification](https://tc39.es/ecma262/)
- [Test262 Test Suite](https://github.com/tc39/test262)
- [Go regexp package](https://pkg.go.dev/regexp)
- [Russ Cox's Regular Expression Articles](https://swtch.com/~rsc/regexp/)
