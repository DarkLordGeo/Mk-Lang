// Why: `for x in it` iterates any value with `Next(mut self): ?T`, not only
// arrays, slices and str, so the loop head needs its own typing rules.
//
// What: iterHead (types a loop over an iterator), nextMethod, nextMiss and
// loopVar (binds a name or a destructured record).
//
// How: the Next call is checked with the same call-site rules as
// `it.Next()` and recorded on the RefSource node for lower. An iterator is
// iterated by value and has no index form; a miss is recorded as a read of
// the type's members, so a dependency that adds a Next re-checks the loop.

package check

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// iterHead types `for p in e` where e is an iterator: a value that
// is not an array, a slice, a str or a Slice[T] implementer, and whose
// `Next` takes `mut self` or `&self`, nothing else, and gives `?T`. The
// Next call is recorded on the RefSource node: its Use and Inst,
// the receiver mode and the deref layers in Adjust, ?T in Types. It returns
// T and whether e was taken as an iterator; false leaves e to loopElem.
func (u *unit) iterHead(h *ast.InHead, src *ast.RefSource, xt types.TypeID) (types.TypeID, bool) {
	if xt == types.Poison {
		return types.Poison, false
	}
	v, n := u.deref(xt)
	vt := u.tab.Get(v)
	switch vt.Kind {
	case types.KSlice, types.KMutSlice, types.KArray, types.KStr, types.KPoison:
		return types.Poison, false
	case types.KNamed:
		if _, ok := u.sliceElemOf(v); ok {
			return types.Poison, false // a Slice[T] implementer iterates as one
		}
	}
	next, ok := u.c.cfg.Interner.Lookup("Next")
	if !ok {
		// No module spells Next: the miss is still a read of v's members,
		// so adding a Next re-checks this loop.
		u.nextMiss(v)
		return types.Poison, false
	}
	cs, ok := u.nextMethod(v, next, src.X.Span())
	if !ok {
		return types.Poison, false
	}
	sig := cs.sig
	if (sig.Receiver != types.ByMut && sig.Receiver != types.ByShared) || len(sig.Params) != 1 || sig.Errors != types.EmptyErrors ||
		u.tab.Kind(sig.Result) != types.KOptional {
		u.err(diag.E0301, src.X.Span(), "`"+u.str(v)+"` has a `Next` that is not an iterator's: it takes `mut self` or `&self` and gives `?T`").
			Text("expected", "an iterator (`Next(mut self): ?T`)").Text("found", u.str(v)).Text("next", u.c.sigString(sig))
		return types.Poison, true
	}
	if src.Mode != ast.ByCopy {
		// An iterator is iterated by value.
		u.err(diag.E0301, src.Span(), "an iterator is iterated by value; `for x := it.Next()` keeps `it`").
			Text("expected", u.str(v)).Text("found", u.str(xt)).Alt("for x := it.Next() { }")
		return types.Poison, true
	}
	if h.Value != nil || h.Key != nil && h.Pat != nil {
		// No index form over an iterator: E0101, in the types group,
		// emitted by check, so it suppresses nothing of the build's other
		// type errors.
		u.err(diag.E0101, h.Key.Span(), "an iterator has no index; count in the body").In(diag.PhaseTypes).
			Text("found", u.str(v)).Alt("count in the body")
		return types.Poison, true
	}
	recvPassed := u.tab.Mut(v)
	adj := AdjRefMut
	if sig.Receiver == types.ByShared {
		recvPassed, adj = u.tab.Ref(v), AdjRefShared
	}
	in := u.newInfer(sig.Generics, cs.self)
	in.preset(0, cs.pre)
	in.unify(in.at(sig.Params[0].Type), recvPassed)
	inst, ok := in.finish(src.X.Span(), src.Span())
	if !ok {
		return types.Poison, true
	}
	// The loop calls Next at the source as `it.Next()` would: the same
	// call-site rules: E1101 for a cpu clause the context lacks, a
	// naked, mmio or bodiless extern callee; E0705 for a
	// gen-only one.
	if cs.kind == UseMethod {
		if d, ok := u.c.declOfItem(u.c.cfg.Items.Get(cs.item)).(*types.FuncDecl); ok {
			u.unsafeCall(src, cs.item, d)
		}
	}
	u.genUse(src, sig, "call")
	res := in.final(sig.Result)
	*u.b.use(src.ID()) = Use{Kind: cs.kind, Item: cs.item, Index: cs.index, Inst: inst}
	*u.b.adjust(src.ID()) = Adjust{Kind: adj, Deref: uint8(n)}
	u.setType(src, res)
	if res == types.Poison || u.tab.Kind(res) != types.KOptional {
		return types.Poison, true
	}
	return u.tab.Get(res).Elem, true
}

