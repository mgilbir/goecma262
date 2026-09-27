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
bounded by an execution budget that grows linearly with the input
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
- ✅ Modifier groups `(?i:...)`, `(?-i:...)`, `(?ms-i:...)`: turn `i`, `m` and `s` on or off for part of a pattern
- ✅ Lookahead `(?=...)`, `(?!...)`
- ✅ Lookbehind `(?<=...)`, `(?<!...)` — including variable-length, with ECMA-262 right-to-left capture semantics
- ✅ Unicode property escapes `\p{...}`, `\P{...}` (requires `u`/`v`; all general categories, scripts via `Script=`, common binary properties; unknown names are rejected)
- ✅ `v`-flag class sets: intersection `[\p{L}&&\p{Script=Greek}]`, subtraction `[\w--\d]`, nested classes `[[a-z][0-9]]`, strings `[\q{abc|xy}]`, and properties of strings such as `\p{RGI_Emoji}` (Unicode 17.0)
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

With `v`, classes are sets that can be intersected, subtracted and nested,
and can hold strings as well as characters:

```go
re := ecma262.MustCompile(`[\p{L}--[a-z]]+`, flags.UnicodeSets)
re.FindString("abcÄÖüdef") // "ÄÖü"

re = ecma262.MustCompile(`\p{RGI_Emoji}`, flags.UnicodeSets)
re.FindString("hi 👨‍👩‍👧!") // "👨‍👩‍👧" (one match for the whole family sequence)
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
| `v` | UnicodeSets - Unicode mode plus class set operations, `\q{...}` strings, and properties of strings (cannot use with `u`) |
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

**Bounded matching (ReDoS protection).** Matching backtracks on an explicit,
heap-allocated stack, so no input can overflow the goroutine stack. Every
match operation runs under an execution budget of steps and backtracking
memory that grows linearly with the input: by default 1,000,000 steps plus
100 per input byte, and 256 MiB plus 32 bytes per input byte
(`SetMaxSteps` sets a fixed step limit instead). Common patterns such as
`^[a-z]+$`, `^(a)+$` or `^(?:a|b)+$` run in linear time and use a handful of
steps per byte, so they complete at any input length; only super-linear
(catastrophic) searches exhaust the budget.

An operation that exceeds its budget has **no answer** — which is not the same
as no match. The error-returning forms report it as `ErrStepLimit`:
the package-level `MatchString` and `Match`, and the methods ending in `Err`
(`MatchStringErr`, `MatchErr`, `FindStringIndexErr`,
`FindStringSubmatchIndexErr`, `FindAllStringSubmatchIndexErr`,
`ReplaceAllStringErr`, `SplitErr`). The other methods have no error result:
they report an exceeded budget as **no match**, and the iterating ones
(`FindAll*`, `ReplaceAll*`, `Split`) stop at that point. If you match untrusted
patterns or inputs, use the error-returning forms:

```go
ok, err := re.MatchStringErr(input)
if errors.Is(err, ecma262.ErrStepLimit) {
    // pattern/input too expensive: no answer, not a non-match
}
```

**Errors.** `Compile` wraps failures as `parse error: …` or
`compile error: …` and rejects `u`+`v` as `incompatible flags`. `flags.Parse`
returns typed errors (`InvalidFlagError`, `DuplicateFlagError`,
`IncompatibleFlagsError`). An exceeded execution budget is `ErrStepLimit`
(the same value as `vm.ErrStepLimit`; the memory form wraps it), comparable
with `errors.Is`.

## Architecture

Pattern strings are parsed to an AST (`parser/`), compiled to bytecode
(`compiler/`), and executed by a backtracking VM (`vm/`) — the
backtracking design from Russ Cox's regular-expression articles, run on an
explicit stack, with memoization of failed states, a static analysis that
keeps common patterns linear-time, and an execution budget bounding
worst-case cost.
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
[Test262](https://github.com/tc39/test262) suite: **all 66,346 extracted
cases pass or are explicitly skipped**. The 14 permanent skips need a real
JavaScript runtime (e.g. a JS function as replacement argument) or exceed
compile-time limits; [`tests/test262_skip_test.go`](tests/test262_skip_test.go)
is the canonical list, with the reason for every entry. How to regenerate the
suite and maintain the skip list is covered in
[CONTRIBUTING.md](CONTRIBUTING.md).

## Known limitations

1. **Unicode property escapes** - Names and values are matched exactly, as in JavaScript: `\p{Lu}`, `\p{Uppercase_Letter}` and `\p{sc=Latn}` are valid, `\p{uppercase_letter}` is not. All general categories and scripts (with every alias in `PropertyValueAliases.txt`) and most of ECMA-262's binary properties are supported, including computed ones (`\p{Cased}`, `\p{Math}`, `\p{ID_Start}`, …) and the emoji properties (`\p{Emoji}`, `\p{Extended_Pictographic}`, …, from generated Unicode 17.0 tables). Not supported, because Go's `unicode` package has no data for them: `Bidi_Mirrored`, `Changes_When_Casefolded`, `Changes_When_Casemapped`, `Changes_When_NFKC_Casefolded` and `Grapheme_Base`. Approximate: `Script_Extensions=` uses `Script` data; `Case_Ignorable`, `Default_Ignorable_Code_Point` and `XID_*` are derived approximately; `Changes_When_Uppercased`/`Titlecased` miss characters whose mapping is a string (`ß`); `ID_Start`/`ID_Continue` include U+2E2F.
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
