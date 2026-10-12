package ecma262_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/mgilbir/goecma262"
)

// footprintCases are patterns whose compiled form is large enough that the
// heap they retain can be measured against MemoryFootprint, each stressing a
// different kind of data an instruction owns.
func footprintCases() []struct{ name, pattern, flags string } {
	var named strings.Builder
	for i := 0; i < 3000; i++ {
		// Long names, so that they weigh as much as the program.
		fmt.Fprintf(&named, "(?<group%d_%s>a)", i, strings.Repeat("x", 300))
	}
	return []struct{ name, pattern, flags string }{
		{"literals", `(a{1000}){190}`, ""},
		{"class atoms", `([abcdefghijklmnopqrstuvwxyz0123456789]{1000}){50}`, ""},
		{"property names", `(?:\p{Lu}\P{Script=Greek}[\p{Nd}\p{Ll}]){10000}`, "u"},
		{"class set", `(?:[\p{L}--[a-z]]|[[0-9]&&\p{Nd}]){10000}`, "v"},
		{"string tries", `(?:\p{RGI_Emoji}){10}`, "v"},
		{"group names", named.String(), ""},
		{"backreferences", `(?:(?<x>a)|(?<x>b))(?:\k<x>){10000}`, ""},
		{"lookbehind", `(?:(?<=[\q{abc|abd|xyz}]a)b){5000}`, "v"},
	}
}

// retained returns the heap bytes that f's result keeps alive.
func retained(f func() any) (any, int64) {
	var ms runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&ms)
	before := ms.HeapAlloc
	v := f()
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&ms)
	return v, int64(ms.HeapAlloc) - int64(before)
}

func TestMemoryFootprintMatchesRetainedHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles large patterns")
	}
	for _, tc := range footprintCases() {
		t.Run(tc.name, func(t *testing.T) {
			// A first compile fills the caches shared by every pattern, which
			// the footprint does not count.
			if _, err := ecma262.CompileFlags(tc.pattern, tc.flags); err != nil {
				t.Fatal(err)
			}
			v, heap := retained(func() any {
				// The Regexp keeps its source alive, and the footprint
				// counts it, so the source is allocated here too.
				re, err := ecma262.CompileFlags(strings.Clone(tc.pattern), tc.flags)
				if err != nil {
					t.Fatal(err)
				}
				return re
			})
			re := v.(*ecma262.Regexp)
			fp := re.MemoryFootprint()
			t.Logf("%d instructions, footprint %d, retained %d (%.3f)", re.ProgramSize(), fp, heap, float64(fp)/float64(heap))
			// The footprint is an upper bound: it counts a small object
			// without pointers as the whole 16-byte block it may keep
			// alive. slack absorbs what the runtime itself allocates.
			const slack = 16 << 10
			if fp < heap-slack {
				t.Errorf("footprint %d under-counts the %d bytes retained", fp, heap)
			}
			if fp > heap+heap/20+slack {
				t.Errorf("footprint %d over-counts the %d bytes retained by more than 5%%", fp, heap)
			}
			runtime.KeepAlive(re)
		})
	}
}

func TestProgramSize(t *testing.T) {
	cases := map[string]int{
		`a`:      4, // save 0, char, save 0, match
		`a|b`:    7, // + split, jmp
		`(a)`:    6, // + save 1, save 1
		`a{3}`:   6,
		`(a){3}`: 3 + 3*3 + 2, // three copies of the group, two resets between them
	}
	for p, want := range cases {
		if got := ecma262.MustCompile(p, 0).ProgramSize(); got != want {
			t.Errorf("%s: ProgramSize %d, want %d", p, got, want)
		}
	}
}

func TestMemoryFootprintGrowsWithTheProgram(t *testing.T) {
	small := ecma262.MustCompile(`a{10}`, 0).MemoryFootprint()
	large := ecma262.MustCompile(`a{10000}`, 0).MemoryFootprint()
	if small <= 0 || large < 1000*small/10 {
		t.Errorf("footprints %d for a{10} and %d for a{10000}", small, large)
	}
}

// The footprint is computed lazily, so concurrent first calls must agree (and
// be race-free under -race).
func TestMemoryFootprintConcurrent(t *testing.T) {
	re := ecma262.MustCompile(`(?:[a-z]\p{L}|(?<n>x)\k<n>){100}`, 0)
	results := make(chan int64, 8)
	for i := 0; i < cap(results); i++ {
		go func() { results <- re.MemoryFootprint() }()
	}
	first := <-results
	for i := 1; i < cap(results); i++ {
		if got := <-results; got != first {
			t.Fatalf("concurrent footprints %d and %d differ", first, got)
		}
	}
}

// Every reference to a duplicated group name shares the list of its groups:
// a copy per reference would make a pattern with many of each quadratic.
func TestDuplicateNamedReferencesShareTheirGroups(t *testing.T) {
	const n = 5000
	var sb strings.Builder
	sb.WriteString("(?:")
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString("|")
		}
		sb.WriteString("(?<x>a)")
	}
	sb.WriteString(")")
	sb.WriteString(strings.Repeat(`\k<x>`, n))
	pattern := sb.String()
	var re *ecma262.Regexp
	allocated := allocatedBy(func() { re = ecma262.MustCompile(pattern, 0) })
	// Linear is a few MB; one list per reference would be n*n*8 = 200 MB.
	if allocated > 40<<20 {
		t.Errorf("compiling %d duplicate groups and references allocated %d bytes", n, allocated)
	}
	if fp := re.MemoryFootprint(); fp > 20<<20 {
		t.Errorf("footprint %d bytes", fp)
	}
}

// allocatedBy returns the bytes f allocates.
func allocatedBy(f func()) int64 {
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	before := ms.TotalAlloc
	f()
	runtime.ReadMemStats(&ms)
	return int64(ms.TotalAlloc - before)
}