// nextMethod finds `Next` on v: a method of a named type, or a method of an
// interface among a type parameter's bounds. A private Next of another
// module's type is E0204 at at.
func (u *unit) nextMethod(v types.TypeID, next source.Symbol, at source.Span) (*callee, bool) {
	vt := u.tab.Get(v)
	switch vt.Kind {
	case types.KNamed:
		cands, folded := u.m.lookup(v, next)
		if folded || len(cands) != 1 || (cands[0].Kind != MemberFunc && cands[0].Kind != MemberImplMethod) {
			// Not an iterator today; a dependency that adds, removes or
			// renames a member changes that, as memberLookup's
			// miss records.
			u.nextMiss(v)
			return nil, false
		}
		it := u.c.cfg.Items.Get(cands[0].Item)
		u.x.rec.signature(it.Key)
		d, ok := u.c.declOfItem(it).(*types.FuncDecl)
		if !ok || d.Sig.Receiver == types.NoReceiver {
			return nil, false
		}
		u.privateMethod(at, cands[0]) // the loop calls Next
		return &callee{kind: UseMethod, item: it.ID, sig: &d.Sig}, true
	case types.KParam:
		pi := vt.Index
		if int(pi) >= len(u.env.gen) {
			return nil, false
		}
		for _, b := range u.env.gen[pi].Bounds {
			if b.Kind != types.BoundInterface || b.Item == types.NoItem {
				continue
			}
			it := u.c.cfg.Items.Get(b.Item)
			// The interface's method list decides the loop either way: a
			// dependency that gives it a Next re-checks this one.
			u.x.rec.signature(it.Key)
			d, ok := u.c.declOfItem(it).(*types.InterfaceDecl)
			if !ok {
				continue
			}
			for k := range d.Methods {
				if d.Methods[k].Name == next {
					return &callee{kind: UseIfaceMethod, item: b.Item, index: uint32(k), sig: &d.Methods[k], self: v, pre: b.Args}, true
				}
			}
		}
	}
	return nil, false
}

// nextMiss records that v has no usable member Next: the member list read
// of a miss (expr_member.go memberLookup), so a warm build re-checks the
// loop when a dependency gives v a Next.
func (u *unit) nextMiss(v types.TypeID) {
	if u.tab.Kind(v) != types.KNamed {
		return
	}
	key := u.nsKey(v)
	if key == (types.Key{}) {
		return
	}
	u.x.rec.noItem(key, "Next")
	u.x.rec.exports(key)
}

// loopVar binds the loop variable of an InHead to the element type t: a
// name, or a destructured record, with the index form's Key an
// int.
func (u *unit) loopVar(h *ast.InHead, t types.TypeID, indexed bool) {
	if h.Pat == nil {
		if indexed {
			u.bindName(h.Key, types.Int)
			u.bindName(h.Value, t)
			return
		}
		u.bindName(h.Key, t)
		return
	}
	if indexed || h.Key != nil {
		u.bindName(h.Key, types.Int)
	}
	u.setType(h.Pat, t) // lower reads an implementer's element type here
	u.loopPat = true    // E0305 without `..`, which a for head refuses
	u.recordPat(h.Pat, t)
	u.loopPat = false
}
