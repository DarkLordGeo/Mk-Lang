// Why: `become f(args)` is a guaranteed tail call: the frame is freed
// before the callee runs, so the borrow rules of a call and of a return
// both apply, plus rules of its own.
//
// What: isCall, tailCall (the transfer of a TermTailCall) and tailArgLoans.
//
// How: a TermTailCall is checked as the call it makes into _0 followed by
// the return. No argument may hold a loan on a root of the frame (E0603,
// its `become` case); a linear local still holding a value is E0402, since
// nothing runs after the callee to consume it. The result's loans meet the
// boundary check of `_0 = e`, as for `return f(args)`.

package borrow

import (
	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// isCall reports whether t calls a function: a TermCall or a TermTailCall,
// whose callee, arguments and Dest (_0 for a tail call) read the same.
func isCall(t *mir.Terminator) bool {
	return t.Kind == mir.TermCall || t.Kind == mir.TermTailCall
}

// tailCall is the transfer of a TermTailCall: the call into _0 (its Dest, the
// zero Place) with the frame-loan rule checked on the arguments, then E0402
// for the linear locals still holding a value, then the return's parameter
// checks.
func (c *checker) tailCall(st *state, ctx *stmtCtx, t *mir.Terminator) {
	ct := *t
	ct.Kind = mir.TermCall
	ct.Dest = mir.LocalPlace(mir.ReturnLocal)
	c.tail = true
	outs := c.call(st, ctx, &ct)
	c.tail = false
	if len(outs) > 0 {
		st = outs[0].st
	}
	if c.report {
		for r := range c.roots {
			ri := &c.roots[r]
			if !ri.linear || ri.kind != rkUser && ri.kind != rkTemp && ri.kind != rkScope && ri.kind != rkHolder {
				continue
			}
			if initOf(st.get(rootID(r)))&initI == 0 {
				continue
			}
			d := c.errAt(diag.E0402, ctx.span, "%s is not consumed before this `become`, and nothing runs after its callee", c.rootName(rootID(r))).
				Text("place", c.rootName(rootID(r)))
			c.releaseFacts(d, rootID(r), ri.typ)
		}
	}
	exit := mir.ExitNormal
	if c.env.Sig != nil && c.env.Sig.Errors != types.EmptyErrors {
		exit = mir.ExitMayBeError // the callee's error is the body's
	}
	c.ret(st, ctx, &mir.Terminator{Kind: mir.TermReturn, Exit: exit, Span: t.Span})
}

// tailArgLoans is rule 3 at a TermTailCall: a loan an argument holds on a
// root of the frame (a local, a parameter's own storage, a temporary) is
// E0603, since the frame is gone when the callee runs.
func (c *checker) tailArgLoans(ctx *stmtCtx, argH []setID) {
	if !c.report {
		return
	}
	for _, S := range argH {
		for _, l := range c.sets.get(S) {
			lo := &c.loans[l]
			if lo.static || lo.abstract || lo.target == noRoot || !c.roots[lo.target].frame() {
				continue
			}
			c.errAt(diag.E0603, ctx.span, "an argument of this `become` borrows %s, which ends when the tail call replaces the frame",
				c.rootName(lo.target)).Fact("loan", c.loanFact(loanID(l))).Text("reason", "become")
		}
	}
}
