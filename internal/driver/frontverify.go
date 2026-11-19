// Why: a module cache hit skips stages 3 to 7, so a stale or wrong entry
// would go unnoticed; MK_CACHE_VERIFY=1 is the switch that proves the cache
// gives what a cold build would.
//
// What: the verify path of the module cache: a module whose candidate
// validates is recomputed instead of loaded, and its would-be store is
// compared with what the cache holds.
//
// How: the module's payloads are fetched, it runs stages 3 to 7 as on a
// miss, and store compares, byte for byte, the interface frame (function
// result keys and record) and every function result. A difference, or a
// recomputed module that would not be stored at all, is E0000 with where
// "cache verify" and what "iface <key>: ..."; the build goes on with the
// recomputed result, and the stale entries are removed so that the
// recomputed ones replace them (Put never replaces an entry whose frame
// verifies). The output therefore equals a cold build's, plus the E0000s.

package driver

import (
	"bytes"
	"fmt"
	"slices"

	"mk/internal/cache"
	"mk/internal/diag"
	"mk/internal/source"
)

// frontVerify is the stored side of one module's comparison.
type frontVerify struct {
	entry source.Hash
	keys  []source.Hash          // the result keys the frame names, in order
	rec   []byte                 // the interface record
	funcs map[source.Hash][]byte // the function results it names; nil value: missing
	where map[source.Hash]source.Hash
	bad   string // the payload could not be unframed
}

// fetchForVerify records the payloads of m's validated candidate entry.
// The frame is compared by its result keys and record, not by the bundles
// that hold the results: where a result lives depends on which earlier
// build computed it (funcbundle.go).
func (fc *frontCache) fetchForVerify(m *modNode, entry source.Hash) {
	raw, ok, _ := fc.st.Get(cache.KindIface, entry)
	if !ok {
		return // gone since the manifest named it: an ordinary miss
	}
	v := &frontVerify{entry: entry, funcs: map[source.Hash][]byte{}, where: map[source.Hash]source.Hash{}}
	refs, rec, err := unframeIface(raw)
	if err != nil {
		v.bad = err.Error()
	}
	v.rec = rec
	for _, r := range refs {
		v.keys = append(v.keys, r.key)
		v.where[r.key] = r.bundle
		if b, ok := fc.result(r); ok {
			v.funcs[r.key] = b
		} else {
			v.funcs[r.key] = nil
		}
	}
	m.verify = v
}

// mismatch reports one verify failure.
func (fc *frontCache) mismatch(entry source.Hash, what string) {
	d := diag.FromInternal(&source.InternalError{Where: "cache verify",
		What: fmt.Sprintf("iface %s: %s", entry, what)})
	fc.mu.Lock()
	fc.verifyDiags = append(fc.verifyDiags, d)
	fc.mu.Unlock()
}

// mismatchKind reports one verify failure of an entry that is not an
// interface (what "<kind> <key>: ...").
func (fc *frontCache) mismatchKind(kind string, key source.Hash, what string) {
	d := diag.FromInternal(&source.InternalError{Where: "cache verify",
		What: fmt.Sprintf("%s %s: %s", kind, key, what)})
	fc.mu.Lock()
	fc.verifyDiags = append(fc.verifyDiags, d)
	fc.mu.Unlock()
}

// verifyUnstored: the cache holds a validated entry for m, but the
// recomputed module would not be stored (why).
func (fc *frontCache) verifyUnstored(m *modNode, why string) {
	if v := m.verify; v != nil {
		fc.mismatch(v.entry, "the stored module validates, but the recomputed one is not storable ("+why+")")
		_ = fc.st.RemoveFile(cache.KindIface, v.entry)
	}
}

// verifyStored compares the recomputed interface key, frame and function
// results with the stored ones, and removes each stale entry so that the
// Puts that follow store the recomputed bytes.
func (fc *frontCache) verifyStored(m *modNode, ifKey source.Hash, keys []source.Hash, rec []byte, funcs map[source.Hash][]byte) {
	v := m.verify
	switch {
	case v.bad != "":
		fc.mismatch(v.entry, "the stored entry does not unframe: "+v.bad)
	case ifKey != v.entry:
		fc.mismatch(v.entry, fmt.Sprintf("the recomputed module's reads give interface %s", ifKey))
	case !slices.Equal(keys, v.keys) || !bytes.Equal(rec, v.rec):
		fc.mismatch(v.entry, "the stored interface differs from the recomputed one")
	default:
		for k, b := range funcs {
			if s, ok := v.funcs[k]; ok && s != nil && !bytes.Equal(s, b) {
				fc.mismatch(v.entry, fmt.Sprintf("function result %s differs from the recomputed one", k))
				fc.removeBundle(v.where[k])
			}
		}
		return
	}
	_ = fc.st.RemoveFile(cache.KindIface, v.entry)
	for k, s := range v.funcs {
		if b, ok := funcs[k]; s != nil && (!ok || !bytes.Equal(s, b)) {
			fc.removeBundle(v.where[k])
		}
	}
}

// removeBundle removes a stale bundle from the store and from this
// frontCache's reads, so the recomputed results are written in its place.
func (fc *frontCache) removeBundle(k source.Hash) {
	_ = fc.st.RemoveFile(cache.KindFunc, k)
	fc.mu.Lock()
	delete(fc.bundles, k)
	fc.mu.Unlock()
}

// takeVerify returns the mismatches once.
func (fc *frontCache) takeVerify() []*diag.Diagnostic {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	ds := fc.verifyDiags
	fc.verifyDiags = nil
	return ds
}
