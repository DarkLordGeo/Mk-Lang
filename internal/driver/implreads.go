// Why: a reused function must be rechecked when an impl it relies on is
// added or removed, but no more often; impl reads therefore have to be
// validated per function instead of being covered by the function prefix.
//
// What: the impl sets behind ReadImpl and ReadNoImpl reads for per-function
// reuse (funccache.go): implIndex, indexImpls, moduleImpls, implSet and
// implCtor.
//
// How: check records an impl read as (interface key, TypeString of the
// queried type), which the driver cannot turn back into a type. So the part
// such a read keeps is not the found impl's hash but the impl set hash: the
// hashes of every impl of the interface, in the reader's module and its
// closure, that a lookup for that type could find (implSet). Either read
// holds exactly while that set is unchanged; the bounds a found generic impl
// needs are reads of their own (check's implOf records them). The set is a
// superset of what check probes (the interface's and the constructor's
// modules), so a change outside what check saw misses, never hits wrongly.
//
// Every lookup a reusable function unit makes records a read (globalShare,
// a var global's Share judgement, records none, but global units are never
// reused). The Send and Share facts that unsafe impls assert (kinds.go)
// are read through shapes: check's satisfies (a bound)
// and sendable (a task argument) record shapeReads of the types they
// judge, and iface's shape part covers each type's unsafe impls. check's
// Slice implementer lookup (sliceElemOf) records ReadImpl and ReadNoImpl.
// So the function prefix holds no impl hash (TestFuncReuseUnsafeImplSend,
// TestFuncReuseSliceImplReads).

package driver

import (
	"bytes"
	"slices"
	"strings"
	"sync"

	"mk/internal/source"
	"mk/internal/types"
)

// implIndex is the impl sets of one reader: per interface key, the impl
// hashes by the constructor their For type prints as, the impls whose For
// type has no printable constructor (a parameter, a reference, a slice),
// and all of them.
type implIndex struct {
	byCtor map[string]map[string][]source.Hash
	wild   map[string][]source.Hash
	all    map[string][]source.Hash
}

// implState is a frontWorld's impl index, built once on first use: the
// closure's part, shared by every world of one reader while its closure is
// done clean (closureImpls), and the reader's own part.
type implState struct {
	once    sync.Once
	mods    []*modNode // the reader and its closure
	idx     *implIndex // nil: an impl could not be hashed, or no semantics
	closure *implIndex
}

// implSet is the impl set hash of (interface key, the spelled target) for
// the reader; false when the reader's impls cannot be indexed.
func (fw *frontWorld) implSet(ifaceKey types.Key, target string) (source.Hash, bool) {
	st := fw.impls
	if st == nil {
		return source.Hash{}, false
	}
	st.once.Do(func() {
		st.closure = fw.w.closureImpls(st.mods)
		if st.closure != nil {
			st.idx = fw.w.indexImpls(st.mods[:1])
		}
	})
	if st.idx == nil {
		return source.Hash{}, false
	}
	k := ifaceKey.String()
	var set []source.Hash
	for _, idx := range []*implIndex{st.idx, st.closure} {
		if ctor, ok := targetCtor(target); ok {
			set = append(append(set, idx.byCtor[k][ctor]...), idx.wild[k]...)
		} else {
			set = append(set, idx.all[k]...)
		}
	}
	slices.SortFunc(set, func(a, b source.Hash) int { return bytes.Compare(a[:], b[:]) })
	h := source.NewHasher("mk.driver.implset.v1")
	h.Uvarint(uint64(len(set)))
	for _, x := range set {
		h.Hash(x)
	}
	return h.Sum(), true
}

// implPart is World.Part for ReadImpl and ReadNoImpl: the impl set hash,
// with "exists" true for ReadImpl and false for ReadNoImpl, so iface.Valid
// holds either read exactly while the set is unchanged. A set that cannot
// be computed fails both kinds.
func (fw *frontWorld) implPart(r types.Read) (source.Hash, bool) {
	h, ok := fw.implSet(r.Key, r.Name)
	if !ok {
		return source.Hash{}, r.Kind == types.ReadNoImpl
	}
	return h, r.Kind == types.ReadImpl
}

