// Why: the instance-limit walk (walk.go) visits every non-generic function
// of the package on every build, a large share of a one-function edit's
// rebuild time. A per-module cache lets a build skip the roots whose result
// cannot have changed.
//
// What: walkKey and the walk markers (walkMarker, beginWalk, walkedClean,
// markWalked, markWalkedReach), the walk context (walkCtx), walkDeps and the
// external-read records (noteExtReads, extReadsOf).
//
// How: a root's walk result (E0708: a chain deeper than 64, or more than
// 100,000 instances under one root) is a function of the bodies it reaches,
// which all lie in its module or the module's dependency closure, of the
// target and of the profile (release drops contracts). A clean walk marks
// each module whose roots it walked; a later build skips the roots of a
// module whose marker still holds. A walk that reports anything marks
// nothing, so a failing build walks every root again. The walk of the
// remaining roots is the same mono.Collect: E0708 is judged per root, so
// the subset's diagnostics are those roots'.
//
// The marker is keyed on what the walk reads, not on every closure module's
// interface entry (which would rewalk every importer on any body edit): the
// module's own interface entry (its tokens and external reads), each closure
// module's API and shape hashes, and, as the marker's reads, the token
// hashes of every closure body the walk asked for and of each such module's
// other items that are not functions (the declarations a body's MIR depends
// on besides the signatures in the API: private types, consts, impls), and
// each such module's walk context (walkCtx: its names and root-ness,
// imports, own roots' declarations, const and global values). A body the
// walk never reached may change without a rewalk, and so may the body of a
// limit root of the walked build; a body it reached, a declaration beside
// it, or a value computed from any body, may not.

package driver

import (
	"maps"
	"slices"
	"strings"
	"sync"

	"mk/internal/cache"
	"mk/internal/iface"
	"mk/internal/source"
	"mk/internal/types"
)

// walkReach is the set of items whose bodies a walk asked for.
type walkReach struct {
	mu  sync.Mutex
	ids map[types.ItemID]struct{}
}

func (r *walkReach) add(id types.ItemID) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.ids == nil {
		r.ids = map[types.ItemID]struct{}{}
	}
	r.ids[id] = struct{}{}
	r.mu.Unlock()
}

// walkKey is m's walk key, or false when m has no interface entry this
// build or a module of its closure has no part hashes.
func (fc *frontCache) walkKey(m *modNode) (source.Hash, bool) {
	if !m.ifKeyOK {
		return source.Hash{}, false
	}
	h := source.NewHasher("mk.driver.walk.v2")
	h.Hash(fc.ctx)
	h.String(m.path)
	h.Hash(m.ifKey)
	for _, d := range closure(m) {
		if !d.partsOK {
			return source.Hash{}, false
		}
		h.String(d.path)
		h.Hash(d.api)
		h.Hash(d.shape)
	}
	return h.Sum(), true
}

// walkMarker starts the payload of a walk marker entry (cache.KindIface,
// content-addressed by the walk key); the marker's reads follow.
var walkMarker = []byte("mk walk clean v3")

// walkReads is one closure module's part of a marker: the reach entry
// holding the anchor keys of the module's items the walk depends on, and
// the digest of their token hashes when the marker was written.
type walkReads struct {
	path   string
	reach  source.Hash
	digest source.Hash
	// ext is the hash of the module's external reads (extReadsOf): a
	// reached body's MIR may hold what the module read of others (a
	// private const's value computed from another module's body).
	ext source.Hash
	// ctx is the module's walk context (walkCtx): what its reached bodies
	// were checked against besides their own tokens.
	ctx source.Hash
}

// walkState is what one world's walk markers read beside the store: the
// modules of the build being walked (own, whose limit roots are walked
// as roots, so an importer's walk never enters their bodies), the Hasher
// of the walk contexts, and each module's context, memoized.
type walkState struct {
	mu   sync.Mutex
	w    *world
	own  map[*modNode]bool
	hash *iface.Hasher
	ctxs map[*modNode]walkCtxMemo
	// trust (tests only, TestGen_VerifyChecksWalkMarkers): a marker
	// holds whatever its reads, so MK_CACHE_VERIFY's walk check has a
	// stale marker to find.
	trust bool
}

