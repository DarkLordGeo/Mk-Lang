// Why: E0708 (an instance chain too deep, or too many instances under one
// root) is found by mono's collection, which only a build runs. mk check
// must report it too, so that check and build accept the same programs.
//
// What: limitRoots, world.walk (the instance-limit walk), verifyWalk (the
// cache's self-check), walkEnv and the leaf sets lostLiterals and
// lostContracts.
//
// How: after every module is done, mono.Collect runs over the limit roots
// with an Env whose Body is nil for every body that failed, so a failed body
// is a leaf, never an ICE of the walk. The instances are dropped; a build
// collects again over its own roots.

package driver

import (
	"fmt"
	"slices"

	"mk/internal/cache"
	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/mono"
	"mk/internal/source"
	"mk/internal/types"
)

// limitRoots are the walk's roots in every command: every non-generic function
// and method and every test block of the package's own modules (b, the build
// being checked: the user package, a corpus unit, or std when std is the
// package), plus every export extern, which is a non-generic function too.
// Generic items, functions with a const value parameter and bodiless
// declarations (externs, intrinsics) are never roots; an item stage 6 never
// stored has no MIR to walk. Sorted by key, so the list depends on the source
// tree only.
func (w *world) limitRoots(b *build) []mono.Root {
	roots, _ := w.limitRootsOf(b, nil)
	return roots
}

// limitRootsOf is limitRoots without the modules skip says are walked
// clean already (walkcache.go), and the modules whose roots it lists.
func (w *world) limitRootsOf(b *build, skip func(*modNode) bool) ([]mono.Root, []*modNode) {
	var walked []*modNode
	for _, m := range b.mods {
		if m.mod == nil || skip != nil && skip(m) {
			continue
		}
		walked = append(walked, m)
	}
	w.front.prefetchWalked(walked)
	var ids []types.ItemID
	for _, m := range walked {
		for _, id := range m.mod.Items {
			if !isLimitRoot(w.items.Get(id)) {
				continue
			}
			if w.sem5.body(id) != nil {
				ids = append(ids, id)
			}
		}
	}
	slices.SortFunc(ids, func(a, b types.ItemID) int { return cmpKey(w.items.Get(a).Key, w.items.Get(b).Key) })
	roots := make([]mono.Root, len(ids))
	for i, id := range ids {
		k := mono.RootExport
		if w.items.Get(id).Kind == types.KindTest {
			k = mono.RootTest
		}
		roots[i] = mono.Root{Kind: k, Item: id}
	}
	return roots, walked
}

// hasConstParam reports whether sig has a const value parameter. Such a
// function has no instance without its const arguments: it is reached only
// through the callers that pass them, so it is never a root (mono skips one
// too).
func hasConstParam(sig *types.Signature) bool {
	for _, p := range sig.Params {
		if p.Const {
			return true
		}
	}
	return false
}

// walk is the instance-limit walk over b's limit roots: mono.Collect with
// the session's Env, whose Body is nil for a function without clean MIR
// (failed check, lower, Validate or stage 7, or poisoned), so a failed
// body is a leaf and never an ICE of the walk. It is skipped when
// ds holds a diagnostic of a phase before types: E0708 is a types
// diagnostic, so diag.Order would drop it and skipping changes no byte.
// Its results are E0708 (mono's) or one E0000; the instances are dropped (a
// build collects again over its build roots).
func (w *world) walk(b *build, ds []*diag.Diagnostic) []*diag.Diagnostic {
	if w.sem5 == nil || earlierThanTypes(ds) {
		return nil
	}
	// The roots of a module walked clean under the same interfaces are
	// skipped (walkcache.go); a clean walk marks the modules it covered.
	fc := w.front
	var skip func(*modNode) bool
	if fc != nil && !w.noFlow {
		fc.beginWalk(w, b)
		skip = fc.walkedClean
	}
	// MK_CACHE_VERIFY walks every root and notes the modules whose marker
	// holds; the walk without their roots must report what the whole walk
	// does (verifyWalk).
	var claimed []*modNode
	if skip != nil && fc.verify {
		holds := skip
		skip = func(m *modNode) bool {
			if holds(m) {
				claimed = append(claimed, m)
			}
			return false
		}
	}
	roots, walked := w.limitRootsOf(b, skip)
	for _, m := range walked {
		b.trace.ModulesWalked = append(b.trace.ModulesWalked, m.path)
	}
	if len(roots) == 0 {
		if fc != nil && skip != nil {
			fc.markWalked(walked)
		}
		return nil
	}
	lost := w.lostLiterals(b)
	reach := &walkReach{}
	out, ie := w.collectWalk(b, lost, reach, roots)
	if ie != nil {
		return []*diag.Diagnostic{diag.FromInternal(ie)}
	}
	if len(claimed) > 0 {
		w.verifyWalk(b, lost, roots, claimed, out)
	}
	if len(out) == 0 && skip != nil && !earlierThanTypes(ds) && !hasErrors(ds) {
		fc.markWalkedReach(w, walked, reach)
	}
	return out
}

