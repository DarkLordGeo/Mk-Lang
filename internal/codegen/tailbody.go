// Why: LLVM 21's x86-64 lowering of a `musttail` call copies each byval
// argument to the bottom of the caller's frame before moving it into the
// callee's argument area, and a musttail call reserves no space there: the
// copy lands on the return address. A `become` that passes on a System V
// MEMORY parameter (byval) was such a call and crashed in dev builds.
//
// What: tailItems (the unit's items on a `become` edge), tailBody,
// tailSigOf, tailLinkage, tailDecl and emitTailEntry.
//
// How: a function with a body whose lowered signature has a byval
// parameter, and whose item ends a `become` or is the direct callee of one
// anywhere in the unit, is emitted twice. Its body goes under
// <symbol>$tail, whose byval parameters are plain pointers to memory the
// callee owns (Win64's form); its own symbol keeps the C ABI and is a
// wrapper that calls the body with the incoming byval pointers. Every call,
// function value and declaration names the C ABI symbol, as before; a
// `become` is a musttail call from one $tail body to another, which passes
// those pointers in registers and copies nothing. A chain of becomes then
// runs in one wrapper frame plus one body frame. Whether an item is on a
// `become` edge is a read of other packs' bodies, so PackFacts hashes it
// per member. A `become` through a `fn` value cannot reach a $tail body;
// check refuses one that passes on an argument that is not register-sized
// (E0313), which System V might pass byval.

package codegen

import (
	"mk/internal/abi"
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/types"
)

// tailSuffix names a split function's body: '$' is in no Mk symbol.
const tailSuffix = "$tail"

// tailItems is the set of items that end a TermTailCall or are the direct
// callee of one, over every body of the unit; memoized under idxMu while
// Funcs keeps its length.
func (u *Unit) tailItems() map[types.ItemID]bool {
	u.idxMu.Lock()
	if u.tailsN == len(u.Funcs)+1 {
		s := u.tails
		u.idxMu.Unlock()
		return s
	}
	u.idxMu.Unlock()
	s := map[types.ItemID]bool{}
	for fi := range u.Funcs {
		f := &u.Funcs[fi]
		if f.Body == nil {
			continue
		}
		for bi := range f.Body.Blocks {
			t := &f.Body.Blocks[bi].Term
			if t.Kind != mir.TermTailCall {
				continue
			}
			s[f.Item] = true
			if t.Callee.Kind == mir.CallDirect {
				s[t.Callee.Fn.Item] = true
			}
		}
	}
	u.idxMu.Lock()
	u.tails, u.tailsN = s, len(u.Funcs)+1
	u.idxMu.Unlock()
	return s
}

// tailBody reports whether Funcs[fi] is split into a $tail body and a C
// ABI wrapper (the file comment's rule).
func (g *packGen) tailBody(fi int) bool {
	f := &g.u.Funcs[fi]
	if f.Body == nil || g.nakedFn(f) || !g.u.tailItems()[f.Item] {
		return false
	}
	for _, sl := range g.funcSigOf(fi).s.Params {
		if sl.Kind == abi.Indirect && sl.Byval {
			return true
		}
	}
	return false
}

// tailSigOf is the lowered signature of Funcs[fi]'s $tail body: its C ABI
// signature with each byval parameter a plain pointer, nonnull, noalias and
// dereferenceable, and readonly where immutParam allows, as a non-byval
// Indirect parameter is declared.
func (g *packGen) tailSigOf(fi int) *lsig {
	if s, ok := g.tailsigs[fi]; ok {
		return s
	}
	f := &g.u.Funcs[fi]
	base := g.funcSigOf(fi)
	c := *base
	c.s.Params = append([]abi.Slot(nil), base.s.Params...)
	c.lattrs = append([]llvm.Attrs(nil), base.lattrs...)
	for i, sl := range c.s.Params {
		if sl.Kind != abi.Indirect || !sl.Byval {
			continue
		}
		sl.Byval = false
		c.s.Params[i] = sl
		a := llvm.Attrs{}.Flag("noundef").Flag("nonnull").Flag("noalias").Int("align", uint64(max(sl.Align, 1)))
		if sl.Size > 0 {
			a = a.Int("dereferenceable", sl.Size)
		}
		if g.immutParam(f, sl, i) {
			a = a.Flag("readonly").Flag("captures(none)")
		}
		c.lattrs[c.first[i]] = a
	}
	if g.tailsigs == nil {
		g.tailsigs = map[int]*lsig{}
	}
	g.tailsigs[fi] = &c
	return &c
}

// tailLinkage is the linkage of a $tail body: internal with an internal
// function, else hidden (ELF) or dso_local (COFF) external, so a `become`
// in another pack may name it; never a C name's default visibility.
func (g *packGen) tailLinkage(f *Func) (llvm.Linkage, llvm.Visibility, bool) {
	lk, vis, dso := g.linkage(f)
	switch {
	case lk == llvm.Internal:
		return lk, vis, dso
	case g.cfg.Target.OS == types.OSWindows:
		return llvm.External, llvm.Default, true
	}
	return llvm.External, llvm.Hidden, true
}

// tailDecl is the $tail body of split Funcs[fi] in this pack: its
// definition when the pack holds it, else a declaration.
func (g *packGen) tailDecl(fi int) *llvm.Function {
	if b, ok := g.tailDefs[fi]; ok {
		return b
	}
	g.funcDecl(fi) // its checks: a Local function of another pack is an ICE
	f := &g.u.Funcs[fi]
	ls := g.tailSigOf(fi)
	lk, vis, dso := g.tailLinkage(f)
	attrs := llvm.Attrs{}.Flag("nounwind")
	if ls.result == types.Never {
		attrs = attrs.Flag("noreturn")
	}
	return g.m.Declare(f.Symbol+tailSuffix, g.funcSig(ls, lk, vis, dso, attrs))
}

// emitTailEntry fills the C ABI wrapper of split Funcs[fi]: a plain call of
// its $tail body with the wrapper's own parameters (a byval one is the
// pointer to the wrapper's incoming copy, which lives until the wrapper
// returns), then a return of its result. Not a tail call: the body must
// not outlive the byval memory.
func (g *packGen) emitTailEntry(fi int) {
	w, body := g.defs[fi], g.tailDefs[fi]
	b := w.NewBlock("entry")
	var args []llvm.Value
	for i := range w.Sig().Params {
		args = append(args, w.Param(i))
	}
	sig := body.Sig()
	var argAttrs []llvm.Attrs
	if len(sig.ParamAttrs) != 0 {
		argAttrs = sig.ParamAttrs
	}
	r := b.Call(body.Type(), body.Ref(), args, argAttrs, sig.RetAttrs, llvm.Attrs{}.Flag("nounwind"))
	switch {
	case g.tailSigOf(fi).result == types.Never:
		b.Unreachable()
	case sig.Ret == g.m.Void():
		b.Ret(nil)
	default:
		b.Ret(&r)
	}
}
