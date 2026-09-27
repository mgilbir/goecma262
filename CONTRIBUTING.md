# Contributing to goecma262

Thanks for contributing! This guide covers the development workflow, the
Test262 test pipeline, and how to maintain the known-failure skip list.
For how the engine works internally, see [docs/architecture.md](docs/architecture.md).

## Development setup

Go 1.23+ is all you need to build and test. Node.js (a recent version, no
`npm install` required) is needed only if you regenerate the Test262 suite.

```bash
go build ./...
go test ./...                              # unit tests, examples, and 66k+ Test262 cases
go test ./tests/ -bench . -benchmem       # benchmarks
```

Documentation-bearing code snippets live in `example_test.go` as godoc
`Example` functions with `// Output:` assertions — if you change behavior a
snippet relies on, `go test` fails. Prefer adding an example there over
pasting unverified snippets into the README.

## Areas that need work

- Emoji-family Unicode properties (`\p{Emoji}`, `\p{Extended_Pictographic}`, …)
  — need embedded Unicode data; currently rejected at compile time
- Performance optimizations

## The Test262 pipeline

The implementation is tested against the official ECMAScript
[Test262](https://github.com/tc39/test262) suite. Test cases are extracted
from the RegExp tests — `test/built-ins/RegExp`, `test/language/literals/regexp`
and their Annex B counterparts under `test/annexB` — and compiled into
committed Go test files, so `go test` works without Node or a Test262
checkout:

```mermaid
flowchart LR
    T262["tc39/test262 checkout<br/>RegExp tests"]
    T262 -->|"node tools/test262_convert/extract.js<br/>--test262 … --out …"| JSON["tests/test262_cases.json<br/>(gitignored, reproducible)"]
    JSON -->|"go run ./tools/test262_from_json/<br/>-in … -out … -syntax-out …"| GEN["tests/test262_generated_test.go<br/>tests/test262_syntax_generated_test.go<br/>(committed)"]
    SKIP["tests/test262_skip_test.go<br/>hand-maintained skip list<br/>never regenerated"] --> RUN
    GEN --> RUN["go test ./tests/ -run TestTest262"]
```

The extractor runs each test file in Node.js with instrumented RegExp methods
and assertions, and records two kinds of case:

- **Match cases** (`tests/test262_generated_test.go`): a `test`, `exec`,
  `match`, `replace` or `split` call paired with the assertion on its result —
  `assert.sameValue`; `assert(...)` on a `test` call (the test's
  expectation is then the result Node produced, since Node passed the
  assertion); or `assert.compareArray` on a whole `exec` or non-global
  `match` result, which also checks which groups are undefined.
- **Syntax cases** (`tests/test262_syntax_generated_test.go`): patterns that
  must be SyntaxErrors — those the `RegExp` constructor rejects inside
  `assert.throws(SyntaxError, …)`, and the regular expression literal of each
  negative parse test (`negative: phase: parse`) — and patterns that must
  compile, written as a whole statement such as `/(?i:a)/;`. A negative
  literal is kept only if Node's `RegExp` constructor also rejects it: errors
  that belong to the literal rather than the pattern, such as a line
  terminator inside it, are reported as `literalOnlyErrors` in the JSON and
  dropped.

The generated files name the Test262 commit they were extracted from.
Because some expectations are Node's own results and Node's `RegExp` decides
which negative literals are pattern errors, extract with a Node.js that
implements the features under test (the committed files were extracted with
Node.js 26.9.0).

To regenerate after updating the Test262 checkout or extending the extractor:

```bash
# 1. Extract cases from the Test262 source tree (Node built-ins only)
node tools/test262_convert/extract.js \
    --test262 /path/to/test262 \
    --out tests/test262_cases.json

# 2. Generate the Go test files
go run ./tools/test262_from_json/ \
    -in  tests/test262_cases.json \
    -out tests/test262_generated_test.go \
    -syntax-out tests/test262_syntax_generated_test.go
```

This is the only supported pipeline; earlier generator tools that wrote a
different case-table schema have been removed.

## The known-failure skip list

`tests/test262_skip_test.go` is the **canonical record** of which Test262
cases cannot pass in a static Go API and why — each entry carries a comment
explaining the reason. The file is hand-maintained and never touched by the
generator, so it survives regeneration. When the README quotes skip counts,
those numbers derive from this file.

If a regeneration surfaces a new case that cannot pass in Go, add it there:

```go
var test262KnownFailures = map[string]string{
    // existing entries ...
    "new-test-name.js#42": "reason this cannot be implemented in Go",
}
```

The map key is the `tc.name` value printed by `go test -v`. The value is a
human-readable explanation shown in the skip message. Only add an entry when
the semantics genuinely cannot be expressed in Go (e.g. the test needs a JS
function as a replacement argument) — a failing case that *could* pass is a
bug to fix, not a skip to add.

### Strict mode

By default, compile and flag-parse errors in generated cases produce
`t.Skip`, so an experimental parser change doesn't drown you in failures;
the same holds for a syntax case that must compile. Set `TEST262_STRICT=1`
to promote those skips to `t.Fatal`, which catches regressions where a
previously compiling pattern stops compiling. A pattern that must be a
SyntaxError but compiles always fails.

```bash
TEST262_STRICT=1 go test ./tests/ -run TestTest262
```

## Refreshing the README benchmark numbers

The README quotes `go test ./tests/ -bench . -benchmem` output. If your
change affects performance, re-run that command and update the numbers (and
the CPU model if it differs) in the same PR, so the quoted figures stay
reproducible.
