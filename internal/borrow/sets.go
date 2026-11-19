// Why: the checker's states hold many sets of loans, roots and sites that
// differ in a few elements; comparing, joining and storing them must be
// cheap.
//
// What: setID and setStore, with intern, of, fromUnsorted, has, add, unite,
// minus, meet and intersects.
//
// How: sets are hash-consed, immutable and sorted, so equal sets share one
// ID and equality is an integer compare; the binary operations are
// memoized per pair of IDs.

package borrow

import (
	"slices"
)

// setID names an immutable, sorted, duplicate-free []uint32 of one body's
// store; 0 is the empty set. Equal sets have equal IDs, so set equality is ==.
// Loan sets, root sets and site sets share the store: a set is a sorted list
// of numbers.
type setID uint32

const emptySet setID = 0

type setPair struct{ a, b setID }

// setStore is the per-body set table. It is not safe for concurrent use; each
// Check owns one.
//
// The intern index is keyed by a 64-bit hash of the elements (a string key
// cost a byte copy, a string allocation and a string hash per
// lookup). Sets with the same hash are chained through next, newest first,
// and told apart by comparing elements, so a collision costs a compare and
// never merges two sets. IDs are still issued in first-intern order.
type setStore struct {
	elems [][]uint32
	index map[uint64]setID
	next  []setID // next[id]: the previous set interned under id's hash, or 0
	// hashes[id] is hashElems(elems[id]), unmasked.
	hashes []uint64
	union  map[setPair]setID
	diff   map[setPair]setID
	inter  map[setPair]setID
	one    map[uint32]setID
	buf    []uint32
	// hashMask is ANDed into every hash; ^0 except in the collision test,
	// which sets 0 so that every set shares one chain.
	hashMask uint64
	// fvs interns the per-field held-set vectors of split roots (fields.go);
	// fvs[0] is the whole mode's nil.
	fvs     [][]setID
	fvIndex map[string]fvID
	fvKey   []byte
}

func newSetStore() *setStore {
	return &setStore{
		elems:    [][]uint32{nil},
		index:    make(map[uint64]setID),
		next:     []setID{0},
		hashes:   []uint64{0},
		union:    make(map[setPair]setID),
		diff:     make(map[setPair]setID),
		inter:    make(map[setPair]setID),
		one:      make(map[uint32]setID),
		hashMask: ^uint64(0),
	}
}

// mixElem scrambles one element. A set's hash is the sum of its elements'
// mixes (hashElems), so the hash of a union or a difference follows from its
// operand's hash and the elements added or removed, with no pass over the
// result, and the per-element work has no dependency chain.
func mixElem(x uint32) uint64 {
	z := (uint64(x) + 0x9E3779B97F4A7C15) * 0xBF58476D1CE4E5B9
	return z ^ z>>31
}

// hashElems is the hash of a set: the sum of mixElem over its elements.
func hashElems(xs []uint32) uint64 {
	var h uint64
	for _, x := range xs {
		h += mixElem(x)
	}
	return h
}

// intern returns the ID of xs, which must be sorted and duplicate-free; xs is
// copied when it is new.
func (s *setStore) intern(xs []uint32) setID {
	return s.internHashed(xs, hashElems(xs))
}

// internHashed is intern with xs's hash already known (h == hashElems(xs)).
func (s *setStore) internHashed(xs []uint32, h uint64) setID {
	if len(xs) == 0 {
		return emptySet
	}
	key := h & s.hashMask
	head := s.index[key]
	for id := head; id != emptySet; id = s.next[id] {
		if s.hashes[id] == h && slices.Equal(s.elems[id], xs) {
			return id
		}
	}
	id := setID(len(s.elems))
	s.elems = append(s.elems, slices.Clone(xs))
	s.hashes = append(s.hashes, h)
	s.next = append(s.next, head)
	s.index[key] = id
	return id
}

// of is the set {x}.
func (s *setStore) of(x uint32) setID {
	if id, ok := s.one[x]; ok {
		return id
	}
	id := s.intern([]uint32{x})
	s.one[x] = id
	return id
}

// fromUnsorted interns xs after sorting and deduplicating it (xs is
// modified).
func (s *setStore) fromUnsorted(xs []uint32) setID {
	slices.Sort(xs)
	return s.intern(slices.Compact(xs))
}

// get returns the elements of a set; shared, do not modify.
func (s *setStore) get(id setID) []uint32 { return s.elems[id] }

// len is the number of elements.
func (s *setStore) len(id setID) int { return len(s.elems[id]) }

// has reports whether x is in a.
func (s *setStore) has(a setID, x uint32) bool {
	_, ok := slices.BinarySearch(s.elems[a], x)
	return ok
}

// add is a ∪ {x}.
func (s *setStore) add(a setID, x uint32) setID {
	if s.has(a, x) {
		return a
	}
	return s.unite(a, s.of(x))
}

