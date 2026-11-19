// Why: borrow judges every call by the callee's signature alone, so it
// needs one view of the flow facts (sources, `into` and `ends` clauses)
// whether the callee is a direct function, an interface method or a value.
//
// What: flowView, sigFlows and fnFlows, calleeFlows and peekFlows (the
// callee's flows at a call), readSig, and covered and coveredBy (whether a
// loan is covered by the body's own sources).
//
// How: flowView copies the flows of a types.Signature or a fn type once;
// reading a callee's signature records it in the result's read set.

package borrow

import (
	"mk/internal/mir"
	"mk/internal/types"
)

// flowView is the positional flows of one callee, as written (a generic
// callee is not substituted).
type flowView struct {
	paramTypes []types.TypeID
	into       []types.ParamIndex
	sources    []types.Source
	// fields is the callee's per-field clause, read by step 5
	// (fields.go resultFV); sources is then its union.
	fields []types.FieldSources
	stores []types.Store
	ends   []types.ParamIndex
	result types.TypeID
	errors types.ErrorSetID
	// generics is the callee's generic list length (Signature.Generics),
	// -1 for a fn type; storesLoans substitutes only when the call's
	// arguments match it.
	generics int
	// recvMut is a mut self receiver (Signature.Receiver ByMut): argument 0
	// is the receiver.
	recvMut bool
}

func sigFlows(s *types.Signature) *flowView {
	fv := &flowView{sources: s.Flows.Sources, fields: s.Flows.Fields, stores: s.Flows.Stores, ends: s.Flows.Ends, result: s.Result, errors: s.Errors,
		generics: len(s.Generics), recvMut: s.Receiver == types.ByMut}
	for _, p := range s.Params {
		fv.paramTypes = append(fv.paramTypes, p.Type)
		fv.into = append(fv.into, p.Into)
	}
	return fv
}

func fnFlows(fn *types.FnType) *flowView {
	fv := &flowView{sources: fn.Sources, fields: fn.Fields, stores: fn.Stores, ends: fn.Ends, result: fn.Result, errors: fn.Errors, generics: -1}
	for _, p := range fn.Params {
		fv.paramTypes = append(fv.paramTypes, p.Type)
		fv.into = append(fv.into, p.Into)
	}
	return fv
}

// calleeFlows returns the flows of a call's callee: Env.Callee for a
// direct call, Env.Method for an interface call, the fn type of the callee
// value, or none for a builtin. It records the signature read.
func (c *checker) calleeFlows(t *mir.Terminator) *flowView {
	if t.Callee.Kind == mir.CallDirect && c.env.Callee != nil && c.env.Callee(t.Callee.Fn.Item) == nil {
		c.ice("no signature for the callee item %d", t.Callee.Fn.Item)
	}
	fv := c.peekFlows(t)
	switch {
	case fv == nil:
	case t.Callee.Kind == mir.CallDirect:
		c.readSig(t.Callee.Fn.Item)
	case t.Callee.Kind == mir.CallInterface:
		c.readSig(t.Callee.Iface)
	}
	return fv
}

// peekFlows is calleeFlows for setup's pre-scan, which visits every block,
// reachable or not: it records no read and raises no ICE (the call rule does
// both when it reaches the call).
func (c *checker) peekFlows(t *mir.Terminator) *flowView {
	switch t.Callee.Kind {
	case mir.CallDirect:
		if c.env.Callee == nil {
			return nil
		}
		if s := c.env.Callee(t.Callee.Fn.Item); s != nil {
			return sigFlows(s)
		}
	case mir.CallInterface:
		if c.env.Method == nil {
			return nil
		}
		if s := c.env.Method(t.Callee.Iface, t.Callee.Method); s != nil {
			return sigFlows(s)
		}
	case mir.CallValue:
		ty := c.tab.Get(c.body.OperandType(t.Callee.Value))
		if ty.Kind == types.KFn && ty.Fn != nil {
			return fnFlows(ty.Fn)
		}
	}
	return nil
}

func (c *checker) readSig(item types.ItemID) {
	if item == types.NoItem || c.tab == nil {
		return
	}
	c.res.Reads.Add(types.Read{Kind: types.ReadSignature, Key: c.tab.Items().Get(item).Key})
}

// covered is the boundary check's coverage of an abstract loan by the body's
// sources: a source Behind false on p covers p.outer, p.inner and every tagged
// p.inner; Behind with an empty path (the automatic B(p)) covers p.inner and
// every tagged p.inner; Behind with path f... covers a tagged p.inner whose
// path starts with f... and an untagged p.inner; nothing Behind covers
// p.outer. A source Behind false on p also covers every loan declared into p
// (declaredInto, chains of `into` included): the caller computes the result
// from p's argument after the call's stores (step 5), so it already holds
// those loans (the cookbook's AddChild, `name into a` and `from a`).
func (c *checker) covered(l loanID) bool {
	if c.env.Sig == nil {
		return false
	}
	return c.coveredBy(l, c.env.Sig.Flows.Sources)
}

// coveredBy is covered against the sources srcs: the body's whole clause, or
// one field's sources of a per-field clause, whose declaredInto branch then
// reads that field's sources only.
func (c *checker) coveredBy(l loanID, srcs []types.Source) bool {
	lo := &c.loans[l]
	for _, s := range srcs {
		if !s.Behind && s.Param != lo.param && s.Param != types.NoParam && c.declaredInto(s.Param, l) {
			return true
		}
		if s.Param != lo.param {
			continue
		}
		if !s.Behind {
			return true
		}
		if !lo.inner {
			continue
		}
		if len(s.Path) == 0 || lo.tag == nil {
			return true
		}
		if len(lo.tag) >= len(s.Path) {
			ok := true
			for i, f := range s.Path {
				if lo.tag[i] != f.Field {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
	}
	return false
}
