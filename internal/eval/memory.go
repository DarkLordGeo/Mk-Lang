// Why: compile-time values must have the target's exact memory layout, so a
// const built here is the bytes the native program would hold, while no
// evaluation may forge a pointer or read memory it does not own.
//
// What: memory (the allocation table), alloc and arec, pointer words
// (ptrWord, ptrAlloc, ptrOff), newAlloc, free, access, the loads and stores,
// copyBytes, and bytePool.
//
// How: each allocation is a byte slice with two bitsets: ptr marks where a
// pointer word starts, frag marks bytes of a cut or partly overwritten
// pointer word. Pointer bytes are never read as integers, and a fragment is
// never read as anything. A pointer word is an allocation number and an
// offset. Const copies are read-only and a var global's memory is never
// accessed. A freed allocation's number stays dead while its slices are
// reused; slices come zeroed from a per-size-class pool.

package eval

import (
	"math/bits"
	"sync"

	"mk/internal/types"
)

// allocKind is what an allocation holds.
type allocKind uint8

const (
	allocNone allocKind = iota
	// allocFrame is one call's locals.
	allocFrame
	// allocOwn is memory the evaluation allocated: heap, page, arena, host
	// results, scratch.
	allocOwn
	// allocConst is the materialized read-only copy of a const value.
	allocConst
	// allocFn is a function value: size 0, named by its FnRef.
	allocFn
	// allocGlobal stands for a var global: never readable or writable.
	allocGlobal
)

// arec is one allocation number's record: its kind, whether it is live, and
// while it is live the index of its data record. A dead allocation keeps only
// this record (8 bytes): its data record and slices are reused, so the number
// stays dead and dangling pointers stay refused while the memory is reused.
type arec struct {
	d    uint32
	kind allocKind
	live bool
}

// alloc is a live allocation's data record.
type alloc struct {
	bytes []byte
	// ptr: bit o set means a pointer word starts at byte o. nil means none.
	ptr []uint64
	// frag: bit o set means byte o is a fragment of a cut or partly
	// overwritten pointer word. nil means none.
	frag []uint64
	// ptag is the tagged mode's tag shadow: the tag of the loan the pointer
	// word at each offset carries, kept by clearRange and copyBytes beside the
	// ptr bits; nil in plain mode, and a word with no entry carries the
	// owner's tag 0.
	ptag map[uint32]uint32
	// pending: an allocConst whose bytes are materialized on first access.
	pending bool
	cid     types.ConstID
	fn      types.FnRef
	item    types.ItemID
}

// A pointer word is 8 bytes: the low 32 bits the offset, the high 32 bits the
// allocation number (at least 1). Null is all zero with no tag.
func ptrWord(a, off uint32) uint64 { return uint64(a)<<32 | uint64(off) }

func ptrAlloc(w uint64) uint32 { return uint32(w >> 32) }

func ptrOff(w uint64) uint32 { return uint32(w) }

// memory is one evaluation's allocations; number 0 is reserved and numbers
// are never reused within the evaluation. recs has one record per number;
// data the live allocations' data records, index 0 the empty record of no
// allocation, freed indexes reused through freeData.
type memory struct {
	recs     []arec
	data     []alloc
	freeData []uint32
	pool     *bytePool
	// materialize fills a pending allocConst (constval.go).
	materialize func(a uint32)
	// scratch for copies: source tags and fragments, relative offsets, and
	// in the tagged mode the moving words' tag shadows.
	tagBuf, fragBuf, ptagBuf []uint32
	// tagged: the evaluation runs in the tagged mode, so copies carry the
	// tag shadow.
	tagged bool
	// spare holds a few freed slices per size class for this evaluation's
	// next allocations (frames, mostly), so a call and its return do not go
	// through the shared pool; cleared before reuse like the pool's.
	spare [spareClasses][][]byte
}

// spareClasses are the size classes kept in memory.spare (up to 64 KiB);
// spareMax the slices kept per class.
const (
	spareClasses = 17
	spareMax     = 16
)

// getBytes returns a zeroed slice of n bytes, from spare or the pool.
func (m *memory) getBytes(n int) []byte {
	if n == 0 {
		return nil
	}
	if c := sizeClass(n); c < spareClasses {
		if l := len(m.spare[c]); l > 0 {
			b := m.spare[c][l-1][:n]
			m.spare[c] = m.spare[c][:l-1]
			clear(b)
			return b
		}
	}
	return m.pool.get(n)
}

