package main

import (
	"fmt"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
)

func main() {
	fmt.Println("ECMA-262 Regex Examples")
	fmt.Println("=======================")

	// Example 1: Basic matching
	fmt.Println("1. Basic Matching:")
	re := ecma262.MustCompile(`hello`, flags.Flags(0))
	fmt.Printf("   Pattern: 'hello', Input: 'hello world', Match: %v\n", re.MatchString("hello world"))

	// Example 2: Case insensitive
	fmt.Println("\n2. Case Insensitive (i flag):")
	re = ecma262.MustCompile(`hello`, flags.IgnoreCase)
	fmt.Printf("   Pattern: 'hello', Input: 'HELLO', Match: %v\n", re.MatchString("HELLO"))

	// Example 3: Character classes
	fmt.Println("\n3. Character Classes:")
	re = ecma262.MustCompile(`\d+`, flags.Flags(0))
	fmt.Printf("   Pattern: '\\d+', Input: 'abc123def', Find: %q\n", re.FindString("abc123def"))

	// Example 4: Capturing groups
	fmt.Println("\n4. Capturing Groups:")
	re = ecma262.MustCompile(`(\d{4})-(\d{2})-(\d{2})`, flags.Flags(0))
	matches := re.FindStringSubmatch("Date: 2024-03-15")
	fmt.Printf("   Pattern: '(\\d{4})-(\\d{2})-(\\d{2})'\n")
	fmt.Printf("   Input: 'Date: 2024-03-15'\n")
	fmt.Printf("   Matches: %v\n", matches)

	// Example 5: Named capture groups
	fmt.Println("\n5. Named Capture Groups:")
	re = ecma262.MustCompile(`(?<year>\d{4})-(?<month>\d{2})-(?<day>\d{2})`, flags.Flags(0))
	matches = re.FindStringSubmatch("Date: 2024-03-15")
	fmt.Printf("   Pattern: '(?<year>\\d{4})-(?<month>\\d{2})-(?<day>\\d{2})'\n")
	fmt.Printf("   Group names: %v\n", re.SubexpNames())
	fmt.Printf("   Matches: %v\n", matches)

	// Example 6: Alternation
	fmt.Println("\n6. Alternation:")
	re = ecma262.MustCompile(`cat|dog|bird`, flags.Flags(0))
	fmt.Printf("   Pattern: 'cat|dog|bird', Input: 'I have a dog', Find: %q\n", re.FindString("I have a dog"))

	// Example 7: Quantifiers
	fmt.Println("\n7. Quantifiers:")
	re = ecma262.MustCompile(`a{2,4}`, flags.Flags(0))
	fmt.Printf("   Pattern: 'a{2,4}', Input: 'baaaac', Find: %q\n", re.FindString("baaaac"))

	// Example 8: FindAll
	fmt.Println("\n8. FindAll:")
	re = ecma262.MustCompile(`\w+`, flags.Flags(0))
	allMatches := re.FindAllString("hello world test", -1)
	fmt.Printf("   Pattern: '\\w+', Input: 'hello world test'\n")
	fmt.Printf("   All matches: %v\n", allMatches)

	// Example 9: ReplaceAll
	fmt.Println("\n9. ReplaceAll:")
	re = ecma262.MustCompile(`\d`, flags.Flags(0))
	result := re.ReplaceAllString("a1b2c3", "X")
	fmt.Printf("   Pattern: '\\d', Input: 'a1b2c3', Replace with 'X': %q\n", result)

	// Example 10: Split
	fmt.Println("\n10. Split:")
	re = ecma262.MustCompile(`,\s*`, flags.Flags(0))
	parts := re.Split("apple, banana,  cherry", -1)
	fmt.Printf("    Pattern: ',\\s*', Input: 'apple, banana,  cherry'\n")
	fmt.Printf("    Parts: %v\n", parts)

	// Example 11: DotAll flag
	fmt.Println("\n11. DotAll Flag (s flag):")
	re = ecma262.MustCompile(`a.b`, flags.Flags(0))
	fmt.Printf("    Pattern: 'a.b', Input: 'a\\nb' without s flag, Match: %v\n", re.MatchString("a\nb"))
	re = ecma262.MustCompile(`a.b`, flags.DotAll)
	fmt.Printf("    Pattern: 'a.b', Input: 'a\\nb' with s flag, Match: %v\n", re.MatchString("a\nb"))

	// Example 12: Multiline flag
	fmt.Println("\n12. Multiline Flag (m flag):")
	re = ecma262.MustCompile(`^start`, flags.Flags(0))
	fmt.Printf("    Pattern: '^start', Input: 'line1\\nstart here', without m flag, Match: %v\n", re.MatchString("line1\nstart here"))
	re = ecma262.MustCompile(`^start`, flags.Multiline)
	fmt.Printf("    Pattern: '^start', Input: 'line1\\nstart here', with m flag, Match: %v\n", re.MatchString("line1\nstart here"))

	fmt.Println("\n✅ All examples completed successfully!")
}