// unite is a ∪ b, memoized.
func (s *setStore) unite(a, b setID) setID {
	if a == b || b == emptySet {
		return a
	}
	if a == emptySet {
		return b
	}
	if a > b {
		a, b = b, a
	}
	k := setPair{a, b}
	if r, ok := s.union[k]; ok {
		return r
	}
	xs, ys := s.elems[a], s.elems[b]
	// Merge the shorter set into the longer one; big is the longer's ID.
	big := a
	if len(ys) > len(xs) {
		xs, ys = ys, xs
		big = b
	}
	h := s.hashes[big]
	out := s.buf[:0]
	if len(ys)*8 < len(xs) {
		// Few elements into a long set: binary-search each one's place and
		// copy the runs between them.
		lo := 0
		for _, y := range ys {
			i, ok := slices.BinarySearch(xs[lo:], y)
			i += lo
			out = append(out, xs[lo:i]...)
			lo = i
			if !ok {
				out = append(out, y)
				h += mixElem(y)
			}
		}
		out = append(out, xs[lo:]...)
	} else {
		i, j := 0, 0
		for i < len(xs) && j < len(ys) {
			switch {
			case xs[i] < ys[j]:
				out = append(out, xs[i])
				i++
			case xs[i] > ys[j]:
				out = append(out, ys[j])
				h += mixElem(ys[j])
				j++
			default:
				out = append(out, xs[i])
				i++
				j++
			}
		}
		out = append(out, xs[i:]...)
		for _, y := range ys[j:] {
			out = append(out, y)
			h += mixElem(y)
		}
	}
	var r setID
	switch len(out) {
	case len(xs): // the shorter set is a subset: the union is the longer one
		r = big
	default:
		r = s.internHashed(out, h)
	}
	s.buf = out[:0]
	s.union[k] = r
	return r
}

// minus is a \ b, memoized.
func (s *setStore) minus(a, b setID) setID {
	if a == emptySet || b == emptySet {
		return a
	}
	if a == b {
		return emptySet
	}
	k := setPair{a, b}
	if r, ok := s.diff[k]; ok {
		return r
	}
	xs, ys := s.elems[a], s.elems[b]
	// Find the first element of a that b removes; when there is none, a \ b
	// is a itself and nothing is built or interned.
	first := s.firstShared(xs, ys)
	if first < 0 {
		s.diff[k] = a
		return a
	}
	h := s.hashes[a]
	out := append(s.buf[:0], xs[:first]...)
	if len(ys)*8 < len(xs) {
		// Few elements out of a long set: binary-search each and copy the
		// runs between them.
		lo := first
		for _, y := range ys {
			i, ok := slices.BinarySearch(xs[lo:], y)
			i += lo
			out = append(out, xs[lo:i]...)
			lo = i
			if ok {
				h -= mixElem(y)
				lo++
			}
		}
		out = append(out, xs[lo:]...)
	} else {
		j := 0
		for _, x := range xs[first:] {
			for j < len(ys) && ys[j] < x {
				j++
			}
			if j < len(ys) && ys[j] == x {
				h -= mixElem(x)
				continue
			}
			out = append(out, x)
		}
	}
	r := s.internHashed(out, h)
	s.buf = out[:0]
	s.diff[k] = r
	return r
}

// firstShared is the least index i with xs[i] in ys, or -1. When ys is much
// shorter than xs it binary-searches xs for each element of ys, so removing a
// few elements from a long set does not walk all of it.
func (s *setStore) firstShared(xs, ys []uint32) int {
	if len(ys)*8 < len(xs) {
		best := -1
		lo := 0
		for _, y := range ys {
			i, ok := slices.BinarySearch(xs[lo:], y)
			i += lo
			if ok {
				best = i
				break
			}
			lo = i
			if lo >= len(xs) {
				break
			}
		}
		return best
	}
	i, j := 0, 0
	for i < len(xs) && j < len(ys) {
		switch {
		case xs[i] < ys[j]:
			i++
		case xs[i] > ys[j]:
			j++
		default:
			return i
		}
	}
	return -1
}

// meet is a ∩ b, memoized.
func (s *setStore) meet(a, b setID) setID {
	if a == emptySet || b == emptySet {
		return emptySet
	}
	if a == b {
		return a
	}
	if a > b {
		a, b = b, a
	}
	k := setPair{a, b}
	if r, ok := s.inter[k]; ok {
		return r
	}
	xs, ys := s.elems[a], s.elems[b]
	out := s.buf[:0]
	i, j := 0, 0
	for i < len(xs) && j < len(ys) {
		switch {
		case xs[i] < ys[j]:
			i++
		case xs[i] > ys[j]:
			j++
		default:
			out = append(out, xs[i])
			i++
			j++
		}
	}
	r := s.intern(out)
	s.buf = out[:0]
	s.inter[k] = r
	return r
}

// intersects reports whether a ∩ b is non-empty, without interning.
func (s *setStore) intersects(a, b setID) bool {
	if a == emptySet || b == emptySet {
		return false
	}
	xs, ys := s.elems[a], s.elems[b]
	i, j := 0, 0
	for i < len(xs) && j < len(ys) {
		switch {
		case xs[i] < ys[j]:
			i++
		case xs[i] > ys[j]:
			j++
		default:
			return true
		}
	}
	return false
}