// putBytes keeps a freed slice in spare, or returns it to the pool.
func (m *memory) putBytes(b []byte) {
	if c := sizeClass(cap(b)); cap(b) > 0 && c < spareClasses && cap(b) == 1<<c && len(m.spare[c]) < spareMax {
		m.spare[c] = append(m.spare[c], b)
		return
	}
	m.pool.put(b)
}

func newMemory(pool *bytePool) memory {
	return memory{recs: make([]arec, 1, 64), data: make([]alloc, 1, 16), pool: pool}
}

// release returns every live allocation's slices to the pool.
func (m *memory) release() {
	for i := range m.recs {
		m.free(uint32(i))
	}
	m.recs = m.recs[:1]
	m.data = m.data[:1]
	m.freeData = m.freeData[:0]
	for c := range m.spare {
		for _, b := range m.spare[c] {
			m.pool.put(b)
		}
		m.spare[c] = nil
	}
}

// add makes a live allocation of kind with data record al and returns its
// number.
func (m *memory) add(kind allocKind, al alloc) uint32 {
	a := uint32(len(m.recs))
	if uint64(a) >= maxAllocs {
		refuse(types.RefuseRawAccess, "too many allocations")
	}
	var d uint32
	if n := len(m.freeData); n > 0 {
		d = m.freeData[n-1]
		m.freeData = m.freeData[:n-1]
		m.data[d] = al
	} else {
		d = uint32(len(m.data))
		m.data = append(m.data, al)
	}
	m.recs = append(m.recs, arec{d: d, kind: kind, live: true})
	return a
}

// addDead makes a number that names no memory: every access to it is
// RefuseRawAccess.
func (m *memory) addDead(kind allocKind) uint32 {
	a := uint32(len(m.recs))
	if uint64(a) >= maxAllocs {
		refuse(types.RefuseRawAccess, "too many allocations")
	}
	m.recs = append(m.recs, arec{kind: kind})
	return a
}

// at is the data record of live allocation a. The pointer is valid until the
// next allocation.
func (m *memory) at(a uint32) *alloc { return &m.data[m.recs[a].d] }

// kindOf is the kind of allocation number a, allocNone for no number.
func (m *memory) kindOf(a uint32) allocKind {
	if a == 0 || int(a) >= len(m.recs) {
		return allocNone
	}
	return m.recs[a].kind
}

// isLive reports whether a names a live allocation.
func (m *memory) isLive(a uint32) bool {
	return a != 0 && int(a) < len(m.recs) && m.recs[a].live
}

// newAlloc makes a zeroed allocation of n bytes. The caller has
// charged it. An allocation larger than offsets can address is refused.
func (m *memory) newAlloc(kind allocKind, n uint64) uint32 {
	if n > maxAlloc {
		refuse(types.RefuseRawAccess, "allocation too large")
	}
	if uint64(len(m.recs)) >= maxAllocs {
		refuse(types.RefuseRawAccess, "too many allocations")
	}
	return m.add(kind, alloc{bytes: m.getBytes(int(n))})
}

// maxAlloc keeps every offset, and offset plus 8, inside 32 bits.
const maxAlloc = 1<<32 - 16

// maxAllocs keeps allocation numbers inside the high 32 bits of a word.
const maxAllocs = 1<<32 - 1

// free marks an allocation dead and returns its slices to the pool and its
// data record for reuse; the number keeps its 8-byte record.
func (m *memory) free(a uint32) {
	r := &m.recs[a]
	if !r.live {
		return
	}
	if r.d != 0 {
		al := &m.data[r.d]
		m.putBytes(al.bytes)
		*al = alloc{}
		m.freeData = append(m.freeData, r.d)
	}
	*r = arec{kind: r.kind}
}

// get returns a live allocation, materializing a pending const copy. Any
// access to a dead or unknown allocation is RefuseRawAccess.
func (m *memory) get(a uint32) *alloc {
	if !m.isLive(a) {
		refuse(types.RefuseRawAccess, "")
	}
	al := m.at(a)
	if al.pending {
		m.materialize(a)
		al = m.at(a)
	}
	return al
}

// access checks an access of n bytes at off in allocation a, for reading or
// writing, and returns the allocation. The bound check cannot wrap whatever n
// a length word holds.
func (m *memory) access(a, off uint32, n uint64, write bool) *alloc {
	k := m.kindOf(a)
	if k == allocGlobal {
		refuse(types.RefuseGlobal, "")
	}
	al := m.get(a)
	if size := uint64(len(al.bytes)); n > size || uint64(off) > size-n {
		refuse(types.RefuseRawAccess, "")
	}
	if write && k == allocConst {
		refuse(types.RefuseRawAccess, "") // const copies are read-only
	}
	return al
}

