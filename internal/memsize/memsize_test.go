package memsize

import (
	"runtime"
	"testing"
)

var sink []byte

// Round must agree with what the running runtime allocates, so that the
// figures built on it describe real memory.
func TestRoundMatchesRuntime(t *testing.T) {
	sizes := []int64{1, 8, 9, 16, 17, 24, 25, 33, 49, 100, 1000, 1025, 4000, 8193, 20000, 32768, 32769, 40000, 100000, 1 << 20}
	for _, c := range sizeClasses {
		sizes = append(sizes, int64(c), int64(c)+1)
	}
	var ms runtime.MemStats
	for _, n := range sizes {
		// Allocated bytes are counted per size class; measure many
		// allocations so the tiny allocator (objects under 16 bytes without
		// pointers) does not blur the figure, and keep the least of several
		// trials, since the runtime itself may allocate during one.
		const reps, trials = 64, 5
		got := int64(-1)
		for trial := 0; trial < trials; trial++ {
			runtime.GC()
			runtime.ReadMemStats(&ms)
			before := ms.TotalAlloc
			for i := 0; i < reps; i++ {
				sink = make([]byte, n)
			}
			runtime.ReadMemStats(&ms)
			if per := int64(ms.TotalAlloc-before) / reps; got < 0 || per < got {
				got = per
			}
		}
		want := Round(n)
		if n < 16 {
			// The tiny allocator packs these into shared 16-byte blocks
			// (except under the race detector, which gives each a block):
			// RoundNoScan, which counts the block, bounds them.
			if got > RoundNoScan(n) {
				t.Errorf("RoundNoScan(%d) = %d, but the runtime allocates %d", n, RoundNoScan(n), got)
			}
			continue
		}
		// Anything else the runtime allocates during a trial only adds, so
		// got may exceed the object's size by a few bytes, but by less than
		// the gap to the next size class (8 bytes) or page (8 KiB).
		tolerance := int64(8)
		if n > maxSmallSize {
			tolerance = 512
		}
		if got < want || got-want >= tolerance {
			t.Errorf("Round(%d) = %d, but the runtime allocates %d", n, want, got)
		}
	}
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
