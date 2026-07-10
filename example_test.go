package ecma262_test

import (
	"errors"
	"fmt"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/vm"
)

func ExampleCompile() {
	re, err := ecma262.Compile(`\d+`, flags.Flags(0))
	if err != nil {
		panic(err)
	}
	fmt.Println(re.MatchString("hello123"))
	fmt.Println(re.FindString("order 42 shipped"))
	// Output:
	// true
	// 42
}

func ExampleCompileFlags() {
	// CompileFlags parses the flag string for you — the one-call form of
	// flags.Parse followed by Compile.
	re, err := ecma262.CompileFlags(`hello`, "i")
	if err != nil {
		panic(err)
	}
	fmt.Println(re.MatchString("HELLO world"))
	// Output:
	// true
}

func ExampleMustCompile() {
	re := ecma262.MustCompile(`^line`, flags.Multiline)
	fmt.Println(re.MatchString("first\nline two")) // ^ matches after \n with m
	fmt.Println(re.MatchString("first\nsecond"))
	// Output:
	// true
	// false
}

func ExampleRegexp_FindAllString() {
	re := ecma262.MustCompile(`\d+`, flags.Flags(0))
	fmt.Println(re.FindAllString("a1 b22 c333", -1))
	// Output:
	// [1 22 333]
}

func ExampleRegexp_FindStringSubmatch() {
	re := ecma262.MustCompile(`(\d{4})-(\d{2})-(\d{2})`, flags.Flags(0))
	fmt.Println(re.FindStringSubmatch("Date: 2024-03-15"))
	// Output:
	// [2024-03-15 2024 03 15]
}

func ExampleRegexp_SubexpNames() {
	re := ecma262.MustCompile(`(?<year>\d{4})-(?<month>\d{2})-(?<day>\d{2})`, flags.Flags(0))
	m := re.FindStringSubmatch("Date: 2024-03-15")
	fmt.Println(re.SubexpNames())
	fmt.Println(m[re.SubexpIndex("month")])
	// Output:
	// [ year month day]
	// 03
}

func ExampleRegexp_FindString_lookahead() {
	// Positive lookahead: digits only when followed by " dollars".
	re := ecma262.MustCompile(`\d+(?= dollars)`, flags.Flags(0))
	fmt.Println(re.FindString("Price: 42 dollars"))

	// Negative lookahead: digits not followed by " dollars".
	re = ecma262.MustCompile(`\d+(?! dollars)`, flags.Flags(0))
	fmt.Println(re.FindString("Price: 42 euros"))
	// Output:
	// 42
	// 42
}

func ExampleRegexp_FindString_lookbehind() {
	// Variable-length lookbehind is supported, with ECMA-262 right-to-left
	// capture semantics.
	re := ecma262.MustCompile(`(?<=\$\s*)\d+`, flags.Flags(0))
	fmt.Println(re.FindString("total: $ 99"))
	// Output:
	// 99
}

func ExampleRegexp_ReplaceAllString() {
	// Without the g flag only the first match is replaced, as in JavaScript.
	re := ecma262.MustCompile(`\d+`, flags.Flags(0))
	fmt.Println(re.ReplaceAllString("a1b22c333", "X"))

	// With g, every match is replaced.
	re = ecma262.MustCompile(`\d+`, flags.Global)
	fmt.Println(re.ReplaceAllString("a1b22c333", "X"))
	// Output:
	// aXb22c333
	// aXbXcX
}

func ExampleRegexp_ReplaceAllString_substitution() {
	// The replacement string supports ECMA-262 $ substitutions:
	// $& (match), $n (group), $<name> (named group), $` and $' (context), $$ (literal $).
	re := ecma262.MustCompile(`(?<first>\w+) (?<last>\w+)`, flags.Flags(0))
	fmt.Println(re.ReplaceAllString("Ada Lovelace", "$<last>, $<first>"))

	re = ecma262.MustCompile(`(\d+)`, flags.Global)
	fmt.Println(re.ReplaceAllString("5 + 7", "[$1]"))
	// Output:
	// Lovelace, Ada
	// [5] + [7]
}

func ExampleRegexp_Split() {
	re := ecma262.MustCompile(`,\s*`, flags.Flags(0))
	fmt.Println(re.Split("apple, banana,  cherry", -1))
	// Output:
	// [apple banana cherry]
}

func ExampleRegexp_MatchString_global() {
	// A g-flagged Regexp is stateful: each MatchString starts at lastIndex
	// and advances it past the match, mirroring JavaScript's RegExp.test.
	re := ecma262.MustCompile(`\d+`, flags.Global)
	input := "a1 b22 c333"
	for re.MatchString(input) {
		fmt.Println(re.LastIndex())
	}
	// Output:
	// 2
	// 6
	// 11
}

func ExampleRegexp_MatchStringErr() {
	// The VM bounds backtracking as ReDoS protection. Boolean methods report
	// a limit hit as "no match"; the *Err variants surface it as an error.
	re := ecma262.MustCompile(`(a|aa)+$`, flags.Flags(0))
	re.SetMaxSteps(100)
	_, err := re.MatchStringErr("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab")
	fmt.Println(errors.Is(err, vm.ErrStepLimit))
	// Output:
	// true
}

func ExampleRegexp_MatchString_unicodeProperties() {
	// \p{...} requires the u (or v) flag and matches by Unicode property.
	re := ecma262.MustCompile(`^\p{Script=Greek}+$`, flags.Unicode)
	fmt.Println(re.MatchString("αβγ"))
	fmt.Println(re.MatchString("abc"))
	// Output:
	// true
	// false
}

func ExampleWithSyntax() {
	// By default the compiler accepts the Annex B web-compatibility syntax,
	// so patterns that work in browsers work here. Strict mode rejects it.
	re := ecma262.MustCompile(`a{2 x}`, flags.Flags(0)) // literal "a{2 x}" in Annex B
	fmt.Println(re.MatchString("a{2 x}"))

	_, err := ecma262.Compile(`a{2 x}`, flags.Flags(0), ecma262.WithSyntax(ecma262.SyntaxStrict))
	fmt.Println(err != nil)
	// Output:
	// true
	// true
}
