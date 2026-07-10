# goecma262

A Go implementation of ECMA-262 (JavaScript) regular expressions with an API
shaped like Go's standard `regexp` package.

Reach for this library when you need regex features Go's RE2-based `regexp`
cannot express — backreferences, lookahead/lookbehind — or when patterns and
match results must behave exactly as they do in JavaScript (same flags, same
Annex B web-compatibility syntax, same capture and replacement semantics,
validated against the official [Test262](https://github.com/tc39/test262)
suite). If you need neither, prefer the standard library: RE2 guarantees
linear-time matching, while this engine is a backtracker whose worst case is
bounded by a configurable step limit
(see [Match semantics and safety](#match-semantics-and-safety)).

## Installation

```bash
go get github.com/mgilbir/goecma262
```

## Quick start

```go
package main

import (
    "fmt"

    "github.com/mgilbir/goecma262"
    "github.com/mgilbir/goecma262/flags"
)

func main() {
    re := ecma262.MustCompile(`\d+`, flags.Flags(0))
    fmt.Println(re.MatchString("hello123"))           // true
    fmt.Println(re.FindString("hello123 world"))      // "123"
    fmt.Println(re.FindAllString("a1 b2 c3", -1))     // [1 2 3]

    // Without the g flag only the first match is replaced, as in JavaScript.
    re = ecma262.MustCompile(`\d+`, flags.Global)
    fmt.Println(re.ReplaceAllString("a1b2c3", "X"))   // "aXbXcX"
    fmt.Println(re.Split("a1b2c3", -1))               // [a b c ]
}
```

Runnable, test-asserted examples for every feature below live in
[`example_test.go`](example_test.go) and render on
[pkg.go.dev](https://pkg.go.dev/github.com/mgilbir/goecma262).

## Features

### Core
- ✅ Literals, `.` (with `s` flag for newlines), character classes `[abc]`, `[^abc]`, `[a-z]`
- ✅ Shorthand classes `\d`, `\D`, `\w`, `\W`, `\s`, `\S`; anchors `^`, `$`, `\b`, `\B`
- ✅ Quantifiers `*`, `+`, `?`, `{n}`, `{n,}`, `{n,m}` (greedy and non-greedy)
- ✅ Alternation, capturing and non-capturing groups, backreferences `\1`, `\2`, …

### ECMA-262 specific
- ✅ Flags: `i`, `g`, `m`, `s`, `u`, `v`, `y`, `d` (see [Flags](#flags))
- ✅ Named capture groups `(?<name>abc)` and backreferences `\k<name>`
- ✅ Lookahead `(?=...)`, `(?!...)`
- ✅ Lookbehind `(?<=...)`, `(?<!...)` — including variable-length, with ECMA-262 right-to-left capture semantics
- ✅ Unicode property escapes `\p{...}`, `\P{...}` (requires `u`/`v`; all general categories, scripts via `Script=`, common binary properties; unknown names are rejected)
- ✅ Escapes: `\xFF`, `\uFFFF`, `\u{...}` (code points require `u`/`v`), `\cA`, `\n`, `\r`, `\t`, `\f`, `\v`
- ✅ Annex B web-compatibility syntax by default, strict mode opt-in (see [Syntax mode](#syntax-mode-annex-b-vs-strict))

## Usage examples

### Capturing groups — plain and named

```go
re := ecma262.MustCompile(`(\d{4})-(\d{2})-(\d{2})`, flags.Flags(0))
re.FindStringSubmatch("Date: 2024-03-15")
// ["2024-03-15", "2024", "03", "15"]

re = ecma262.MustCompile(`(?<year>\d{4})-(?<month>\d{2})-(?<day>\d{2})`, flags.Flags(0))
m := re.FindStringSubmatch("Date: 2024-03-15")
m[re.SubexpIndex("month")] // "03"
re.SubexpNames()           // ["", "year", "month", "day"]
```

### Lookaround

```go
// Positive lookahead - digits only when followed by " dollars"
re := ecma262.MustCompile(`\d+(?= dollars)`, flags.Flags(0))
re.FindString("Price: 42 dollars") // "42"

// Variable-length lookbehind
re = ecma262.MustCompile(`(?<=\$\s*)\d+`, flags.Flags(0))
re.FindString("total: $ 99") // "99"
```

### Replacement syntax

`ReplaceAllString` interprets `$` in the replacement per ECMA-262:
`$&` (whole match), `$1`…`$99` (group), `$<name>` (named group),
`` $` `` and `$'` (text before/after the match), `$$` (literal `$`).
Invalid references such as `$0` are emitted literally.

```go
re := ecma262.MustCompile(`(?<first>\w+) (?<last>\w+)`, flags.Flags(0))
re.ReplaceAllString("Ada Lovelace", "$<last>, $<first>") // "Lovelace, Ada"
```

### Unicode property escapes

Require the `u` (Unicode) or `v` (UnicodeSets) flag; without them, `\p`/`\P`
are identity escapes (literal `p`/`P`).

```go
re := ecma262.MustCompile(`\p{Nd}+`, flags.Unicode)
re.MatchString("৪") // true (Bengali digit ৪)

re = ecma262.MustCompile(`^\p{Script=Greek}+$`, flags.Unicode)
re.MatchString("αβγ") // true
```

### Flags from a string

```go
re, err := ecma262.CompileFlags(`pattern`, "gims")
// equivalent to: f, _ := flags.Parse("gims"); ecma262.Compile(`pattern`, f)
// or combine constants: flags.IgnoreCase | flags.Multiline | flags.DotAll
```

### Syntax mode (Annex B vs strict)

By default the compiler accepts the web-compatibility extensions (ECMA-262
Annex B) that browsers apply to non-Unicode patterns, so a regex that works in
JavaScript works here:

```go
// Default: Annex B. Legacy octal escape \5 matches U+0005; \8 is a literal "8";
// a{2 x} matches the text "a{2 x}"; \c1 matches the characters "\c1".
re := ecma262.MustCompile(`\5`, flags.Flags(0))

// Opt into strict ECMA-262, which rejects those constructs as errors:
re, err := ecma262.Compile(`\5`, flags.Flags(0), ecma262.WithSyntax(ecma262.SyntaxStrict))
```

The `u` and `v` flags always force strict behavior regardless of this option
(Annex B does not apply in Unicode mode). Note that an out-of-order quantifier
such as `a{2,1}` is a syntax error in *every* mode.

## Flags

| Flag | Description |
|------|-------------|
| `i` | Ignore case - case-insensitive matching |
| `g` | Global - `ReplaceAllString` replaces every match instead of the first; match operations become stateful via `lastIndex` (see below). `FindAll*` methods always return all matches regardless of this flag |
| `m` | Multiline - `^` and `$` match start/end of lines |
| `s` | DotAll - `.` matches newline characters |
| `u` | Unicode - enable Unicode features (required for `\p{...}` and `\u{...}`) |
| `v` | UnicodeSets - extended Unicode features (cannot use with `u`) |
| `y` | Sticky - match only at exactly the `lastIndex` position |
| `d` | HasIndices - parsed and accepted, but a no-op: match indices are always available via the `*Index` methods (see Known Limitations) |

## Match semantics and safety

**Statefulness.** A `Regexp` compiled without `g` or `y` is immutable and safe
for concurrent use. With `g` or `y`, `MatchString`, `Match`, and
`FindStringSubmatch` mirror JavaScript's `test`/`exec`: they start at the
instance's `lastIndex`, advance it past each match, and reset it to 0 on no
match — so repeated calls iterate, and such instances must not be shared
between goroutines without synchronization. `SetLastIndex`/`LastIndex` expose
the cursor directly.

**Offsets are bytes.** All positions (`lastIndex`, `*Index` results) are byte
offsets into the Go string, not UTF-16 code-unit indices as in JavaScript.

**ReDoS protection.** The backtracking VM enforces a step limit (default
1,000,000; tune per instance with `SetMaxSteps`). When a match operation
exceeds it, the boolean/string methods report **no match** — they cannot
distinguish a limit hit from a genuine non-match. If you match untrusted
patterns or inputs, use the error-returning variants:

```go
ok, err := re.MatchStringErr(input)
if errors.Is(err, vm.ErrStepLimit) {
    // pattern/input too expensive, not a non-match
}
```

**Errors.** `Compile` wraps failures as `parse error: …` or
`compile error: …` and rejects `u`+`v` as `incompatible flags`. `flags.Parse`
returns typed errors (`InvalidFlagError`, `DuplicateFlagError`,
`IncompatibleFlagsError`). Match-time step-limit errors are
`vm.ErrStepLimit`, comparable with `errors.Is`.

## Architecture

Pattern strings are parsed to an AST (`parser/`), compiled to bytecode
(`compiler/`), and executed by a recursive backtracking VM (`vm/`) — the
backtracking design from Russ Cox's regular-expression articles, with
memoization of failed states and a step budget bounding worst-case cost.
Backtracking is what makes backreferences and lookarounds possible (RE2-based
engines structurally cannot support them). Diagrams and internals — including
how right-to-left lookbehind and the ReDoS bounds work — are in
[docs/architecture.md](docs/architecture.md).

## Performance

Basic performance on an AMD Ryzen 9 6900HX (`go test ./tests/ -bench . -benchmem`):

```
BenchmarkMatch-16            782073    1536 ns/op    1000 B/op    28 allocs/op
BenchmarkCompileAndMatch-16  399681    2691 ns/op    3256 B/op    50 allocs/op
```

## Testing and Test262 compliance

```bash
go test ./...
```

The implementation is tested against the official ECMAScript
[Test262](https://github.com/tc39/test262) suite: **all 66,136 extracted
cases pass or are explicitly skipped**. The 14 permanent skips need a real
JavaScript runtime (e.g. a JS function as replacement argument) or exceed
compile-time limits; [`tests/test262_skip_test.go`](tests/test262_skip_test.go)
is the canonical list, with the reason for every entry. How to regenerate the
suite and maintain the skip list is covered in
[CONTRIBUTING.md](CONTRIBUTING.md).

## Known limitations

1. **Unicode property escapes** - All general categories, scripts (via `Script=`/`Script_Extensions=`), and most binary properties are supported, including aliases (`\p{AHex}`, `\p{WSpace}`) and computed properties (`\p{Cased}`, `\p{Math}`, `\p{ID_Start}`, …). The Emoji-family properties (`\p{Emoji}`, `\p{Emoji_Presentation}`, `\p{Extended_Pictographic}`, …) have no Go table and are reported as errors rather than silently matching nothing; a few computed properties (`Case_Ignorable`, `Default_Ignorable_Code_Point`, `XID_*`) are close approximations.
2. **HasIndices flag** (`d`) - Parsed and accepted, but it has no effect: match indices are always available through the `*Index` methods (`FindStringSubmatchIndex`, `FindAllStringSubmatchIndex`, etc.), which return `[start, end)` byte-offset pairs per group (`-1` for a non-participating group). Named-group indices (JavaScript's `indices.groups`) are obtained by combining `SubexpIndex(name)` with those pairs.
3. **Compile-time limits** - Patterns nested more than 200 levels deep, with a single quantifier bound above 10,000 (`a{10001}`), or compiling to more than 200,000 instructions are rejected at compile time.
4. **Case folding** - Case-insensitive matching uses Unicode simple case folding under the `u` flag, and the legacy `Canonicalize` (uppercase-based, with the "don't map non-ASCII to ASCII" guard) otherwise — matching JavaScript in both modes. A handful of full-mapping edge cases (e.g. `ß`↔`SS`) are not folded, as in most engines.

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the
development workflow, the Test262 pipeline, and areas that need work.

## License

MIT License - see LICENSE file for details

## References

- [ECMA-262 Specification](https://tc39.es/ecma262/)
- [Test262 Test Suite](https://github.com/tc39/test262)
- [Go regexp package](https://pkg.go.dev/regexp)
- [Russ Cox's Regular Expression Articles](https://swtch.com/~rsc/regexp/)