// collectWalk is the walk's mono.Collect over roots.
func (w *world) collectWalk(b *build, lost map[types.ItemID]bool, reach *walkReach, roots []mono.Root) ([]*diag.Diagnostic, *source.InternalError) {
	env := walkEnv{monoEnv: w.sem5.monoEnv(w), noFlow: w.noFlow, lost: lost, noContract: lostPredicateOwners(w.items, lost, w.lostContracts(b)), reach: reach}
	var out []*diag.Diagnostic
	ie := source.Guard("instance-limit walk", source.Span{}, func() {
		// The walk drops its instances, so it skips their content hashes
		// no verdict reads one.
		opt := w.monoOptions()
		opt.SkipContent = true
		_, out = mono.Collect(env, roots, opt)
	})
	return out, ie
}

// verifyWalk is MK_CACHE_VERIFY's check of the walk markers: the walk without
// the roots of the claimed modules, whose markers hold, must report what the
// whole walk reported (all). Otherwise each claimed marker is an E0000 "cache
// verify" and is removed, so the clean walk that follows writes it again; the
// build reports the whole walk's diagnostics, as a cold one does.
func (w *world) verifyWalk(b *build, lost map[types.ItemID]bool, roots []mono.Root, claimed []*modNode, all []*diag.Diagnostic) {
	fc := w.front
	skipped := map[types.ModuleID]bool{}
	for _, m := range claimed {
		skipped[m.id] = true
	}
	var rest []mono.Root
	for _, r := range roots {
		if !skipped[w.items.Get(r.Item).Module] {
			rest = append(rest, r)
		}
	}
	var part []*diag.Diagnostic
	if len(rest) > 0 {
		var ie *source.InternalError
		if part, ie = w.collectWalk(b, lost, &walkReach{}, rest); ie != nil {
			return // the whole walk ran clean of it; nothing to compare
		}
	}
	if sameDiags(part, all) {
		return
	}
	what := "a walk the marker skips reports something"
	for _, d := range all {
		if !slices.ContainsFunc(part, func(p *diag.Diagnostic) bool { return sameDiag(p, d) }) {
			what = fmt.Sprintf("the marker skips a walk that reports %s %s", d.Code, d.Message)
			break
		}
	}
	for _, m := range claimed {
		k, ok := fc.walkKey(m)
		if !ok {
			continue
		}
		fc.mismatchKind("walk", k, m.path+": "+what)
		_ = fc.st.RemoveFile(cache.KindIface, k)
	}
}

// sameDiags reports whether a and b hold the same diagnostics, in order.
func sameDiags(a, b []*diag.Diagnostic) bool {
	return slices.EqualFunc(a, b, sameDiag)
}

func sameDiag(a, b *diag.Diagnostic) bool {
	return a.Code == b.Code && a.At == b.At && a.Message == b.Message
}

// hasErrors reports whether ds holds any diagnostic: a module that failed
// its check has bodies the walk treats as leaves, so its clean walk would
// not be the walk of the fixed module's bodies; nothing is marked then.
func hasErrors(ds []*diag.Diagnostic) bool { return len(ds) > 0 }

