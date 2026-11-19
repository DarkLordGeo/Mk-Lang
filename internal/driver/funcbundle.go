// Why: creating, closing and renaming a cache file is slow next to the work
// of checking one function. A cold build that stored two files per function
// (a function result and a function manifest) spent most of its time on
// them; one bundle per module keeps a clean dev build within its budget.
//
// What: function results stored as one cache file per stored module: the
// bundle (bundleKey, encodeBundle, putBundle), the funcRef that locates one
// result in it, and the module's function index (funcIndexKey).
//
// How:
//   - A bundle is one KindFunc entry holding every function result a store of
//     one module computed, keyed by its sorted result keys (bundleKey), so
//     equal contents give one entry. A result is located by a funcRef, its
//     bundle and its own key, FuncKey or funcEntryKey.
//   - A module's function index is one KindManifest entry keyed by the
//     context and the module path (funcIndexKey). It maps each function
//     prefix to its manifest (iface.Manifest, Entry the result key) and each
//     result key to its bundle. Each store rewrites it: the prefixes of the
//     module's current functions get the generation of this store, those of
//     the previous store are kept one generation more (an edit undone finds
//     its old candidate), older ones are dropped. A lost update (two
//     processes) is a later miss, never a wrong hit: every candidate still
//     validates its reads, and a result is checked against its key.

package driver

import (
	"slices"

	"mk/internal/cache"
	"mk/internal/iface"
	"mk/internal/source"
)

// funcRef locates one function result: the bundle entry it is in and its
// key there.
type funcRef struct{ bundle, key source.Hash }

// bundleItem is one result of a bundle.
type bundleItem struct {
	key   source.Hash
	bytes []byte
}

// bundleKey is the key of the bundle of items: their keys, sorted.
func bundleKey(items []bundleItem) source.Hash {
	keys := make([]source.Hash, len(items))
	for i, it := range items {
		keys[i] = it.key
	}
	slices.SortFunc(keys, func(a, b source.Hash) int { return a.Compare(b) })
	h := source.NewHasher("mk.driver.fbundle.v1")
	h.Uvarint(uint64(len(keys)))
	for _, k := range keys {
		h.Hash(k)
	}
	return h.Sum()
}

// encodeBundle is the KindFunc payload of a bundle: the items sorted by key.
func encodeBundle(items []bundleItem) []byte {
	s := slices.Clone(items)
	slices.SortFunc(s, func(a, b bundleItem) int { return a.key.Compare(b.key) })
	var e iface.Encoder
	e.Uvarint(uint64(len(s)))
	for _, it := range s {
		e.Hash(it.key)
		e.Bytes(it.bytes)
	}
	return e.Out()
}

func decodeBundle(b []byte) (map[source.Hash][]byte, error) {
	d := iface.NewDecoder(b)
	n := d.Count(33)
	out := make(map[source.Hash][]byte, n)
	for i := 0; i < n && d.Err == nil; i++ {
		k := d.Hash()
		out[k] = d.Bytes()
	}
	if err := d.Done(); err != nil {
		return nil, err
	}
	return out, nil
}

// bundle returns the decoded bundle key, read once per frontCache, or false
// when the store does not hold it (GC, or never written) or it is corrupt.
func (fc *frontCache) bundle(key source.Hash) (map[source.Hash][]byte, bool) {
	fc.mu.Lock()
	b, ok := fc.bundles[key]
	fc.mu.Unlock()
	if ok {
		return b, b != nil
	}
	raw, ok, _ := fc.st.Get(cache.KindFunc, key)
	if ok {
		if b, _ = decodeBundle(raw); b == nil {
			ok = false
		}
	}
	fc.mu.Lock()
	if fc.bundles == nil {
		fc.bundles = map[source.Hash]map[source.Hash][]byte{}
	}
	if ok {
		fc.bundles[key] = b
	} else {
		fc.bundles[key] = nil
	}
	fc.mu.Unlock()
	return b, ok
}

// result is the payload of the function result at r, or false.
func (fc *frontCache) result(r funcRef) ([]byte, bool) {
	b, ok := fc.bundle(r.bundle)
	if !ok {
		return nil, false
	}
	p, ok := b[r.key]
	return p, ok
}

// putBundle stores items as one bundle and returns its key. False when the
// store refused the write.
func (fc *frontCache) putBundle(items []bundleItem) (source.Hash, bool) {
	k := bundleKey(items)
	if fc.st.Put(cache.KindFunc, k, encodeBundle(items)) != nil {
		return k, false
	}
	// Not kept in memory (a cold build stores every module's results); a
	// read in this session goes to the store, and an absent mark from an
	// earlier read is dropped.
	fc.mu.Lock()
	delete(fc.bundles, k)
	fc.mu.Unlock()
	return k, true
}

// readBundle reads and decodes bundle key from the store without keeping
// it (a module hit's load: its results are decoded once, at once).
func (fc *frontCache) readBundle(key source.Hash) (map[source.Hash][]byte, bool) {
	raw, ok, _ := fc.st.Get(cache.KindFunc, key)
	if !ok {
		return nil, false
	}
	b, err := decodeBundle(raw)
	return b, err == nil
}

// funcIndex is one module's function index (the file comment).
type funcIndex struct {
	gen     uint64
	entries map[source.Hash]indexEntry  // by function prefix
	refs    map[source.Hash]source.Hash // result key -> bundle
}