// loadScalar reads an integer, float, bool, char or tag of width w (1, 2, 4
// or 8) at o: no pointer word may start in [o-7, o+w) and no fragment bit
// may be set in [o, o+w).
func (al *alloc) loadScalar(o uint32, w int) uint64 {
	if al.ptr != nil && anyBit(al.ptr, sub7(o), o+uint32(w)) {
		refuse(types.RefusePointerBytes, "")
	}
	if al.frag != nil && anyBit(al.frag, o, o+uint32(w)) {
		refuse(types.RefusePointerBytes, "")
	}
	return types.LoadUint(al.bytes[o:], w)
}

// loadPtr reads a pointer-typed word at o: a tag at o gives the pointer;
// eight zero bytes with no tag in [o-7, o+8) and no fragment in [o, o+8)
// give null; anything else is RefusePointerBytes.
func (al *alloc) loadPtr(o uint32) uint64 {
	if al.ptr != nil && bitAt(al.ptr, o) {
		return types.LoadUint(al.bytes[o:], 8)
	}
	if al.ptr != nil && anyBit(al.ptr, sub7(o), o+8) {
		refuse(types.RefusePointerBytes, "")
	}
	if al.frag != nil && anyBit(al.frag, o, o+8) {
		refuse(types.RefusePointerBytes, "")
	}
	if types.LoadUint(al.bytes[o:], 8) != 0 {
		refuse(types.RefusePointerBytes, "")
	}
	return 0
}

// clearRange prepares [o, o+n) for a store: every tag t in [o-7, o+n) is
// removed and the bytes of its word outside the range become fragments; then
// the fragment bits inside the range are cleared.
func (al *alloc) clearRange(o, n uint32) {
	if n == 0 {
		return
	}
	end := o + n
	if al.ptr != nil {
		lo := sub7(o)
		for t, ok := nextBit(al.ptr, lo, end); ok; t, ok = nextBit(al.ptr, t+1, end) {
			clearBit(al.ptr, t)
			if al.ptag != nil {
				delete(al.ptag, t)
			}
			for b := t; b < t+8; b++ {
				if b < o || b >= end {
					al.setFrag(b)
				}
			}
		}
	}
	if al.frag != nil {
		clearBits(al.frag, o, end)
	}
}

// storeScalar writes the low w bytes of x at o.
func (al *alloc) storeScalar(o uint32, w int, x uint64) {
	al.clearRange(o, uint32(w))
	types.StoreUint(al.bytes[o:], w, x)
}

// storePtr writes pointer word p at o; null stores eight zero bytes and no
// tag. Stores at o and o+4 leave only the later tag.
func (al *alloc) storePtr(o uint32, p uint64) {
	al.clearRange(o, 8)
	types.StoreUint(al.bytes[o:], 8, p)
	if p != 0 {
		al.setTag(o)
	}
}

// fill writes n copies of byte v at o: a store of n bytes.
func (al *alloc) fill(o, n uint32, v byte) {
	al.clearRange(o, n)
	b := al.bytes[o : o+n]
	if v == 0 {
		clear(b)
		return
	}
	for i := range b {
		b[i] = v
	}
}

func (al *alloc) setTag(o uint32) {
	if al.ptr == nil {
		al.ptr = make([]uint64, (len(al.bytes)+63)/64)
	}
	setBit(al.ptr, o)
}

// setPtag gives the pointer word at o the tag t (0, the owner, is no
// entry). The tagged mode's stores call it after storePtr.
func (al *alloc) setPtag(o, t uint32) {
	if t == 0 {
		if al.ptag != nil {
			delete(al.ptag, o)
		}
		return
	}
	if al.ptag == nil {
		al.ptag = make(map[uint32]uint32)
	}
	al.ptag[o] = t
}

func (al *alloc) setFrag(o uint32) {
	if al.frag == nil {
		al.frag = make([]uint64, (len(al.bytes)+63)/64)
	}
	setBit(al.frag, o)
}

