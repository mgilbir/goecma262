package vm

import (
	"unsafe"

	"github.com/mgilbir/goecma262/internal/memsize"
)

// Footprint returns the bytes p holds, as the Go allocator sizes them: the
// instruction slice it was built from, what the instructions own (class
// atoms, property names, class sets, rune switches, the group lists of
// backreferences) and p's own analysis. An object that several instructions
// share is counted once. Tables shared by every program, such as the
// resolved Unicode properties, are not counted, and neither is the memory a
// VM uses while matching, which ErrStepLimit's memory budget bounds.
//
// It is computed on first use and safe to call concurrently.
func (p *Program) Footprint() int64 {
	p.footprintOnce.Do(func() { p.footprint = p.computeFootprint() })
	return p.footprint
}

func (p *Program) computeFootprint() int64 {
	s := &sizer{}
	s.add(int64(unsafe.Sizeof(*p)))
	s.slice(unsafe.Pointer(unsafe.SliceData(p.code)), cap(p.code), unsafe.Sizeof(Instruction{}))
	for i := range p.code {
		s.instruction(&p.code[i])
	}
	s.slice(unsafe.Pointer(unsafe.SliceData(p.first)), cap(p.first), unsafe.Sizeof((*firstSet)(nil)))
	for _, fs := range p.first {
		if fs != nil && fs != anyFirst && s.once(unsafe.Pointer(fs)) {
			s.addNoScan(int64(unsafe.Sizeof(*fs)))
		}
	}
	s.sliceNoScan(unsafe.Pointer(unsafe.SliceData(p.loops)), cap(p.loops), unsafe.Sizeof(greedyLoop{}))
	s.sliceNoScan(unsafe.Pointer(unsafe.SliceData(p.dir)), cap(p.dir), unsafe.Sizeof(false))
	return s.n
}

// sizer adds up allocations, counting each distinct one once.
type sizer struct {
	seen map[unsafe.Pointer]struct{}
	n    int64
}

func (s *sizer) add(bytes int64) { s.n = memsize.Add(s.n, memsize.Round(bytes)) }

// addNoScan adds an object that holds no pointers (see memsize.RoundNoScan).
func (s *sizer) addNoScan(bytes int64) { s.n = memsize.Add(s.n, memsize.RoundNoScan(bytes)) }

// once reports whether ptr has not been counted yet, marking it counted.
func (s *sizer) once(ptr unsafe.Pointer) bool {
	if ptr == nil {
		return false
	}
	if _, ok := s.seen[ptr]; ok {
		return false
	}
	if s.seen == nil {
		s.seen = make(map[unsafe.Pointer]struct{})
	}
	s.seen[ptr] = struct{}{}
	return true
}

// slice counts the backing array at data of capacity n and element size
// elem, whose elements hold pointers.
func (s *sizer) slice(data unsafe.Pointer, n int, elem uintptr) bool {
	if n == 0 || !s.once(data) {
		return false
	}
	s.add(memsize.Mul(int64(n), int64(elem)))
	return true
}

// sliceNoScan is slice for elements without pointers.
func (s *sizer) sliceNoScan(data unsafe.Pointer, n int, elem uintptr) {
	if n > 0 && s.once(data) {
		s.addNoScan(memsize.Mul(int64(n), int64(elem)))
	}
}

func (s *sizer) str(x string) {
	if len(x) > 0 && s.once(unsafe.Pointer(unsafe.StringData(x))) {
		s.addNoScan(int64(len(x)))
	}
}

func (s *sizer) instruction(inst *Instruction) {
	s.str(inst.Prop)
	if s.slice(unsafe.Pointer(unsafe.SliceData(inst.Class)), cap(inst.Class), unsafe.Sizeof(ClassAtom{})) {
		for i := range inst.Class {
			s.str(inst.Class[i].Prop)
		}
	}
	s.charSet(inst.Set)
	if sw := inst.Switch; sw != nil && s.once(unsafe.Pointer(sw)) {
		s.add(int64(unsafe.Sizeof(*sw)))
		s.sliceNoScan(unsafe.Pointer(unsafe.SliceData(sw.Keys)), cap(sw.Keys), unsafe.Sizeof(rune(0)))
		s.sliceNoScan(unsafe.Pointer(unsafe.SliceData(sw.Targets)), cap(sw.Targets), unsafe.Sizeof(int(0)))
	}
	s.sliceNoScan(unsafe.Pointer(unsafe.SliceData(inst.AltA)), cap(inst.AltA), unsafe.Sizeof(int(0)))
}

func (s *sizer) charSet(cs *CharSet) {
	if cs == nil || !s.once(unsafe.Pointer(cs)) {
		return
	}
	s.add(int64(unsafe.Sizeof(*cs)))
	s.sliceNoScan(unsafe.Pointer(unsafe.SliceData(cs.Ranges)), cap(cs.Ranges), unsafe.Sizeof(RuneRange{}))
	s.str(cs.Atom.Prop)
	s.slice(unsafe.Pointer(unsafe.SliceData(cs.Items)), cap(cs.Items), unsafe.Sizeof((*CharSet)(nil)))
	for _, it := range cs.Items {
		s.charSet(it)
	}
}