// indexEntry is one prefix's manifest and the generation that last wrote
// or kept it as current.
type indexEntry struct {
	gen uint64
	man *iface.Manifest
}

const funcIndexVersion = 1

// funcIndexKey is the KindManifest key of module path's function index.
func (fc *frontCache) funcIndexKey(path string) source.Hash {
	h := source.NewHasher("mk.driver.funcindex.v1")
	h.Hash(fc.ctx)
	h.String(path)
	return h.Sum()
}

func encodeFuncIndex(x *funcIndex) []byte {
	var e iface.Encoder
	e.Uvarint(funcIndexVersion)
	e.Uvarint(x.gen)
	ps := make([]source.Hash, 0, len(x.entries))
	for p := range x.entries {
		ps = append(ps, p)
	}
	slices.SortFunc(ps, func(a, b source.Hash) int { return a.Compare(b) })
	e.Uvarint(uint64(len(ps)))
	for _, p := range ps {
		en := x.entries[p]
		e.Hash(p)
		e.Uvarint(en.gen)
		e.Bytes(iface.EncodeManifest(en.man))
	}
	ks := make([]source.Hash, 0, len(x.refs))
	for k := range x.refs {
		ks = append(ks, k)
	}
	slices.SortFunc(ks, func(a, b source.Hash) int { return a.Compare(b) })
	e.Uvarint(uint64(len(ks)))
	for _, k := range ks {
		e.Hash(k)
		e.Hash(x.refs[k])
	}
	return e.Out()
}

func decodeFuncIndex(b []byte) (*funcIndex, error) {
	d := iface.NewDecoder(b)
	if v := d.Uvarint(); d.Err == nil && v != funcIndexVersion {
		d.Failf("function index version %d", v)
	}
	x := &funcIndex{gen: d.Uvarint(), entries: map[source.Hash]indexEntry{}, refs: map[source.Hash]source.Hash{}}
	n := d.Count(34)
	for i := 0; i < n && d.Err == nil; i++ {
		p := d.Hash()
		g := d.Uvarint()
		mb := d.Bytes()
		if d.Err != nil {
			break
		}
		man, err := iface.DecodeManifest(mb)
		if err != nil {
			return nil, err
		}
		x.entries[p] = indexEntry{gen: g, man: man}
	}
	n = d.Count(64)
	for i := 0; i < n && d.Err == nil; i++ {
		k := d.Hash()
		x.refs[k] = d.Hash()
	}
	if err := d.Done(); err != nil {
		return nil, err
	}
	return x, nil
}

// readFuncIndex reads module path's function index from the store; an
// absent or corrupt one is empty.
func (fc *frontCache) readFuncIndex(path string) *funcIndex {
	if raw, ok, _ := fc.st.Get(cache.KindManifest, fc.funcIndexKey(path)); ok {
		if x, err := decodeFuncIndex(raw); err == nil {
			return x
		}
	}
	return &funcIndex{entries: map[source.Hash]indexEntry{}, refs: map[source.Hash]source.Hash{}}
}

// funcIndexOf is m's function index as the store held it when this build
// first asked (one read per module and build).
func (m *modNode) funcIndexOf(fc *frontCache) *funcIndex {
	m.fidxOnce.Do(func() { m.fidx = fc.readFuncIndex(m.path) })
	return m.fidx
}

// lookupCandidates is the manifest of prefix p in m's index, nil when none.
func (x *funcIndex) lookupCandidates(p source.Hash) *iface.Manifest {
	if en, ok := x.entries[p]; ok {
		return en.man
	}
	return nil
}

// newCandidate is one result computed by a store, to enter the index.
type newCandidate struct {
	prefix source.Hash
	cand   iface.Candidate // Entry: the result key
	bundle source.Hash
}

// updateFuncIndex rewrites m's function index after a store: current are
// the prefixes of the module's reusable functions this build (computed or
// reused), adds the candidates the store computed. The old index is read
// again, so a concurrent store's update is merged as far as it landed.
func (fc *frontCache) updateFuncIndex(m *modNode, current []source.Hash, adds []newCandidate) {
	old := fc.readFuncIndex(m.path)
	gen := old.gen + 1
	nx := &funcIndex{gen: gen, entries: map[source.Hash]indexEntry{}, refs: map[source.Hash]source.Hash{}}
	for _, p := range current {
		man := &iface.Manifest{}
		if en, ok := old.entries[p]; ok {
			man.Candidates = slices.Clone(en.man.Candidates)
		}
		nx.entries[p] = indexEntry{gen: gen, man: man}
	}
	for _, a := range adds {
		en, ok := nx.entries[a.prefix]
		if !ok {
			en = indexEntry{gen: gen, man: &iface.Manifest{}}
			nx.entries[a.prefix] = en
		}
		en.man.Add(a.cand)
		old.refs[a.cand.Entry] = a.bundle
	}
	// The previous store's current prefixes stay one generation.
	for p, en := range old.entries {
		if _, ok := nx.entries[p]; !ok && en.gen+1 >= gen {
			nx.entries[p] = en
		}
	}
	for _, en := range nx.entries {
		for _, c := range en.man.Candidates {
			if b, ok := old.refs[c.Entry]; ok {
				nx.refs[c.Entry] = b
			}
		}
	}
	_ = fc.st.Put(cache.KindManifest, fc.funcIndexKey(m.path), encodeFuncIndex(nx))
}
