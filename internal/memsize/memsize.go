// Package memsize estimates how much memory the Go allocator takes for an
// object, so that the library can report what a compiled pattern holds.
package memsize

import "unsafe"

// sizeClasses are the object sizes of the Go runtime's small size classes
// (internal/runtime/gc.SizeClassToSize, unchanged since Go 1.12). A small
// object takes the smallest class that holds it.
var sizeClasses = [...]uint16{8, 16, 24, 32, 48, 64, 80, 96, 112, 128, 144, 160, 176, 192, 208, 224, 240, 256, 288, 320, 352, 384, 416, 448, 480, 512, 576, 640, 704, 768, 896, 1024, 1152, 1280, 1408, 1536, 1792, 2048, 2304, 2688, 3072, 3200, 3456, 4096, 4864, 5376, 6144, 6528, 6784, 6912, 8192, 9472, 9728, 10240, 10880, 12288, 13568, 14336, 16384, 18432, 19072, 20480, 21760, 24576, 27264, 28672, 32768}

const (
	maxSmallSize = 32768
	pageSize     = 8192

	// The malloc header of a small object with pointers, and the size above
	// which such an object has one (internal/runtime/gc.MallocHeaderSize and
	// MinSizeForMallocHeader: 512 bytes on 64-bit platforms, 128 on 32-bit).
	mallocHeaderSize       = 8
	minSizeForMallocHeader = int64(unsafe.Sizeof(uintptr(0)) * unsafe.Sizeof(uintptr(0)) * 8)
)

// Round returns the bytes the Go allocator takes for an object of n bytes
// that holds pointers: its size class for a small object, whole pages for a
// large one. Since Go 1.22 a small object with pointers of more than
// minSizeForMallocHeader bytes carries a header describing them, which the
// size class must hold too. It saturates rather than overflow.
// TestRoundMatchesRuntime checks it against the running runtime.
func Round(n int64) int64 {
	if n > minSizeForMallocHeader && n <= maxSmallSize-mallocHeaderSize {
		return sizeClass(n + mallocHeaderSize)
	}
	return sizeClass(n)
}

// RoundNoScan is Round for an object that holds no pointers, such as a
// string or a slice of integers. Such an object has no header. The runtime
// packs those under 16 bytes into shared 16-byte blocks, and one live object
// keeps its whole block alive, so RoundNoScan counts the block: an upper
// bound for these objects, never an under-count.
func RoundNoScan(n int64) int64 {
	if n > 0 && n < tinySize {
		return tinySize
	}
	return sizeClass(n)
}

// sizeClass returns the bytes the allocator takes for a block of n bytes,
// any header included.
func sizeClass(n int64) int64 {
	switch {
	case n <= 0:
		return 0
	case n <= maxSmallSize:
		lo, hi := 0, len(sizeClasses)-1
		for lo < hi {
			mid := (lo + hi) / 2
			if int64(sizeClasses[mid]) < n {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return int64(sizeClasses[lo])
	case n > maxInt64-pageSize:
		return maxInt64
	}
	return (n + pageSize - 1) / pageSize * pageSize
}

const tinySize = 16

const maxInt64 = int64(^uint64(0) >> 1)

// Add returns a+b, saturating at the largest int64; both are non-negative.
func Add(a, b int64) int64 {
	if a > maxInt64-b {
		return maxInt64
	}
	return a + b
}

// Mul returns a*b, saturating at the largest int64; both are non-negative.
func Mul(a, b int64) int64 {
	if a != 0 && b > maxInt64/a {
		return maxInt64
	}
	return a * b
}
