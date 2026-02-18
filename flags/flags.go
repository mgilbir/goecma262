// Package flags defines ECMA-262 regular expression flags
package flags

import "strings"

// Flags represents ECMA-262 regex flags
type Flags uint16

const (
	// IgnoreCase - i flag: case-insensitive matching
	IgnoreCase Flags = 1 << iota
	// Global - g flag: find all matches rather than stopping after first
	Global
	// Multiline - m flag: ^ and $ match start/end of lines, not just string
	Multiline
	// DotAll - s flag: dot (.) matches newlines
	DotAll
	// Unicode - u flag: enable Unicode features
	Unicode
	// UnicodeSets - v flag: Unicode sets mode (more Unicode features)
	UnicodeSets
	// Sticky - y flag: match only from lastIndex position
	Sticky
	// HasIndices - d flag: include match indices in result
	HasIndices
)

// DuplicateFlagError is returned when the same flag appears more than once
type DuplicateFlagError struct {
	Flag rune
}

func (e *DuplicateFlagError) Error() string {
	return "duplicate flag: " + string(e.Flag)
}

// Parse parses a flag string and returns the corresponding Flags value.
// Returns an error for invalid flags, duplicate flags, or incompatible flags (u and v).
func Parse(s string) (Flags, error) {
	var f Flags
	seen := make(map[rune]bool, len(s))
	for _, c := range s {
		if seen[c] {
			return 0, &DuplicateFlagError{Flag: c}
		}
		seen[c] = true
		switch c {
		case 'i':
			f |= IgnoreCase
		case 'g':
			f |= Global
		case 'm':
			f |= Multiline
		case 's':
			f |= DotAll
		case 'u':
			f |= Unicode
		case 'v':
			f |= UnicodeSets
		case 'y':
			f |= Sticky
		case 'd':
			f |= HasIndices
		default:
			return 0, &InvalidFlagError{Flag: c}
		}
	}
	// u and v flags are mutually exclusive
	if f&Unicode != 0 && f&UnicodeSets != 0 {
		return 0, &IncompatibleFlagsError{Flags: "u and v"}
	}
	return f, nil
}

// String returns the string representation of flags
func (f Flags) String() string {
	var sb strings.Builder
	if f&IgnoreCase != 0 {
		sb.WriteByte('i')
	}
	if f&Global != 0 {
		sb.WriteByte('g')
	}
	if f&Multiline != 0 {
		sb.WriteByte('m')
	}
	if f&DotAll != 0 {
		sb.WriteByte('s')
	}
	if f&Unicode != 0 {
		sb.WriteByte('u')
	}
	if f&UnicodeSets != 0 {
		sb.WriteByte('v')
	}
	if f&Sticky != 0 {
		sb.WriteByte('y')
	}
	if f&HasIndices != 0 {
		sb.WriteByte('d')
	}
	return sb.String()
}

// Has returns true if the given flag is set
func (f Flags) Has(flag Flags) bool {
	return f&flag != 0
}

// InvalidFlagError is returned when an invalid flag character is encountered
type InvalidFlagError struct {
	Flag rune
}

func (e *InvalidFlagError) Error() string {
	return "invalid flag: " + string(e.Flag)
}

// IncompatibleFlagsError is returned when incompatible flags are used together
type IncompatibleFlagsError struct {
	Flags string
}

func (e *IncompatibleFlagsError) Error() string {
	return "incompatible flags: " + e.Flags
}