type walkCtxMemo struct {
	h  source.Hash
	ok bool
}

// testTrustWalkMarkers makes every walk marker hold (tests only).
var testTrustWalkMarkers bool

// beginWalk records the build whose roots w walks.
func (fc *frontCache) beginWalk(w *world, b *build) {
	ws := &fc.walk
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.w = w
	ws.trust = testTrustWalkMarkers
	if ws.own == nil {
		ws.own = map[*modNode]bool{}
	}
	for _, m := range b.mods {
		ws.own[m] = true
	}
}

// walkCtx is d's walk context: what a reached body of d was checked and
// lowered against besides its own tokens, so that a change of it rewalks
// every importer that reached d:
//
//   - the keys of d's items, each with whether it is a limit root: a new
//     private item hides a name a reached body resolved elsewhere (the
//     prelude's Swap), and a root that becomes generic is no longer walked
//     as a root, so its body counts under its callers' roots;
//   - d's resolved imports and each file's imports hash: an import alias
//     repointed to another module of the closure;
//   - for a module of the walked build, the declaration hash of each limit
//     root (its signature: the roots' bodies are the one thing the marker
//     does not read, since they are walked as roots of their own): a
//     private root's new return type changes what a reached generic body
//     instantiates;
//   - the value hash of each const and global with a value: a value
//     computed at compile time from a body of d that the walk never
//     reached (a generic evaluated by the const's initializer) is in no
//     token hash the marker reads.
//
// Everything else a reached body depends on is in the marker already: its
// own tokens, the tokens of d's other items that are not functions, d's
// external reads, and the closure's API and shape in the walk key. False
// when d has no items in the session or a file has no parse summary.
func (fc *frontCache) walkCtx(d *modNode) (source.Hash, bool) {
	ws := &fc.walk
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if v, ok := ws.ctxs[d]; ok {
		return v.h, v.ok
	}
	h, ok := fc.walkCtxOf(d)
	if ws.ctxs == nil {
		ws.ctxs = map[*modNode]walkCtxMemo{}
	}
	ws.ctxs[d] = walkCtxMemo{h, ok}
	return h, ok
}

// walkCtxOf computes walkCtx, under fc.walk.mu.
func (fc *frontCache) walkCtxOf(d *modNode) (source.Hash, bool) {
	ws := &fc.walk
	if d.mod == nil || ws.w == nil {
		return source.Hash{}, false
	}
	w := ws.w
	if ws.hash == nil {
		if w.sem5 == nil {
			return source.Hash{}, false
		}
		ws.hash = iface.NewHasher(w.tab, w.tab.Consts(), w.sem5.hashBody)
	}
	own := ws.own[d]
	type ent struct {
		key string
		h   source.Hash
	}
	ents := make([]ent, 0, len(d.mod.Items))
	ok := true
	if ie := source.Guard("walk context "+d.path, source.Span{}, func() {
		for _, id := range d.mod.Items {
			it := w.items.Get(id)
			e := ent{key: it.Key.String()}
			if isLimitRoot(it) {
				e.key += "\x00root"
				if own {
					e.h = ws.hash.Decl(it)
				}
			}
			switch dc := it.Decl.(type) {
			case *types.ConstDecl:
				if dc.HasValue {
					e.h = ws.hash.Const(dc.Value)
				}
			case *types.GlobalDecl:
				if dc.HasValue {
					e.h = ws.hash.Const(dc.Value)
				}
			}
			ents = append(ents, e)
		}
	}); ie != nil {
		ok = false
	}
	if !ok {
		return source.Hash{}, false
	}
	slices.SortFunc(ents, func(a, b ent) int { return strings.Compare(a.key, b.key) })
	h := source.NewHasher("mk.driver.walkctx.v1")
	h.Bool(own)
	h.Uvarint(uint64(len(ents)))
	for _, e := range ents {
		h.String(e.key)
		h.Hash(e.h)
	}
	h.Hash(resolvedImports(d))
	for _, f := range d.files {
		if f.src == nil || f.sum == nil {
			return source.Hash{}, false
		}
		h.String(f.src.Path)
		h.Hash(f.sum.ImportsHash)
	}
	return h.Sum(), true
}

