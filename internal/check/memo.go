// Why: the facts, shape and layout of a closed type are asked for over and
// over, from many goroutines, and must be cheap to read.
//
// What: factsMemo, its factsSlot per TypeID and layoutEntry.
//
// How: chunked slots indexed by TypeID, each value published once through an
// atomic pointer (CompareAndSwap from nil) and read with one atomic load and
// no lock; the chunk directory grows copy-on-write under a mutex.

package check

import (
	"sync"
	"sync/atomic"

	"mk/internal/types"
)

const memoChunk = 1024

// factsSlot holds the memoized answers of one closed TypeID. Each pointer is
// published once with CompareAndSwap(nil, p) and never written again, so
// every write into the record happens before any Load that observes it.
type factsSlot struct {
	facts  atomic.Pointer[types.Facts]
	layout atomic.Pointer[layoutEntry]
}

// layoutEntry is a closed type's shape and layout.
type layoutEntry struct {
	shape  types.Shape
	layout types.Layout
}

type memoDir = []*[memoChunk]factsSlot

// factsMemo is the Checker's memo. The directory is replaced copy-on-grow
// under mu; readers load it atomically.
type factsMemo struct {
	mu  sync.Mutex
	dir atomic.Pointer[memoDir]
}

// slot returns the slot of id, growing the directory when needed.
func (m *factsMemo) slot(id types.TypeID) *factsSlot {
	k, i := int(id)/memoChunk, int(id)%memoChunk
	if d := m.dir.Load(); d != nil && k < len(*d) {
		return &(*d)[k][i]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var old memoDir
	if d := m.dir.Load(); d != nil {
		old = *d
	}
	if k < len(old) {
		return &old[k][i]
	}
	nd := make(memoDir, k+1)
	copy(nd, old)
	for j := len(old); j <= k; j++ {
		nd[j] = new([memoChunk]factsSlot)
	}
	m.dir.Store(&nd)
	return &nd[k][i]
}

// facts returns the memoized facts of id, or nil.
func (m *factsMemo) facts(id types.TypeID) *types.Facts { return m.slot(id).facts.Load() }

// putFacts publishes f for id and returns the record every reader sees: f,
// or an equal one published first by another goroutine.
func (m *factsMemo) putFacts(id types.TypeID, f types.Facts) *types.Facts {
	s := m.slot(id)
	p := &f
	if s.facts.CompareAndSwap(nil, p) {
		return p
	}
	return s.facts.Load()
}

// layoutOf returns the memoized layout of id, or nil.
func (m *factsMemo) layoutOf(id types.TypeID) *layoutEntry { return m.slot(id).layout.Load() }

// putLayout publishes e for id, as putFacts does.
func (m *factsMemo) putLayout(id types.TypeID, e layoutEntry) *layoutEntry {
	s := m.slot(id)
	p := &e
	if s.layout.CompareAndSwap(nil, p) {
		return p
	}
	return s.layout.Load()
}
