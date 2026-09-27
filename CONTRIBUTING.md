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

The `v`-flag suites check inside harness helpers, so the extractor replaces
`testPropertyOfStrings` and `testExtendedCharacterClass` with equivalents
that make the same checks through `assert.sameValue`. `testPropertyEscapes`
is deliberately not replaced: its inputs span whole Unicode ranges, too
large to embed. A case records the pattern and flags the regex matches with
(its `[[OriginalSource]]` and `[[OriginalFlags]]`), read through
`RegExp.prototype`'s own accessors, since a test may shadow the flag
properties.

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

## Differential testing against node

Test262 checks the cases its authors thought of. `tools/difftest` finds the
rest by asking a real JavaScript engine: it generates patterns, flags and
inputs, runs each case through `node` and through this engine, and reports
every disagreement — a pattern one side rejects, a different match, a
different capture, replacement or split. Node is the only requirement.

```bash
go run ./tools/difftest -n 200000 -seed 1          # random cases
node tools/difftest/gen-corpus.mjs > /tmp/corpus.txt
go run ./tools/difftest -cases /tmp/corpus.txt     # the structured corpus
```

`-nov`, `-nomod` and `-only xars` narrow a run to one area while working on
it. The oracle (`fuzz-oracle.mjs`) and the corpus (`corpus.mjs`) are shared
with [ktecma262](https://github.com/mgilbir/ktecma262), including the
detectors for known V8 defects, which are skipped and counted rather than
reported. This copy of the oracle also answers `U` for a result with a
position between the two halves of a surrogate pair, which goecma262 reports
as `ErrSurrogateSplit`; the two must agree like any other answer. A failure prints the exact pattern, flags and input, so it can be
turned into a regression test directly.

## Regenerating the emoji data

Go's `unicode` package has no emoji tables, so they are generated: the
members of `\p{RGI_Emoji}` and the other properties of strings in
`vm/emoji_strings.go`, and the emoji binary properties (`\p{Emoji}`, …) in
`vm/emoji_props.go`. When Go's `unicode.Version` changes, regenerate them
from the matching Unicode version's files, fetched from the numbered directory
rather than `/Public/emoji/latest`, which runs ahead of Go and of the
JavaScript engines:

```bash
V=17.0.0   # go doc unicode.Version
curl -fsSO https://www.unicode.org/Public/$V/emoji/emoji-sequences.txt
curl -fsSO https://www.unicode.org/Public/$V/emoji/emoji-zwj-sequences.txt
curl -fsSO https://www.unicode.org/Public/$V/ucd/emoji/emoji-data.txt
go run ./tools/genemoji -seq emoji-sequences.txt -zwj emoji-zwj-sequences.txt -data emoji-data.txt
```

The generator refuses files whose declared version does not match, and
`TestEmojiDataVersion` fails once `unicode.Version` moves past the data.

The spellings ECMA-262 accepts for general categories and scripts in
`\p{...}` are generated the same way, into `vm/property_names.go`, together
with the tables for the properties Go lacks (`vm/ucd_props.go`);
`TestPropertyNamesVersion` checks their version:

```bash
for f in PropertyValueAliases.txt DerivedCoreProperties.txt DerivedNormalizationProps.txt \
         extracted/DerivedBinaryProperties.txt ScriptExtensions.txt; do
  curl -fsSO https://www.unicode.org/Public/$V/ucd/$f
done
go run ./tools/genpropnames -in PropertyValueAliases.txt -core DerivedCoreProperties.txt \
    -norm DerivedNormalizationProps.txt -binary DerivedBinaryProperties.txt -scx ScriptExtensions.txt
```

## Refreshing the README benchmark numbers

The README quotes `go test ./tests/ -bench . -benchmem` output. If your
change affects performance, re-run that command and update the numbers (and
the CPU model if it differs) in the same PR, so the quoted figures stay
reproducible.
