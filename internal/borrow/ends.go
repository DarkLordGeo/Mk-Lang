// Why: a signature's `ends p` promises callers that the call ends every loan
// p held; the body must keep that promise, or the promise is unchecked.
//
// What: endsDecl, endsProven, blockEndsWhole and tempBorrowsWhole.
//
// How: on every path from entry to a success exit the body must pass a call
// that ends the whole p. Otherwise the function is a primitive: E1102 at the
// signature in the flow phase, unless its module is unsafe-permitted.

package borrow

import (
	"fmt"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// endsDecl runs the backward must-analysis, one bit per ends parameter,
// O(blocks) per parameter.
func (c *checker) endsDecl() {
	sig := c.env.Sig
	if sig == nil || len(sig.Flows.Ends) == 0 || len(c.rpo) == 0 {
		return
	}
	if c.env.Module != nil && c.env.Module.UnsafePermitted {
		return
	}
	for _, p := range sig.Flows.Ends {
		if p == types.NoParam || int(p) > int(c.body.ParamCount) {
			continue
		}
		if c.endsProven(mir.LocalID(p)) {
			continue
		}
		c.errAt0(diag.E1102, sig.Span, "%s declares ends %s but does not end all of it on every path; only an unsafe-permitted module may define such a primitive",
			c.symbol(sig.Name), c.paramName(p)).In(diag.PhaseFlow).Text("param", c.paramName(p))
	}
}

// endsProven is the greatest fixpoint of P(b) = b holds a call that ends the
// whole p, or b is not a success return and every successor has P.
func (c *checker) endsProven(p mir.LocalID) bool {
	n := len(c.body.Blocks)
	has := make([]bool, n)
	for _, b := range c.rpo {
		has[b] = c.blockEndsWhole(b, p)
	}
	prov := make([]bool, n)
	for i := range prov {
		prov[i] = true
	}
	var buf []mir.BlockID
	for changed := true; changed; {
		changed = false
		for i := len(c.rpo) - 1; i >= 0; i-- {
			b := c.rpo[i]
			if !prov[b] || has[b] {
				continue
			}
			t := &c.body.Blocks[b].Term
			v := true
			switch t.Kind {
			case mir.TermReturn:
				v = t.Exit != mir.ExitNormal && t.Exit != mir.ExitElse
			case mir.TermTailCall:
				// A success exit whose call does not end p (has[b] is
				// false here): the callee's own ends p is its own.
				v = false
			default:
				buf = c.body.Successors(b, buf[:0])
				for _, s := range buf {
					v = v && prov[s]
				}
			}
			if !v {
				prov[b] = false
				changed = true
			}
		}
	}
	return prov[mir.EntryBlock]
}

// blockEndsWhole reports whether block b's terminator is a non-spawned call
// that ends the whole parameter p: an ends argument that is an ArgMove of p,
// an ArgReborrow of p, Move(t) of a temporary an RvRef in the block made
// from p or p.* with no other step, or Move(t) of p's argument slot.
func (c *checker) blockEndsWhole(b mir.BlockID, p mir.LocalID) bool {
	t := &c.body.Blocks[b].Term
	if !isCall(t) || t.Spawn != mir.NoLocal {
		return false
	}
	fv := c.peekFlows(t)
	if fv == nil {
		return false
	}
	args := c.body.Args[t.Args.Start:t.Args.End()]
	for _, q := range fv.ends {
		i := int(q) - 1
		if i < 0 || i >= len(args) {
			continue
		}
		a := args[i]
		pl := a.Operand.Place
		if a.Operand.Kind != mir.OpCopy && a.Operand.Kind != mir.OpMove || pl.Proj.Len != 0 {
			continue
		}
		switch {
		case a.Mode == mir.ArgMove && pl.Local == p, a.Mode == mir.ArgReborrow && pl.Local == p:
			return true
		case a.Mode == mir.ArgMove && c.body.Locals[pl.Local].Kind == mir.LocalTemp:
			if c.tempBorrowsWhole(b, pl.Local, p) {
				return true
			}
			// An argument slot `t = Use(Move(p))` passes the whole p on,
			// as the direct ArgMove of p does.
			if sl, ok := c.argSlots[pl.Local]; ok && sl.src == rootID(p) {
				return true
			}
		}
	}
	return false
}

// tempBorrowsWhole reports whether the last assignment of temporary t in
// block b is an RvRef of p or of p.* with no other step.
func (c *checker) tempBorrowsWhole(b mir.BlockID, t, p mir.LocalID) bool {
	stmts := c.body.Blocks[b].Stmts
	for i := len(stmts) - 1; i >= 0; i-- {
		s := &stmts[i]
		if s.Kind != mir.StAssign || s.Place.Proj.Len != 0 || s.Place.Local != t {
			continue
		}
		rv := &s.Rvalue
		if rv.Kind != mir.RvRef || rv.Place.Local != p {
			return false
		}
		steps := c.body.Proj(rv.Place)
		return len(steps) == 0 || len(steps) == 1 && steps[0].Kind == mir.ProjDeref
	}
	return false
}

// errAt0 is errAt at a source span rather than a body span.
func (c *checker) errAt0(code diag.Code, at source.Span, format string, args ...any) *diag.Diagnostic {
	d := diag.New(code, at, fmt.Sprintf(format, args...))
	c.diags = append(c.diags, d)
	return d
}
