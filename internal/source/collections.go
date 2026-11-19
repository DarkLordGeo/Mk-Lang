// Why: the compiler's output must be deterministic, and Go's map iteration
// order is random; no map iteration may reach output.
//
// What: IndexMap and IndexSet, insertion-ordered collections, the only maps
// whose iteration may reach output.
//
// How: a Go map from key to index beside parallel key and value slices;
// iteration walks the slices, so it follows insertion order.

package source

import "iter"

// IndexMap is a map whose iteration order is the order of first insertion.
// Insert on a present key replaces the value in place, keeping its position.
// There is no removal: positions are identities, and removal would renumber
// them; build a new map instead. The inner Go map is never iterated, so its
// randomized order cannot reach output. The zero IndexMap is empty and ready to
// use. An IndexMap is not safe for concurrent writes.
type IndexMap[K comparable, V any] struct {
	keys  []K
	vals  []V
	index map[K]int
}

// Len is the number of keys.
func (m *IndexMap[K, V]) Len() int { return len(m.keys) }

// Insert sets k to v. When k was present it returns the old value and true, and
// k keeps its position; otherwise k is appended at position Len()-1.
func (m *IndexMap[K, V]) Insert(k K, v V) (old V, had bool) {
	if i, ok := m.index[k]; ok {
		old, m.vals[i] = m.vals[i], v
		return old, true
	}
	if m.index == nil {
		m.index = make(map[K]int)
	}
	m.index[k] = len(m.keys)
	m.keys = append(m.keys, k)
	m.vals = append(m.vals, v)
	return old, false
}

// Entry returns a pointer to k's value, appending k with the zero value first
// when it is absent; inserted reports whether it was. The pointer is valid until
// the next insertion of a new key.
func (m *IndexMap[K, V]) Entry(k K) (v *V, inserted bool) {
	if i, ok := m.index[k]; ok {
		return &m.vals[i], false
	}
	var zero V
	m.Insert(k, zero)
	return &m.vals[len(m.vals)-1], true
}

// Get returns k's value and whether k is present.
func (m *IndexMap[K, V]) Get(k K) (V, bool) {
	if i, ok := m.index[k]; ok {
		return m.vals[i], true
	}
	var zero V
	return zero, false
}

// Ptr returns a pointer to k's value, or nil when k is absent. The pointer is
// valid until the next insertion of a new key, which may move the values.
func (m *IndexMap[K, V]) Ptr(k K) *V {
	if i, ok := m.index[k]; ok {
		return &m.vals[i]
	}
	return nil
}

// Has reports whether k is present.
func (m *IndexMap[K, V]) Has(k K) bool {
	_, ok := m.index[k]
	return ok
}

// IndexOf returns k's position (0-based, in insertion order) and whether k is present.
func (m *IndexMap[K, V]) IndexOf(k K) (int, bool) {
	i, ok := m.index[k]
	return i, ok
}

// At returns the key and value at position i; i out of [0, Len()) panics as a
// slice index does.
func (m *IndexMap[K, V]) At(i int) (K, V) { return m.keys[i], m.vals[i] }

// KeyAt returns the key at position i.
func (m *IndexMap[K, V]) KeyAt(i int) K { return m.keys[i] }

// ValueAt returns the value at position i.
func (m *IndexMap[K, V]) ValueAt(i int) V { return m.vals[i] }

// All yields every key and value in insertion order. Inserting during the
// iteration is not allowed.
func (m *IndexMap[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for i, k := range m.keys {
			if !yield(k, m.vals[i]) {
				return
			}
		}
	}
}

// Keys yields every key in insertion order.
func (m *IndexMap[K, V]) Keys() iter.Seq[K] {
	return func(yield func(K) bool) {
		for _, k := range m.keys {
			if !yield(k) {
				return
			}
		}
	}
}

// Values yields every value in insertion order.
func (m *IndexMap[K, V]) Values() iter.Seq[V] {
	return func(yield func(V) bool) {
		for _, v := range m.vals {
			if !yield(v) {
				return
			}
		}
	}
}

// Clear removes every key, keeping allocated capacity.
func (m *IndexMap[K, V]) Clear() {
	clear(m.keys)
	clear(m.vals)
	m.keys, m.vals = m.keys[:0], m.vals[:0]
	clear(m.index)
}

// Clone returns an independent copy with the same order (values are copied
// shallowly).
func (m *IndexMap[K, V]) Clone() IndexMap[K, V] {
	c := IndexMap[K, V]{
		keys:  append([]K(nil), m.keys...),
		vals:  append([]V(nil), m.vals...),
		index: make(map[K]int, len(m.keys)),
	}
	for i, k := range m.keys {
		c.index[k] = i
	}
	return c
}

// IndexSet is a set whose iteration order is the order of first insertion, with
// no removal, built on IndexMap. The zero IndexSet is empty and ready to use.
type IndexSet[K comparable] struct {
	m IndexMap[K, struct{}]
}

// Len is the number of elements.
func (s *IndexSet[K]) Len() int { return s.m.Len() }

// Insert adds k and reports whether it was new; a present k keeps its position.
func (s *IndexSet[K]) Insert(k K) bool {
	_, had := s.m.Insert(k, struct{}{})
	return !had
}

// Has reports whether k is present.
func (s *IndexSet[K]) Has(k K) bool { return s.m.Has(k) }

// IndexOf returns k's position in insertion order and whether k is present.
func (s *IndexSet[K]) IndexOf(k K) (int, bool) { return s.m.IndexOf(k) }

// At returns the element at position i.
func (s *IndexSet[K]) At(i int) K { return s.m.keys[i] }

// All yields every element in insertion order.
func (s *IndexSet[K]) All() iter.Seq[K] { return s.m.Keys() }

// Slice returns a new slice of the elements in insertion order.
func (s *IndexSet[K]) Slice() []K { return append([]K(nil), s.m.keys...) }

// Clear removes every element, keeping allocated capacity.
func (s *IndexSet[K]) Clear() { s.m.Clear() }

// Clone returns an independent copy with the same order.
func (s *IndexSet[K]) Clone() IndexSet[K] { return IndexSet[K]{m: s.m.Clone()} }