// walkEnv is the walk's mono.Env: the session's, with more leaves.
//
//   - A body that reads a const with no value is a leaf (nil). Such a const
//     failed (E0901, E0902, or an ICE of its evaluation; its diagnostic is in
//     the report), so its readers are poisoned for eval, and mono's
//     content hash of an instance that reads it would be an ICE (iface: a
//     const's fact is its value). So a failed const adds its own diagnostic
//     and never an E0000 of the walk.
//   - In a corpus session whose Through is before flow (noFlow), stage 7 ran
//     on the units' bodies silently, and a body it did not accept is a leaf,
//     as it is poisoned, and so a leaf, in a flow-clean session. So the walk
//     never asks OnerrRuns of a body without decisions, and it reaches no
//     chain a flow-clean session would not.
//   - A body that encloses, at any depth, a fn literal with no body is a
//     leaf. The literal's check failed (its diagnostic is in the report),
//     and mono's content hash of a literal is its body's (iface FnRef), so
//     walking the enclosing body would be an E0000 (a FuzzCheck corpus
//     entry, a0330440db9679ea, found it).
//   - So is a body that reads a const or global whose value names such a
//     literal (`const F = fn(...) {...}` whose literal failed its check, or
//     did not lower because it reads a const with no type; `const G =
//     F`; a struct const holding one): the reader's hash is the value's,
//     and the value's is the literal body's.
type walkEnv struct {
	*monoEnv
	noFlow bool
	// lost holds every fn literal with no body and every item that encloses
	// one.
	lost map[types.ItemID]bool
	// noContract holds every function with a contract predicate that has
	// no body (lostContracts).
	noContract map[types.ItemID]bool
	// reach records every body the walk asked for (walkcache.go's reads).
	reach *walkReach
}

// lostLiterals is walkEnv.lost for b's modules: one pass over the item
// table, since a lifted literal is in no Module.Items, and a literal whose
// check failed still has an entry in the body store (poisoned), so the test
// is mono's Body. Each literal with none marks itself and its Owner chain.
func (w *world) lostLiterals(b *build) map[types.ItemID]bool {
	var lost map[types.ItemID]bool
	env := w.sem5.monoEnv(w)
	mods := map[types.ModuleID]bool{}
	for _, m := range b.mods {
		if m.mod != nil {
			mods[m.id] = true
		}
	}
	for i := 1; i < w.items.NumItems(); i++ {
		id := types.ItemID(i)
		it := w.items.Get(id)
		if it.Kind != types.KindLiteral || !mods[it.Module] || env.Body(id) != nil {
			continue
		}
		if lost == nil {
			lost = map[types.ItemID]bool{}
		}
		lost[id] = true
		for o := it.Owner; o != types.NoItem && !lost[o]; o = w.items.Get(o).Owner {
			lost[o] = true
		}
	}
	return lost
}

func (e walkEnv) Body(id types.ItemID) *mir.Body {
	e.reach.add(id)
	be := e.w.sem5.body(id)
	if be == nil || e.noFlow && !be.flowed || e.lost[id] || e.noContract[id] {
		return nil
	}
	b := e.monoEnv.Body(id)
	if b == nil || readsValuelessConst(e.w.items, b) || len(e.lost) > 0 && readsLostLiteral(e.w.items, e.w.tab.Consts(), b, e.lost) ||
		len(e.noContract) > 0 && callsAny(b, e.noContract) {
		return nil
	}
	return b
}

// lostContracts is walkEnv.noContract: every function with a contract
// predicate that has no lowered body. The predicate's check failed (its
// diagnostic is in the report), and mono's content hash of the function's
// instance names the predicate's body (iface), so walking the function, or a
// body that calls it, would be an E0000 beside the real error (the same
// class as lostLiterals). Both are leaves. One pass over the item table, as
// lostLiterals. A corpus unit's build skips the items of other units, which
// their own goroutines may still be declaring (an E0000 at -j 8 only): a
// unit reaches its own modules and std's, which is read-only by then.
func (w *world) lostContracts(b *build) map[types.ItemID]bool {
	var mods map[types.ModuleID]bool
	if b.unit {
		mods = map[types.ModuleID]bool{}
		for _, bb := range []*build{b, b.std} {
			if bb == nil {
				continue
			}
			for _, m := range bb.mods {
				if m.mod != nil {
					mods[m.id] = true
				}
			}
		}
	}
	var out map[types.ItemID]bool
	for i := 1; i < w.items.NumItems(); i++ {
		id := types.ItemID(i)
		it := w.items.Get(id)
		if mods != nil && !mods[it.Module] {
			continue
		}
		fd, ok := it.Decl.(*types.FuncDecl)
		if !ok || fd == nil {
			continue
		}
		for _, ct := range fd.Sig.Contracts {
			if ct.Body == types.NoItem {
				continue
			}
			if !w.sem5.hasBody(ct.Body) {
				if out == nil {
					out = map[types.ItemID]bool{}
				}
				out[id] = true
				break
			}
		}
	}
	return out
}

