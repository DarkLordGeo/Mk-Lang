// Why: `mk query why-live F line` tells which holder keeps a loan live at a
// line of F, and borrow exports no loan table for an accepted body (loans
// never reach code generation), so the answer is computed here.
//
// What: the whyLive answer, liveLoan, and the joinMax and joinComma helpers
// that keep it short.
//
// How: from F's lowered MIR, a loan is a borrow statement (RvRef, RvSlice) on
// or before the line; its holders are the locals the reference flows into by
// copy, move, aggregate or a call's result (every reference argument of a
// call is taken as held by the result, which over-approximates a `from`
// clause); it is live at the line when a holder is used on or after it. A
// rejected body has no MIR here: its diagnostic (E0501's notes) names the
// holder exactly, and the answer says so.

package driver

import (
	"slices"
	"strconv"

	"mk/internal/mir"
)

// liveLoan is one borrow statement of a body.
type liveLoan struct {
	of    mir.LocalID // the borrowed place's local
	mut   bool
	line  uint32
	local mir.LocalID // the statement's destination
}

// whyLive is `why-live F line`.
func (qx *queryIndex) whyLive(e qEntry, lineArg string) []string {
	n, err := strconv.ParseUint(lineArg, 10, 32)
	if err != nil || n == 0 {
		return []string{"why-live takes a line number, got " + lineArg}
	}
	line := uint32(n)
	if !qx.isFunc(e) {
		return []string{e.name + " is not a function"}
	}
	b := qx.bodyOf(e.id)
	if b == nil {
		return []string{e.name + " has no checked body (bodiless, or rejected: read its diagnostics)"}
	}
	lineOf := func(sp mir.SpanID) uint32 { _, l := qx.at(b, sp); return l }
	// holds[l] are the loans (indexes into loans) local l may hold.
	var loans []liveLoan
	holds := make([]map[int]bool, len(b.Locals))
	addHold := func(l mir.LocalID, set map[int]bool) bool {
		changed := false
		for k := range set {
			if holds[l] == nil {
				holds[l] = map[int]bool{}
			}
			if !holds[l][k] {
				holds[l][k] = true
				changed = true
			}
		}
		return changed
	}
	opHolds := func(o mir.Operand) map[int]bool {
		if o.Kind == mir.OpCopy || o.Kind == mir.OpMove {
			return holds[o.Place.Local]
		}
		return nil
	}
	for _, bl := range b.Blocks {
		for _, st := range bl.Stmts {
			if st.Kind == mir.StAssign && (st.Rvalue.Kind == mir.RvRef || st.Rvalue.Kind == mir.RvSlice) {
				loans = append(loans, liveLoan{of: st.Rvalue.Place.Local, mut: st.Rvalue.Borrow == mir.Exclusive,
					line: lineOf(st.Span), local: st.Place.Local})
				addHold(st.Place.Local, map[int]bool{len(loans) - 1: true})
			}
		}
	}
	// Propagate to a fixed point (bounded: every step adds a loan to a
	// local, and there are finitely many).
	for changed := true; changed; {
		changed = false
		for _, bl := range b.Blocks {
			for _, st := range bl.Stmts {
				if st.Kind != mir.StAssign {
					continue
				}
				rv := st.Rvalue
				switch rv.Kind {
				case mir.RvUse, mir.RvCast:
					changed = addHold(st.Place.Local, opHolds(rv.A)) || changed
				case mir.RvRef, mir.RvSlice:
					changed = addHold(st.Place.Local, holds[rv.Place.Local]) || changed
				case mir.RvAggregate:
					for _, o := range b.Operands[rv.Ops.Start:rv.Ops.End()] {
						changed = addHold(st.Place.Local, opHolds(o)) || changed
					}
				}
			}
			if t := bl.Term; t.Kind == mir.TermCall || t.Kind == mir.TermTailCall {
				// A tail call's Dest is _0, the result.
				for _, a := range b.Args[t.Args.Start:t.Args.End()] {
					changed = addHold(t.Dest.Local, opHolds(a.Operand)) || changed
				}
			}
		}
	}
	// lastUse[l] is the last line on or after line where local l is read.
	lastUse := make([]uint32, len(b.Locals))
	use := func(l mir.LocalID, at uint32) {
		if at >= line && at > lastUse[l] {
			lastUse[l] = at
		}
	}
	useOp := func(o mir.Operand, at uint32) {
		if o.Kind == mir.OpCopy || o.Kind == mir.OpMove {
			use(o.Place.Local, at)
		}
	}
	for _, bl := range b.Blocks {
		for _, st := range bl.Stmts {
			at := lineOf(st.Span)
			if st.Kind != mir.StAssign {
				continue
			}
			rv := st.Rvalue
			useOp(rv.A, at)
			useOp(rv.B, at)
			if rv.Kind == mir.RvRef || rv.Kind == mir.RvSlice || rv.Kind == mir.RvLen || rv.Kind == mir.RvDiscriminant {
				use(rv.Place.Local, at)
			}
			if rv.Kind == mir.RvAggregate {
				for _, o := range b.Operands[rv.Ops.Start:rv.Ops.End()] {
					useOp(o, at)
				}
			}
			if st.Place.Proj.Len > 0 {
				use(st.Place.Local, at) // a write through a reference reads it
			}
		}
		t := bl.Term
		at := lineOf(t.Span)
		switch t.Kind {
		case mir.TermCall, mir.TermTailCall:
			for _, a := range b.Args[t.Args.Start:t.Args.End()] {
				useOp(a.Operand, at)
			}
		case mir.TermSwitch:
			useOp(t.On, at)
		case mir.TermReturn:
			use(0, at)
		}
	}
	name := func(l mir.LocalID) string {
		if l == 0 {
			return "the result"
		}
		if s := qx.str(b.Locals[l].Name); s != "" {
			return s
		}
		return ""
	}
	var out []string
	for i, ln := range loans {
		if ln.line > line {
			continue
		}
		var holders []string
		last := uint32(0)
		for l := range b.Locals {
			if !holds[l][i] || lastUse[l] == 0 {
				continue
			}
			if nm := name(mir.LocalID(l)); nm != "" && !slices.Contains(holders, nm) {
				holders = append(holders, nm)
			}
			last = max(last, lastUse[l])
		}
		if last == 0 {
			continue
		}
		kind := "&"
		if ln.mut {
			kind = "mut "
		}
		of := name(ln.of)
		if of == "" {
			of = "a temporary"
		}
		h := "a temporary"
		if len(holders) > 0 {
			h = joinMax(holders, 4)
		}
		out = append(out, kind+of+" (line "+itoaQ(int(ln.line))+") held by "+h+", used until line "+itoaQ(int(last)))
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) == 0 {
		out = []string{"no loan of " + e.name + " is live at line " + lineArg}
	}
	return append(out, "(from "+e.name+"'s MIR; a call's result is taken to hold every reference argument)")
}

// joinMax joins up to n names, then "and k more".
func joinMax(ns []string, n int) string {
	if len(ns) <= n {
		return joinComma(ns)
	}
	return joinComma(ns[:n]) + " and " + itoaQ(len(ns)-n) + " more"
}

func joinComma(ns []string) string {
	out := ""
	for i, s := range ns {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
