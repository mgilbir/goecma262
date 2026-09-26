package vm

import "encoding/binary"

// The failure memo records which OpSplit states have already been visited in
// the current search, so a revisit takes the split's B branch instead of
// re-exploring branch A (see the OpSplit case in run).
//
// A state is (pc, pos) plus, when the program has backreferences or an
// empty-matching loop (Program.exact), the full capture vector. Capture vectors
// are interned to small ids, and the positions visited for each (pc, id) are
// kept in a paged bitset, so marking costs one bit per visited position rather
// than one allocated string per visit.

const (
	memoPageBits  = 1024
	memoPageWords = memoPageBits / 64
	memoPageBytes = memoPageWords * 8
	// memoEntryOverhead approximates the map/bookkeeping bytes behind each
	// page, set or interned key, for memory accounting.
	memoEntryOverhead = 64
)

type memoPage [memoPageWords]uint64

type posSet struct {
	pages   map[int]*memoPage
	lastIdx int
	last    *memoPage
}

type memoKey struct {
	pc  int
	gid int32
}

type memo struct {
	sets    map[memoKey]*posSet
	lastKey memoKey
	lastSet *posSet
	gids    map[string]int32
	keyBuf  []byte
	bytes   int // memory accounted to this memo
}

func (m *memo) reset() {
	*m = memo{keyBuf: m.keyBuf[:0]}
}

// internGroups returns the id of the capture vector g, charging new entries to
// the memo's byte count.
func (m *memo) internGroups(g []int) int32 {
	buf := m.keyBuf[:0]
	for _, v := range g {
		buf = binary.AppendVarint(buf, int64(v))
	}
	m.keyBuf = buf
	if id, ok := m.gids[string(buf)]; ok {
		return id
	}
	if m.gids == nil {
		m.gids = make(map[string]int32)
	}
	id := int32(len(m.gids) + 1)
	m.gids[string(buf)] = id
	m.bytes += len(buf) + memoEntryOverhead
	return id
}

// visit reports whether (key, pos) was already marked, marking it if not.
func (m *memo) visit(key memoKey, pos int) bool {
	s := m.lastSet
	if s == nil || key != m.lastKey {
		if m.sets == nil {
			m.sets = make(map[memoKey]*posSet)
		}
		s = m.sets[key]
		if s == nil {
			s = &posSet{pages: make(map[int]*memoPage), lastIdx: -1}
			m.sets[key] = s
			m.bytes += memoEntryOverhead
		}
		m.lastKey, m.lastSet = key, s
	}
	idx := pos / memoPageBits
	page := s.last
	if idx != s.lastIdx {
		page = s.pages[idx]
		if page == nil {
			page = new(memoPage)
			s.pages[idx] = page
			m.bytes += memoPageBytes + memoEntryOverhead
		}
		s.lastIdx, s.last = idx, page
	}
	bit := pos % memoPageBits
	w, b := bit/64, uint64(1)<<(bit%64)
	if page[w]&b != 0 {
		return true
	}
	page[w] |= b
	return false
}