// lostPredicateOwners adds to noContract every function one of whose
// contract predicates encloses a fn literal with no body (lost holds the
// predicate, lostLiterals' Owner chain): the function's instance hash names
// the predicate's body and so the literal's, so its callers are leaves as
// for a predicate with no body; walking them was "iface: no body for
// main.F#requires1#fn0" beside the literal's own diagnostic.
func lostPredicateOwners(items *types.Items, lost, noContract map[types.ItemID]bool) map[types.ItemID]bool {
	for id := range lost {
		it := items.Get(id)
		if it.Owner == types.NoItem {
			continue
		}
		fd, ok := items.Get(it.Owner).Decl.(*types.FuncDecl)
		if !ok || fd == nil {
			continue
		}
		for _, ct := range fd.Sig.Contracts {
			if ct.Body == id {
				if noContract == nil {
					noContract = map[types.ItemID]bool{}
				}
				noContract[it.Owner] = true
				break
			}
		}
	}
	return noContract
}

// callsAny reports whether b names (calls, or takes as a fn value) any
// item of set.
func callsAny(b *mir.Body, set map[types.ItemID]bool) bool {
	for i := range b.Blocks {
		blk := &b.Blocks[i]
		if set[blk.Term.Callee.Fn.Item] { // NoItem unless a direct callee
			return true
		}
		for j := range blk.Stmts {
			if rv := &blk.Stmts[j].Rvalue; rv.Kind == mir.RvFnRef && set[rv.Fn.Item] {
				return true
			}
		}
	}
	return false
}

// readsValuelessConst reports whether b reads the value of, or a static
// reference to, a const that has no value.
func readsValuelessConst(items *types.Items, b *mir.Body) bool {
	for i := range b.Blocks {
		for j := range b.Blocks[i].Stmts {
			rv := &b.Blocks[i].Stmts[j].Rvalue
			if rv.Kind != mir.RvConstRef && rv.Kind != mir.RvGlobalRef || rv.Item == types.NoItem {
				continue
			}
			if d, ok := items.Get(rv.Item).Decl.(*types.ConstDecl); ok && !d.HasValue {
				return true
			}
		}
	}
	return false
}

// readsLostLiteral reports whether b reads the value of, or a static
// reference to, a const or global whose value names a fn literal of lost
// (walkEnv.lost) as a function word, at any depth of its pointees.
func readsLostLiteral(items *types.Items, consts *types.ConstTable, b *mir.Body, lost map[types.ItemID]bool) bool {
	var seen map[types.ConstID]bool
	var names func(c types.ConstID) bool
	names = func(c types.ConstID) bool {
		if c == types.NoConst || seen[c] {
			return false
		}
		if seen == nil {
			seen = map[types.ConstID]bool{}
		}
		seen[c] = true
		for _, r := range consts.Get(c).Relocs {
			if r.Kind == types.RelocFn && lost[r.Fn.Item] || r.Kind == types.RelocData && names(r.Data) {
				return true
			}
		}
		return false
	}
	for i := range b.Blocks {
		for j := range b.Blocks[i].Stmts {
			rv := &b.Blocks[i].Stmts[j].Rvalue
			if rv.Kind != mir.RvConstRef && rv.Kind != mir.RvGlobalRef || rv.Item == types.NoItem {
				continue
			}
			switch d := items.Get(rv.Item).Decl.(type) {
			case *types.ConstDecl:
				if d.HasValue && names(d.Value) {
					return true
				}
			case *types.GlobalDecl:
				if d.HasValue && names(d.Value) {
					return true
				}
			}
		}
	}
	return false
}

// hasBody reports whether id has a lowered body in the store, without
// decoding a lazy one: a module hit's stored result holds the body it had.
func (s *semantics) hasBody(id types.ItemID) bool {
	s.mu.RLock()
	e := s.bodies[id]
	s.mu.RUnlock()
	return e != nil && (e.lazy != nil || e.mir != nil)
}

// isLimitRoot reports whether it, an item of its module's Items, is a root
// of the instance-limit walk: a non-generic function with a body and no
// const parameter, or a test.
func isLimitRoot(it *types.Item) bool {
	switch d := it.Decl.(type) {
	case *types.FuncDecl:
		return it.Kind == types.KindFunc && !d.Bodiless && !d.Intrinsic && len(d.Sig.Generics) == 0 && !hasConstParam(&d.Sig)
	case *types.TestDecl:
		return true
	}
	return false
}
