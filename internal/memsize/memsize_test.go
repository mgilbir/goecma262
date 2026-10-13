package memsize

import (
	"runtime"
	"testing"
	"unsafe"
)

var (
	sink        []byte
	sinkPointer []unsafe.Pointer
)

// Round and RoundNoScan must agree with what the running runtime allocates,
// so that the figures built on them describe real memory.
func TestRoundMatchesRuntime(t *testing.T) {
	sizes := []int64{1, 8, 9, 16, 17, 24, 25, 33, 49, 100, 1000, 1025, 4000, 8193, 20000, 32760, 32768, 32769, 40000, 100000, 1 << 20}
	for _, c := range sizeClasses {
		sizes = append(sizes, int64(c)-8, int64(c), int64(c)+1)
	}
	const word = int64(unsafe.Sizeof(unsafe.Pointer(nil)))
	for _, n := range sizes {
		if n <= 0 {
			continue
		}
		got := allocates(func() { sink = make([]byte, n) })
		want := RoundNoScan(n)
		if n < 16 {
			// The tiny allocator packs these into shared 16-byte blocks
			// (except under the race detector, which gives each a block):
			// RoundNoScan, which counts the block, bounds them.
			if got > want {
				t.Errorf("RoundNoScan(%d) = %d, but the runtime allocates %d", n, want, got)
			}
		} else if !near(got, want, n) {
			t.Errorf("RoundNoScan(%d) = %d, but the runtime allocates %d", n, want, got)
		}

		// An object with pointers is a whole number of words, and may carry
		// a malloc header.
		words := (n + word - 1) / word
		got = allocates(func() { sinkPointer = make([]unsafe.Pointer, words) })
		if want := Round(words * word); !near(got, want, n) {
			t.Errorf("Round(%d) = %d, but the runtime allocates %d", words*word, want, got)
		}
	}
}

// allocates returns the bytes the runtime counts for one call of alloc.
func allocates(alloc func()) int64 {
	// Allocated bytes are counted per size class; measure many allocations
	// so the tiny allocator (objects under 16 bytes without pointers) does
	// not blur the figure, and keep the least of several trials, since the
	// runtime itself may allocate during one.
	const reps, trials = 64, 5
	var ms runtime.MemStats
	got := int64(-1)
	for trial := 0; trial < trials; trial++ {
		runtime.GC()
		runtime.ReadMemStats(&ms)
		before := ms.TotalAlloc
		for i := 0; i < reps; i++ {
			alloc()
		}
		runtime.ReadMemStats(&ms)
		if per := int64(ms.TotalAlloc-before) / reps; got < 0 || per < got {
			got = per
		}
	}
	return got
}

// near reports whether got, measured for an object of n bytes, is want.
// Anything else the runtime allocates during a trial only adds, so got may
// exceed it by a few bytes, but by less than the gap to the next size class
// (8 bytes) or page (8 KiB).
func near(got, want, n int64) bool {
	tolerance := int64(8)
	if n > maxSmallSize {
		tolerance = 512
	}
	return got >= want && got-want < tolerance
}

func TestSaturation(t *testing.T) {
	if got := Add(maxInt64-1, 5); got != maxInt64 {
		t.Errorf("Add overflowed to %d", got)
	}
	if got := Mul(1<<40, 1<<40); got != maxInt64 {
		t.Errorf("Mul overflowed to %d", got)
	}
	if got := Round(maxInt64 - 1); got != maxInt64 {
		t.Errorf("Round overflowed to %d", got)
	}
	if Add(2, 3) != 5 || Mul(6, 7) != 42 || Round(0) != 0 {
		t.Error("Add, Mul or Round wrong on small values")
	}
}
