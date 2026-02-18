package ecma262_test

import (
	"testing"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/vm"
)

func TestBasicMatching(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   flags.Flags
		input   string
		want    bool
	}{
		{"literal match", "abc", flags.Flags(0), "abc", true},
		{"literal no match", "abc", flags.Flags(0), "def", false},
		{"literal in text", "abc", flags.Flags(0), "xyzabc123", true},
		{"dot matches char", "a.c", flags.Flags(0), "abc", true},
		{"dot matches digit", "a.c", flags.Flags(0), "a1c", true},
		{"dot no match newline", "a.c", flags.Flags(0), "a\nc", false},
		{"dot matches newline with s flag", "a.c", flags.DotAll, "a\nc", true},
		{"case insensitive", "abc", flags.IgnoreCase, "ABC", true},
		{"alternation first", "a|b", flags.Flags(0), "a", true},
		{"alternation second", "a|b", flags.Flags(0), "b", true},
		{"alternation no match", "a|b", flags.Flags(0), "c", false},
		{"start anchor match", "^abc", flags.Flags(0), "abc", true},
		{"start anchor no match", "^abc", flags.Flags(0), "xabc", false},
		{"end anchor match", "abc$", flags.Flags(0), "abc", true},
		{"end anchor no match", "abc$", flags.Flags(0), "abcx", false},
		{"word boundary", `\bword\b`, flags.Flags(0), "word", true},
		{"not word boundary", `\Bword\B`, flags.Flags(0), "swordfish", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := ecma262.Compile(tt.pattern, tt.flags)
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}

			got := re.MatchString(tt.input)
			if got != tt.want {
				t.Errorf("MatchString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestCharacterClasses(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   flags.Flags
		input   string
		want    bool
	}{
		{"digit class", `\d`, flags.Flags(0), "5", true},
		{"digit class no match", `\d`, flags.Flags(0), "a", false},
		{"non-digit class", `\D`, flags.Flags(0), "a", true},
		{"non-digit class no match", `\D`, flags.Flags(0), "5", false},
		{"word class", `\w`, flags.Flags(0), "a", true},
		{"word class underscore", `\w`, flags.Flags(0), "_", true},
		{"word class no match", `\w`, flags.Flags(0), "!", false},
		{"non-word class", `\W`, flags.Flags(0), "!", true},
		{"non-word class no match", `\W`, flags.Flags(0), "a", false},
		{"space class", `\s`, flags.Flags(0), " ", true},
		{"space class tab", `\s`, flags.Flags(0), "\t", true},
		{"space class no match", `\s`, flags.Flags(0), "a", false},
		{"non-space class", `\S`, flags.Flags(0), "a", true},
		{"non-space class no match", `\S`, flags.Flags(0), " ", false},
		{"char class match", `[abc]`, flags.Flags(0), "b", true},
		{"char class no match", `[abc]`, flags.Flags(0), "d", false},
		{"char class negated", `[^abc]`, flags.Flags(0), "d", true},
		{"char class negated no match", `[^abc]`, flags.Flags(0), "a", false},
		{"char class range", `[a-z]`, flags.Flags(0), "m", true},
		{"char class range no match", `[a-z]`, flags.Flags(0), "5", false},
		{"class escape digit", `[\d]+`, flags.Flags(0), "123", true},
		{"class escape non-digit", `[\D]+`, flags.Flags(0), "abc", true},
		{"class escape word", `[\w]+`, flags.Flags(0), "abc_123", true},
		{"class escape space", `[\s]+`, flags.Flags(0), " \t", true},
		{"class escape unicode property", `[\p{L}]+`, flags.Unicode, "abc", true},
		{"class escape unicode property negated", `[\P{L}]+`, flags.Unicode, "123", true},
		{"class escape unicode property upper", `[\p{Lu}]+`, flags.Unicode, "ABC", true},
		{"class escape unicode property upper no match", `[\p{Lu}]+`, flags.Unicode, "abc", false},
		{"class negate unicode property", `[^\p{L}]+`, flags.Unicode, "123", true},
		{"class negate unicode property no match", `[^\p{L}]+`, flags.Unicode, "abc", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := ecma262.Compile(tt.pattern, tt.flags)
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}

			got := re.MatchString(tt.input)
			if got != tt.want {
				t.Errorf("MatchString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestLookbehindFixedLength(t *testing.T) {
	_, err := ecma262.Compile(`(?<=a+)b`, flags.Flags(0))
	if err == nil {
		t.Fatal("expected error for variable-length lookbehind")
	}
}

func TestMatchStringErr(t *testing.T) {
	re, err := ecma262.Compile(`abc`, flags.Flags(0))
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	matched, err := re.MatchStringErr("xxabcxx")
	if err != nil {
		t.Fatalf("MatchStringErr unexpected error: %v", err)
	}
	if !matched {
		t.Fatalf("MatchStringErr expected match")
	}
}

func TestMatchStringErrStepLimit(t *testing.T) {
	re, err := ecma262.Compile(`a`, flags.Flags(0))
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	re.SetMaxSteps(1)

	matched, err := re.MatchStringErr("a")
	if err == nil {
		t.Fatalf("expected step limit error")
	}
	if err != vm.ErrStepLimit {
		t.Fatalf("expected ErrStepLimit, got %v", err)
	}
	if matched {
		t.Fatalf("expected no match due to step limit")
	}
}

func TestFindStringIndexErrStepLimit(t *testing.T) {
	re, err := ecma262.Compile(`a`, flags.Flags(0))
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	re.SetMaxSteps(1)

	idx, err := re.FindStringIndexErr("a")
	if err == nil {
		t.Fatalf("expected step limit error")
	}
	if err != vm.ErrStepLimit {
		t.Fatalf("expected ErrStepLimit, got %v", err)
	}
	if idx != nil {
		t.Fatalf("expected nil index due to step limit")
	}
}

func TestFindStringIndexErr(t *testing.T) {
	re, err := ecma262.Compile(`abc`, flags.Flags(0))
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	idx, err := re.FindStringIndexErr("xxabcxx")
	if err != nil {
		t.Fatalf("FindStringIndexErr unexpected error: %v", err)
	}
	if idx == nil || idx[0] != 2 || idx[1] != 5 {
		t.Fatalf("FindStringIndexErr got %v, want [2 5]", idx)
	}
}

func TestQuantifiers(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		input   string
		want    bool
	}{
		{"star zero matches", `a*`, "", true},
		{"star one match", `a*`, "a", true},
		{"star multiple", `a*`, "aaaa", true},
		{"plus zero matches", `a+`, "", false},
		{"plus one match", `a+`, "a", true},
		{"plus multiple", `a+`, "aaaa", true},
		{"optional present", `a?`, "a", true},
		{"optional absent", `a?`, "", true},
		{"exact count", `a{3}`, "aaa", true},
		{"exact count no match", `a{3}`, "aa", false},
		{"min max", `a{2,4}`, "aaa", true},
		{"min max too few", `a{2,4}`, "a", false},
		{"min max too many", `a{2,4}`, "aaaaa", true}, // Matches first 4
		{"greedy vs non-greedy", `a+?`, "aaaa", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := ecma262.Compile(tt.pattern, flags.Flags(0))
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}

			got := re.MatchString(tt.input)
			if got != tt.want {
				t.Errorf("MatchString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGroups(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		input   string
		want    []string
	}{
		{
			name:    "simple group",
			pattern: `(abc)`,
			input:   "abc",
			want:    []string{"abc", "abc"},
		},
		{
			name:    "multiple groups",
			pattern: `(a)(b)(c)`,
			input:   "abc",
			want:    []string{"abc", "a", "b", "c"},
		},
		{
			name:    "nested groups",
			pattern: `((a)(b))`,
			input:   "ab",
			want:    []string{"ab", "ab", "a", "b"},
		},
		{
			name:    "group with quantifier",
			pattern: `(ab)+`,
			input:   "abab",
			want:    []string{"abab", "ab"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := ecma262.Compile(tt.pattern, flags.Flags(0))
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}

			got := re.FindStringSubmatch(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("FindStringSubmatch() returned %d groups, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Group %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFindAll(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		input   string
		n       int
		want    []string
	}{
		{
			name:    "find all words",
			pattern: `\w+`,
			input:   "hello world test",
			n:       -1,
			want:    []string{"hello", "world", "test"},
		},
		{
			name:    "find limited",
			pattern: `\w+`,
			input:   "hello world test",
			n:       2,
			want:    []string{"hello", "world"},
		},
		{
			name:    "no matches",
			pattern: `\d+`,
			input:   "hello world",
			n:       -1,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := ecma262.Compile(tt.pattern, flags.Flags(0))
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}

			got := re.FindAllString(tt.input, tt.n)
			if len(got) != len(tt.want) {
				t.Errorf("FindAllString() returned %d matches, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Match %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestReplaceAll(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		input   string
		repl    string
		want    string
	}{
		{
			name:    "replace all digits",
			pattern: `\d`,
			input:   "a1b2c3",
			repl:    "X",
			want:    "aXbXcX",
		},
		{
			name:    "replace words",
			pattern: `\w+`,
			input:   "hello world",
			repl:    "WORD",
			want:    "WORD WORD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := ecma262.Compile(tt.pattern, flags.Flags(0))
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}

			got := re.ReplaceAllString(tt.input, tt.repl)
			if got != tt.want {
				t.Errorf("ReplaceAllString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSplit(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		input   string
		n       int
		want    []string
	}{
		{
			name:    "split by comma",
			pattern: `,`,
			input:   "a,b,c",
			n:       -1,
			want:    []string{"a", "b", "c"},
		},
		{
			name:    "split by whitespace",
			pattern: `\s+`,
			input:   "hello   world",
			n:       -1,
			want:    []string{"hello", "world"},
		},
		{
			name:    "split limited",
			pattern: `,`,
			input:   "a,b,c,d",
			n:       2,
			want:    []string{"a", "b,c,d"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := ecma262.Compile(tt.pattern, flags.Flags(0))
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}

			got := re.Split(tt.input, tt.n)
			if len(got) != len(tt.want) {
				t.Errorf("Split() returned %d parts, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Part %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
	}{
		{"unclosed group", `(abc`},
		{"unclosed char class", `[abc`},
		{"invalid escape", `\`},
		// Note: ECMA-262 allows backreferences to non-existent groups at parse time
		// They just fail to match at runtime. So \9 is valid syntax.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ecma262.Compile(tt.pattern, flags.Flags(0))
			if err == nil {
				t.Error("Expected compile error, got nil")
			}
		})
	}
}

func BenchmarkMatch(b *testing.B) {
	re, err := ecma262.Compile(`[a-z]+`, flags.Flags(0))
	if err != nil {
		b.Fatal(err)
	}
	input := "abcdefghij"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		re.MatchString(input)
	}
}

func BenchmarkCompileAndMatch(b *testing.B) {
	pattern := `[a-z]+`
	input := "abcdefghij"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		re, _ := ecma262.Compile(pattern, flags.Flags(0))
		re.MatchString(input)
	}
}