// walkedClean reports whether the store marks m's roots walked clean and
// every token hash the marker read is unchanged.
func (fc *frontCache) walkedClean(m *modNode) bool {
	k, ok := fc.walkKey(m)
	if !ok {
		return false
	}
	b, ok, _ := fc.st.Get(cache.KindIface, k)
	if !ok || len(b) < len(walkMarker) || string(b[:len(walkMarker)]) != string(walkMarker) {
		return false
	}
	d := iface.NewDecoder(b[len(walkMarker):])
	n := d.Count(3)
	reads := make([]walkReads, 0, n)
	for i := 0; i < n && d.Err == nil; i++ {
		reads = append(reads, walkReads{path: d.String(), reach: d.Hash(), digest: d.Hash(), ext: d.Hash(), ctx: d.Hash()})
	}
	if d.Done() != nil {
		return false
	}
	if fc.walk.trust {
		return true
	}
	byPath := map[string]*modNode{}
	for _, d := range closure(m) {
		byPath[d.path] = d
	}
	for _, r := range reads {
		d := byPath[r.path]
		if d == nil {
			return false
		}
		if ext, ok := fc.extReadsOf(d); !ok || ext != r.ext {
			return false
		}
		if c, ok := fc.walkCtx(d); !ok || c != r.ctx {
			return false
		}
		keys, ok := fc.reachKeys(r.reach)
		if !ok {
			return false
		}
		got, ok := fc.reachDigest(keys)
		if !ok || got != r.digest {
			return false
		}
	}
	return true
}

// reachKeys loads a reach entry's anchor keys, once per frontCache.
func (fc *frontCache) reachKeys(k source.Hash) ([]iface.AnchorKey, bool) {
	if v, ok := fc.reaches.Load(k); ok {
		return v.([]iface.AnchorKey), true
	}
	b, ok, _ := fc.st.Get(cache.KindIface, k)
	if !ok {
		return nil, false
	}
	d := iface.NewDecoder(b)
	n := d.Count(1)
	keys := make([]iface.AnchorKey, 0, n)
	for i := 0; i < n && d.Err == nil; i++ {
		keys = append(keys, iface.AnchorKey(d.String()))
	}
	if d.Done() != nil {
		return nil, false
	}
	fc.reaches.Store(k, keys)
	return keys, true
}

// reachDigest is the digest of the current token hashes of keys; false
// when one has no anchor.
func (fc *frontCache) reachDigest(keys []iface.AnchorKey) (source.Hash, bool) {
	h := source.NewHasher("mk.driver.walkreads.v1")
	for _, k := range keys {
		a, ok := fc.anchors.Anchor(k)
		if !ok {
			return source.Hash{}, false
		}
		h.String(string(k))
		h.Hash(a.TokenHash)
	}
	return h.Sum(), true
}

// markWalked marks modules walked with nothing reached outside them.
func (fc *frontCache) markWalked(mods []*modNode) {
	fc.markWalkedReach(nil, mods, nil)
}