// copyBytes copies n bytes from (sa, so) to (da, do) with their tags: the
// destination is cleared as a store; a source tag whose word lies inside the
// source range moves with it; a tag whose word crosses either end, and every
// source fragment, become fragments at the destination. A pointer cut by a
// copy is never a pointer at the destination. Overlapping
// ranges in one allocation are copied as memmove does.
func (m *memory) copyBytes(da, do, sa, so uint32, n uint64) {
	if n == 0 {
		return
	}
	src := m.access(sa, so, n, false)
	dst := m.access(da, do, n, true)
	src = m.at(sa) // access may have materialized; re-read both
	dst = m.at(da)
	nn := uint32(n)
	m.tagBuf, m.fragBuf, m.ptagBuf = m.tagBuf[:0], m.fragBuf[:0], m.ptagBuf[:0]
	if src.ptr != nil {
		end := so + nn
		for t, ok := nextBit(src.ptr, sub7(so), end); ok; t, ok = nextBit(src.ptr, t+1, end) {
			if t >= so && t+8 <= end {
				m.tagBuf = append(m.tagBuf, t-so)
				if m.tagged {
					m.ptagBuf = append(m.ptagBuf, src.ptag[t])
				}
				continue
			}
			for b := t; b < t+8; b++ {
				if b >= so && b < end {
					m.fragBuf = append(m.fragBuf, b-so)
				}
			}
		}
	}
	if src.frag != nil {
		end := so + nn
		for f, ok := nextBit(src.frag, so, end); ok; f, ok = nextBit(src.frag, f+1, end) {
			m.fragBuf = append(m.fragBuf, f-so)
		}
	}
	dst.clearRange(do, nn)
	copy(dst.bytes[do:do+nn], src.bytes[so:so+nn])
	for i, t := range m.tagBuf {
		dst.setTag(do + t)
		if m.tagged {
			dst.setPtag(do+t, m.ptagBuf[i])
		}
	}
	for _, f := range m.fragBuf {
		dst.setFrag(do + f)
	}
}

// sub7 is o-7 clamped at 0: the first offset whose pointer word reaches o.
func sub7(o uint32) uint32 {
	if o < 7 {
		return 0
	}
	return o - 7
}

func bitAt(bs []uint64, o uint32) bool { return bs[o>>6]&(1<<(o&63)) != 0 }

func setBit(bs []uint64, o uint32) { bs[o>>6] |= 1 << (o & 63) }

func clearBit(bs []uint64, o uint32) { bs[o>>6] &^= 1 << (o & 63) }

// rangeMask is the mask of bits [lo, hi) within word w (both in word w's
// 64-bit window, hi may be the window's end).
func wordMask(w, lo, hi uint32) uint64 {
	base := w << 6
	l, h := uint32(0), uint32(64)
	if lo > base {
		l = lo - base
	}
	if hi < base+64 {
		h = hi - base
	}
	if h <= l {
		return 0
	}
	if h-l == 64 {
		return ^uint64(0)
	}
	return ((1 << (h - l)) - 1) << l
}

// anyBit reports whether a bit in [lo, hi) is set; hi is clamped to the set.
func anyBit(bs []uint64, lo, hi uint32) bool {
	hi = min(hi, uint32(len(bs))<<6)
	for w := lo >> 6; lo < hi && w<<6 < hi; w++ {
		if bs[w]&wordMask(w, lo, hi) != 0 {
			return true
		}
	}
	return false
}

func clearBits(bs []uint64, lo, hi uint32) {
	hi = min(hi, uint32(len(bs))<<6)
	for w := lo >> 6; lo < hi && w<<6 < hi; w++ {
		bs[w] &^= wordMask(w, lo, hi)
	}
}

// nextBit is the first set bit in [lo, hi).
func nextBit(bs []uint64, lo, hi uint32) (uint32, bool) {
	hi = min(hi, uint32(len(bs))<<6)
	for w := lo >> 6; lo < hi && w<<6 < hi; w++ {
		if x := bs[w] & wordMask(w, lo, hi); x != 0 {
			return w<<6 + uint32(bits.TrailingZeros64(x)), true
		}
	}
	return 0, false
}

// bytePool reuses allocation slices across evaluations, by power-of-two size
// class; every slice handed out is zero.
type bytePool struct {
	classes [33]sync.Pool
}

func sizeClass(n int) int {
	if n <= 64 {
		return 6
	}
	return bits.Len(uint(n - 1))
}

// get returns a zeroed slice of length n.
func (p *bytePool) get(n int) []byte {
	if n == 0 {
		return nil
	}
	c := sizeClass(n)
	if c >= len(p.classes) {
		return make([]byte, n)
	}
	if v := p.classes[c].Get(); v != nil {
		b := (*v.(*[]byte))[:n]
		clear(b)
		return b
	}
	return make([]byte, n, 1<<c)
}

// put returns a slice for reuse.
func (p *bytePool) put(b []byte) {
	if cap(b) == 0 {
		return
	}
	c := sizeClass(cap(b))
	if c >= len(p.classes) || cap(b) != 1<<c {
		return
	}
	b = b[:cap(b)]
	p.classes[c].Put(&b)
}