// indexImpls indexes the impls of mods; nil when a module has no table
// entry or an impl cannot be hashed. A module that is done clean (every
// module of a reader's closure) has its entries computed once per world
// (moduleImpls: per-unit lookups of a stored gen module rebuilt the
// closure's index, hashing every impl, once per unit); the reader's own,
// still in its check, are computed each time.
func (w *world) indexImpls(mods []*modNode) *implIndex {
	s := w.sem5
	if s == nil {
		return nil
	}
	idx := &implIndex{byCtor: map[string]map[string][]source.Hash{}, wild: map[string][]source.Hash{}, all: map[string][]source.Hash{}}
	ie := source.Guard("impl index", source.Span{}, func() {
		for _, m := range mods {
			if m.mod == nil {
				idx = nil
				return
			}
			for _, e := range w.moduleImpls(m) {
				idx.all[e.iface] = append(idx.all[e.iface], e.hash)
				if e.wild {
					idx.wild[e.iface] = append(idx.wild[e.iface], e.hash)
					continue
				}
				if idx.byCtor[e.iface] == nil {
					idx.byCtor[e.iface] = map[string][]source.Hash{}
				}
				idx.byCtor[e.iface][e.ctor] = append(idx.byCtor[e.iface][e.ctor], e.hash)
			}
		}
	})
	if ie != nil {
		return nil
	}
	return idx
}

// closureImpls is the index of mods[1:], the reader's closure, memoized per
// reader when every module of it is done clean (then none of their impls
// changes for the rest of the build).
func (w *world) closureImpls(mods []*modNode) *implIndex {
	if len(mods) == 0 {
		return nil
	}
	reader, rest := mods[0], mods[1:]
	done := w.front != nil
	for _, m := range rest {
		if m.state.Load() != modDoneClean {
			done = false
			break
		}
	}
	if done {
		if v, ok := w.front.closureIndex.Load(reader); ok {
			return v.(*implIndex)
		}
	}
	idx := w.indexImpls(rest)
	if done && idx != nil {
		w.front.closureIndex.Store(reader, idx)
	}
	return idx
}

// implEntry is one impl of a module as the index files it: its interface
// key, its For type's constructor (wild when it has none) and its hash.
type implEntry struct {
	iface, ctor string
	wild        bool
	hash        source.Hash
}

// moduleImpls are m's impl entries in item order; memoized per world once
// m is done clean, when its impls no longer change.
func (w *world) moduleImpls(m *modNode) []implEntry {
	done := m.state.Load() == modDoneClean && w.front != nil
	if done {
		if v, ok := w.front.implEntries.Load(m); ok {
			return v.([]implEntry)
		}
	}
	s := w.sem5
	var out []implEntry
	for _, id := range m.mod.Items {
		it := w.items.Get(id)
		d, ok := it.Decl.(*types.ImplDecl)
		if it.Kind != types.KindImpl || !ok {
			continue
		}
		k := ""
		if d.Bound.Item != types.NoItem {
			k = w.items.Get(d.Bound.Item).Key.String()
		}
		e := implEntry{iface: k, hash: s.hash.Impl(it)}
		e.ctor, ok = w.implCtor(d)
		e.wild = !ok
		out = append(out, e)
	}
	if done {
		w.front.implEntries.Store(m, out)
	}
	return out
}

// implCtor is the constructor an impl's For type prints as: a named type's
// path before its arguments, or a builtin scalar's name; false for any
// other For type (a parameter, a reference, a slice, a tuple).
func (w *world) implCtor(d *types.ImplDecl) (string, bool) {
	ty := w.tab.Get(d.For)
	switch ty.Kind {
	case types.KNamed, types.KInt, types.KIntPtr, types.KFloat, types.KBool, types.KChar, types.KStr:
	default:
		return "", false
	}
	return targetCtor(w.tab.TypeString(d.For, d.Generics))
}

// targetCtor is the constructor a TypeString spells: the text before its
// first "[", when that is a dotted name; false otherwise ("&T", "[]u8",
// "(int, str)").
func targetCtor(s string) (string, bool) {
	if i := strings.IndexByte(s, '['); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "", false
	}
	for _, r := range s {
		if !(r == '.' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return "", false
		}
	}
	return s, true
}
