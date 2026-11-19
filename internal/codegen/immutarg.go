// Why: a large by-value argument travels as a pointer to a fresh copy the
// caller makes for every call; when the callee never writes it, LLVM can
// skip the copy, but only if the parameter says so.
//
// What: immutParam, tailForwards, withImmutParams (the readonly and
// captures(none) attributes) and checkImmutParams (the backstop).
//
// How: a by-value parameter that travels Indirect and that the callee can
// never write is readonly and captures(none), so LLVM's MemCpyOpt may pass
// the copy's source instead of the copy where it proves that source
// unchanged until the call returns (processImmutArgument). Codegen still
// makes the copy.

package codegen

import (
	"mk/internal/abi"
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// immutParam reports whether Mk parameter i of Funcs[fi] is an Indirect,
// non-byval parameter the callee never writes and never keeps: a Mk
// function with a body (not extern: C may write its parameter copy), the
// parameter not var (only a var parameter may be assigned or
// borrowed mut), and its type holding no inline interior mutability
// (Facts.Interior: a Cell, Mutex, Chan, Arena, atomic or Scope changes
// through a shared reference). A reference to a by-value parameter cannot
// outlive the call (the parameter's lifetime ends at return), so the
// pointer is not captured. Signature facts only: the parameter's Var is the
// declaration's (lower copies it into the body's local), never a property
// of what the body does, but one: a parameter a tail call passes on
// (tailForwards) is not readonly, since its incoming pointer reaches a
// callee that may write it (a var parameter of the same fn type), and
// MemCpyOpt would then hand that callee the caller's own storage.
// PackFacts keys that body fact into every pack that
// calls the function directly.
func (g *packGen) immutParam(f *Func, sl abi.Slot, i int) bool {
	if sl.Kind != abi.Indirect || sl.Byval || sl.Size == 0 {
		return false
	}
	l := f.Body.Locals[i+1]
	return !l.Var && !g.env.Facts(l.Type).Interior && tailForwards(f.Body)&(1<<min(i, 63)) == 0
}

// tailForwards is the set of f's parameters (bit i for parameter i, the
// receiver first; bit 63 for every one past 62) that a TermTailCall of the
// body passes on whole, a copy or move of the parameter's own local at any
// argument position: codegen forwards its incoming pointer (tailcall.go).
// It reads the MIR only, so PackFacts can compute it without a module.
func tailForwards(b *mir.Body) uint64 {
	if b == nil {
		return 0
	}
	var set uint64
	for bi := range b.Blocks {
		t := &b.Blocks[bi].Term
		if t.Kind != mir.TermTailCall {
			continue
		}
		for _, a := range b.Args[t.Args.Start:t.Args.End()] {
			o := a.Operand
			if (o.Kind == mir.OpCopy || o.Kind == mir.OpMove) && o.Place.Proj.Len == 0 &&
				o.Place.Local >= 1 && int(o.Place.Local) <= int(b.ParamCount) {
				set |= 1 << min(int(o.Place.Local)-1, 63)
			}
		}
	}
	return set
}

// withImmutParams is ls with readonly and captures(none) on every
// immutParam of Funcs[fi], or ls itself when there is none. ls is shared
// through lowerSig's memo, so a changed signature is a copy.
func (g *packGen) withImmutParams(fi int, ls *lsig) *lsig {
	f := &g.u.Funcs[fi]
	// A naked function's asm reads its parameters by the ABI, unchecked, so
	// it keeps the plain copy.
	if f.Body == nil || g.isExtern(f) || g.nakedFn(f) {
		return ls
	}
	var out *lsig
	for i, sl := range ls.s.Params {
		if !g.immutParam(f, sl, i) {
			continue
		}
		if out == nil {
			c := *ls
			c.lattrs = append([]llvm.Attrs(nil), ls.lattrs...)
			out = &c
		}
		k := ls.first[i]
		out.lattrs[k] = out.lattrs[k].Flag("readonly").Flag("captures(none)")
	}
	if out == nil {
		return ls
	}
	return out
}

// checkImmutParams raises an ICE when the body writes a parameter that
// withImmutParams declared readonly: an assignment or call result into it,
// a mut borrow or reslice of it, a mut reborrow, or an asm output, each
// with no deref on the place (a write through a pointer the parameter
// holds is not a write of the parameter). Lower and the loan pass never
// produce one; this keeps a broken invariant from becoming a
// silent miscompile under the readonly attribute.
func (fg *fnGen) checkImmutParams() {
	g, b := fg.g, fg.b
	f := &g.u.Funcs[fg.fi]
	if f.Body == nil || g.isExtern(f) {
		return
	}
	var ro map[mir.LocalID]bool
	for i, sl := range fg.sig.s.Params {
		if g.immutParam(f, sl, i) {
			if ro == nil {
				ro = map[mir.LocalID]bool{}
			}
			ro[mir.LocalID(i+1)] = true
		}
	}
	if ro == nil {
		return
	}
	// direct: writing p writes the parameter's own bytes. A deref, or an
	// index into a slice (through its data pointer), leaves them.
	direct := func(p mir.Place) bool {
		if !ro[p.Local] {
			return false
		}
		cur := b.Locals[p.Local].Type
		for _, s := range b.Proj(p) {
			if s.Kind == mir.ProjDeref || (s.Kind == mir.ProjIndex && g.tt.Get(cur).IsSliceLike()) {
				return false
			}
			cur = s.Type
		}
		return true
	}
	// mutBorrow: a mut borrow of the parameter's own storage. A reborrow of
	// a mut T or a mut []T parameter (its referent, not its bytes) is not.
	mutBorrow := func(p mir.Place) bool {
		if !direct(p) {
			return false
		}
		t := b.Locals[p.Local].Type
		if pr := b.Proj(p); len(pr) > 0 {
			t = pr[len(pr)-1].Type
		}
		ty := g.tt.Get(t)
		return ty.Kind != types.KMut && !ty.IsSliceLike()
	}
	bad := func(p mir.Place) {
		source.ICEf(fg.spanOf(fg.span), "codegen: non-var parameter %d of %s is written but declared readonly",
			p.Local, g.items.Get(f.Item).Key)
	}
	for bi := range b.Blocks {
		bl := &b.Blocks[bi]
		for si := range bl.Stmts {
			s := &bl.Stmts[si]
			switch s.Kind {
			case mir.StAssign:
				if direct(s.Place) {
					bad(s.Place)
				}
				rv := &s.Rvalue
				if (rv.Kind == mir.RvRef || rv.Kind == mir.RvSlice) && rv.Borrow == mir.Exclusive && mutBorrow(rv.Place) {
					bad(rv.Place)
				}
			case mir.StAsm:
				for _, o := range b.Asms[s.Asm].Operands {
					if (o.Kind == mir.AsmOut || o.Kind == mir.AsmLateOut || o.Kind == mir.AsmInOut || o.Kind == mir.AsmInLateOut) && direct(o.Out) {
						bad(o.Out)
					}
				}
			}
		}
		t := &bl.Term
		if isCall(t) {
			if direct(t.Dest) {
				bad(t.Dest)
			}
			for _, a := range b.Args[t.Args.Start:t.Args.End()] {
				if a.Mode == mir.ArgReborrow && (a.Operand.Kind == mir.OpCopy || a.Operand.Kind == mir.OpMove) && mutBorrow(a.Operand.Place) {
					bad(a.Operand.Place)
				}
			}
		}
	}
}
