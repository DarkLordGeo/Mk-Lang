// Why: the compiler's shared tables (files, symbols, types) are read from
// many goroutines far more often than they grow; reads must not take a lock.
//
// What: AppendList, an append-only list with Append, Get and Len.
//
// How: elements live in chunks that double in size and never move once
// allocated, so a published element stays at its address; the length is an
// atomic counter, and a mutex in the owning table serializes Append.

package source

import (
	"math/bits"
	"sync/atomic"
)

// chunkBaseBits sets the first chunk's length, 1<<chunkBaseBits. Chunk k holds
// 1<<(k+chunkBaseBits) elements, so the chunks together cover every uint32 index.
const chunkBaseBits = 10

// chunkCount is enough chunks for indices 0 .. 1<<32-1.
const chunkCount = 32 - chunkBaseBits + 1

// AppendList is an append-only list whose existing elements can be read without
// a lock while another goroutine appends. Chunks never move once allocated.
// Invariant: Append is called by one goroutine at a time (the table that
// holds the list holds a mutex); Get and Len may be called concurrently with
// Append and each other. The zero AppendList is empty and ready to use. It is
// the storage of every lock-free-read table of the shared data types
// (SourceMap, Interner, types.Table).
type AppendList[T any] struct {
	n      atomic.Uint32
	chunks [chunkCount]atomic.Pointer[[]T]
}

// locate maps an index to its chunk and the offset inside that chunk.
func locate(i uint32) (k int, off uint64) {
	p := uint64(i) + 1<<chunkBaseBits
	k = bits.Len64(p) - 1 - chunkBaseBits
	return k, p - 1<<(k+chunkBaseBits)
}

// Append stores v at the next index and returns that index. The element is
// written before the length is published, so a reader that sees the new length
// sees the element. The caller serializes appends.
func (c *AppendList[T]) Append(v T) uint32 {
	i := c.n.Load()
	if i == ^uint32(0) {
		ICEf(Span{}, "AppendList full")
	}
	k, off := locate(i)
	ch := c.chunks[k].Load()
	if ch == nil {
		s := make([]T, 1<<(k+chunkBaseBits))
		ch = &s
		c.chunks[k].Store(ch)
	}
	(*ch)[off] = v
	c.n.Store(i + 1)
	return i
}

// Get returns the element at i and whether i has been published.
func (c *AppendList[T]) Get(i uint32) (T, bool) {
	if i >= c.n.Load() {
		var zero T
		return zero, false
	}
	k, off := locate(i)
	return (*c.chunks[k].Load())[off], true
}

// Len is the number of published elements.
func (c *AppendList[T]) Len() uint32 { return c.n.Load() }