// markWalkedReach marks the modules whose roots a clean walk covered, each
// with the reads of its closure the walk depends on. A module whose reads
// cannot be anchored is not marked.
func (fc *frontCache) markWalkedReach(w *world, mods []*modNode, reach *walkReach) {
	if fc == nil {
		return
	}
	fc.walk.mu.Lock()
	ownSet := maps.Clone(fc.walk.own)
	fc.walk.mu.Unlock()
	byMod := map[types.ModuleID][]types.ItemID{}
	if reach != nil && w != nil {
		reach.mu.Lock()
		for id := range reach.ids {
			mid := w.items.Get(id).Module
			byMod[mid] = append(byMod[mid], id)
		}
		reach.mu.Unlock()
	}
	// The reach entry of each module the walk entered, written once.
	type part struct {
		r  walkReads
		ok bool
	}
	parts := map[*modNode]part{}
	partOf := func(d *modNode) part {
		if p, ok := parts[d]; ok {
			return p
		}
		p := part{ok: true}
		if ids := byMod[d.id]; len(ids) > 0 {
			keys, ok := fc.walkDeps(w, d, ids, ownSet[d])
			if ok {
				var e iface.Encoder
				e.Uvarint(uint64(len(keys)))
				for _, k := range keys {
					e.String(string(k))
				}
				enc := e.Out()
				rk := source.HashBytes("mk.driver.walkreach.v1", enc)
				dg, ok2 := fc.reachDigest(keys)
				ext, ok3 := fc.extReadsOf(d)
				cx, ok4 := fc.walkCtx(d)
				ok = ok2 && ok3 && ok4 && fc.st.Put(cache.KindIface, rk, enc) == nil
				p.r = walkReads{path: d.path, reach: rk, digest: dg, ext: ext, ctx: cx}
			}
			p.ok = ok
		}
		parts[d] = p
		return p
	}
	for _, m := range mods {
		k, ok := fc.walkKey(m)
		if !ok {
			continue
		}
		var reads []walkReads
		marked := true
		for _, d := range closure(m) {
			p := partOf(d)
			if !p.ok {
				marked = false
				break
			}
			if p.r.path != "" {
				reads = append(reads, p.r)
			}
		}
		if !marked {
			continue
		}
		var e iface.Encoder
		e.Uvarint(uint64(len(reads)))
		for _, r := range reads {
			e.String(r.path)
			e.Hash(r.reach)
			e.Hash(r.digest)
			e.Hash(r.ext)
			e.Hash(r.ctx)
		}
		_ = fc.st.Put(cache.KindIface, k, append(slices.Clone(walkMarker), e.Out()...))
	}
}

// walkDeps are the anchor keys of d's items a walk that reached ids
// depends on, for an importer's marker: each reached item's anchor (a
// lifted literal's is its enclosing item's) except the limit roots of d
// when d is a module of the walked build (own), and the anchor of each
// item of d that is not a function, sorted; false when one has no anchor.
// An own root is a root of the walk: an importer's walk that calls it
// meets its root node and never counts its body, and the root is walked
// whenever d's own text changes (d's interface entry is in d's key), so
// the importer's marker reads only its declaration (walkCtx). A function
// of a dependency package is never a root: an importer's walk enters its
// body, which is read (otherwise a body edit in a path dependency would
// keep a marker that no longer holds).
func (fc *frontCache) walkDeps(w *world, d *modNode, ids []types.ItemID, own bool) ([]iface.AnchorKey, bool) {
	roots := map[types.ItemID]bool{}
	if own && d.mod != nil {
		for _, id := range d.mod.Items {
			if isLimitRoot(w.items.Get(id)) {
				roots[id] = true
			}
		}
	}
	seen := map[iface.AnchorKey]bool{}
	add := func(id types.ItemID) bool {
		it := w.items.Get(id)
		if it.Span.File == source.NoFile {
			return false
		}
		k, ok := fc.anchors.Locate(it.Span.File, it.Span.Start)
		if !ok {
			return false
		}
		seen[k] = true
		return true
	}
	for _, id := range ids {
		if !roots[id] && !add(id) {
			return nil, false
		}
	}
	if d.mod != nil {
		for _, id := range d.mod.Items {
			it := w.items.Get(id)
			if it.Kind == types.KindFunc || it.Kind == types.KindTest || it.Span.File == source.NoFile || isTargetKey(it.Key) {
				continue
			}
			if !add(id) {
				return nil, false
			}
		}
	}
	keys := make([]iface.AnchorKey, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b iface.AnchorKey) int { return strings.Compare(string(a), string(b)) })
	return keys, true
}

// noteExtReads records m's external reads (a hit's candidate's, or the
// stored module's) for the walk markers of its importers.
func (fc *frontCache) noteExtReads(m *modNode, reads []types.Read) {
	if fc == nil {
		return
	}
	fc.extReads.Store(m, source.HashBytes("mk.driver.walkext.v1", iface.EncodeReads(reads)))
}

// extReadsOf is m's external reads hash; false when m was neither a hit
// nor stored this build.
func (fc *frontCache) extReadsOf(m *modNode) (source.Hash, bool) {
	v, ok := fc.extReads.Load(m)
	if !ok {
		return source.Hash{}, false
	}
	return v.(source.Hash), true
}
