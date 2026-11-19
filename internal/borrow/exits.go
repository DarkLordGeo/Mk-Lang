// Why: a function's result may carry loans out of the body only on memory
// its signature names as a source, and its parameters end at return; these
// checks are what make a signature a promise callers can rely on.
//
// What: boundary and boundaryFields (the check at `_0 = e`, whole or per
// field), boundaryStatic, implSourced and sigRawParam (static loans in a
// result), ret (TermReturn) and emitE0411.
//
// How: each loan the returned value holds must be static, covered by a
// source of the signature (else E0602), not on a frame root (else E0603), or
// made through a deref. At return every parameter ends; a linear parameter
// still holding a value is E0402 except at an error exit, and an E0411 exit
// records the change sites of every protected parameter.

package borrow

import (
	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// boundary is the return boundary check: each loan the returned value holds
// must be static, covered by a source of the body's signature (else E0602),
// not on a frame root (else E0603), or made through a deref. Skipped when the
// signature carries the E0601 or E0607 mark.
func (c *checker) boundary(st *state, ctx *stmtCtx, _ setID) {
	if !c.report || c.env.Sig == nil || c.env.Sig.Flows.Ambiguous || c.env.Sig.Flows.ClauseError {
		return
	}
	if len(c.env.Sig.Flows.Fields) > 0 {
		c.boundaryFields(st, ctx)
		return
	}
	failed := false
	for _, l := range c.sets.get(c.held(st, 0)) {
		lo := &c.loans[l]
		switch {
		case lo.static:
			if c.boundaryStatic(ctx, loanID(l), "") {
				failed = true
			}
		case lo.abstract:
			if !c.covered(loanID(l)) {
				failed = true
				if name, ok := c.loanCapture(loanID(l)); ok {
					// No clause can name a capture, so its loan cannot leave
					// the literal.
					c.captureE0609(ctx.span, name, "result-loan", "the literal's result could hold a loan on `"+name+
						"`, which it captured; a capture is no parameter, so no `from` can name it")
					continue
				}
				c.errAt(diag.E0602, ctx.span, "the result holds a loan of %s, which the signature does not name as a source",
					c.paramName(lo.param)).Fact("loan", c.loanFact(loanID(l))).Text("signature", c.sigString(c.env.Sig))
			}
		case lo.target != noRoot && c.roots[lo.target].frame():
			failed = true
			c.errAt(diag.E0603, ctx.span, "the result borrows %s, which ends when the function returns",
				c.rootName(lo.target)).Fact("loan", c.loanFact(loanID(l)))
		}
	}
	if failed {
		c.boundaryFail = append(c.boundaryFail, ctx.span)
	}
}

// boundaryFields is the boundary check for a signature with a per-field
// clause: a split _0 (a literal, a forwarded per-field call, some or ok of
// one, a part taken from either) is checked field by field, each field's
// loans against that field's sources (a field the clause leaves out,
// against Flows.Sources); a whole _0 has every loan checked against the
// sources of every named field, since a field left out holds the union,
// which covers whatever each named field's sources do. A field of a type
// that holds no loans holds nothing to check. Static loans and loans made
// through a deref pass, a loan on a frame root is E0603, as in boundary.
func (c *checker) boundaryFields(st *state, ctx *stmtCtx) {
	fl := c.env.Sig.Flows
	type job struct {
		set   setID
		srcs  [][]types.Source
		field string
	}
	var jobs []job
	if fs := c.sets.fieldSets(st.get(0).fv); fs != nil {
		for i, s := range fs {
			srcs, named := c.fieldSources(fl, uint32(i))
			label := "field `" + c.shapeFieldName(c.body.Locals[0].Type, i) + "`"
			if c.roots[0].rest && i == len(fs)-1 {
				// A view's rest, which no clause names.
				label = "what the view keeps outside its fields, which holds every source"
			} else if !named {
				label += ", which the clause leaves out, so it holds every source"
			}
			jobs = append(jobs, job{s, [][]types.Source{srcs}, label})
		}
	} else {
		all := [][]types.Source{fl.Sources}
		for _, f := range fl.Fields {
			all = append(all, f.Sources)
		}
		jobs = append(jobs, job{c.held(st, 0), all, "every field"})
	}
	failed := false
	reported := map[loanID]bool{}
	for _, j := range jobs {
		for _, l := range c.sets.get(j.set) {
			lo := &c.loans[l]
			switch {
			case lo.static:
				if !reported[loanID(l)] && c.boundaryStatic(ctx, loanID(l), " of "+j.field) {
					failed = true
					reported[loanID(l)] = true
				}
			case lo.abstract:
				ok := true
				for _, srcs := range j.srcs {
					ok = ok && c.coveredBy(loanID(l), srcs)
				}
				if ok || reported[loanID(l)] {
					continue
				}
				failed = true
				reported[loanID(l)] = true
				if name, isCap := c.loanCapture(loanID(l)); isCap {
					c.captureE0609(ctx.span, name, "result-loan", "the literal's result could hold a loan on `"+name+
						"`, which it captured; a capture is no parameter, so no `from` can name it")
					continue
				}
				c.errAt(diag.E0602, ctx.span, "the result holds a loan of %s, which the signature does not name as a source of %s",
					c.paramName(lo.param), j.field).Fact("loan", c.loanFact(loanID(l))).Text("signature", c.sigString(c.env.Sig))
			case lo.target != noRoot && c.roots[lo.target].frame():
				if reported[loanID(l)] {
					continue
				}
				failed = true
				reported[loanID(l)] = true
				c.errAt(diag.E0603, ctx.span, "the result borrows %s, which ends when the function returns",
					c.rootName(lo.target)).Fact("loan", c.loanFact(loanID(l)))
			}
		}
	}
	if failed {
		c.boundaryFail = append(c.boundaryFail, ctx.span)
	}
}

// boundaryStatic is the static half of the boundary check: a result whose
// signature names a source may not hold the static loan of memory a store may
// land in (a global that takes stores, or the global node of a call that
// handed one back), since its callers judge a store through the result against
// the sources and would let a frame's loan into the global. It is E0602 at the
// return, and reports whether it fired. A result with no source may hold one:
// its callers see it (call.go step 5), unless a parameter holds a raw pointer:
// then its callers take the result as made from the pointer (resultGlobal), so
// it is refused as for a source; or unless the body is an impl method whose
// interface method names a source: a generic body calling through the
// interface judges a store through the result against that source, so it is
// refused too (an impl's `&U` or `[]U` result, a user Slice[T] impl's Items
// under Slice[T].Get). A static that takes no store, such as `&Limit`, passes
// wherever it is returned.
func (c *checker) boundaryStatic(ctx *stmtCtx, l loanID, field string) bool {
	fl := c.env.Sig.Flows
	raw := c.sigRawParam()
	iface := false
	if len(fl.Sources) == 0 && len(fl.Fields) == 0 && raw == types.NoParam {
		if !c.implSourced() {
			return false
		}
		iface = true
	}
	t := c.loans[l].target
	if t == noRoot {
		return false
	}
	ri := &c.roots[t]
	if t == c.global.root {
		// The global node stands for whatever the result's first level
		// refers to (resultGlobal): judged on the result's type, for every
		// instance in a generic body, since its callers judge a store
		// through the result against the sources at an instance this body
		// never sees (`Call[T](f: fn(): T): T` returning f(), or
		// `if T is X { return Get() }`).
		if !c.w.storesFirstGen(c.body.Locals[0].Type) {
			return false
		}
	} else if !ri.takes {
		return false // a named global that takes no store, such as `&Limit`
	}
	d := c.errAt(diag.E0602, ctx.span, "the result%s points into global memory (%s), which takes stores, but the signature names a source or takes a raw pointer, so a caller would judge a store through the result against that source",
		field, c.rootName(t)).Fact("loan", c.loanFact(l)).Text("signature", c.sigString(c.env.Sig))
	if iface {
		d.Text("interface", "the interface method this implements names a source, so a caller through the interface judges a store through the result against it")
	} else if len(fl.Sources) == 0 && len(fl.Fields) == 0 {
		// Name the parameter that counts as raw.
		d.Text("raw-pointer", "parameter `"+c.paramName(raw)+"` holds a raw pointer, so callers take the result as made from it")
	}
	d.Alt("return it from a function whose result names no source, or return a reference the sources supply")
	return true
}

// implSourced reports whether the body is an impl method whose interface
// method's result names a source (boundaryStatic). It reads the interface's
// signature and records the read. The impl's method is found by name, as check
// pairs them (implSignatures).
func (c *checker) implSourced() bool {
	if c.tab == nil || c.body == nil || c.body.Item == types.NoItem {
		return false
	}
	items := c.tab.Items()
	it := items.Get(c.body.Item)
	if it.Owner == types.NoItem {
		return false
	}
	impl, ok := items.Get(it.Owner).Decl.(*types.ImplDecl)
	if !ok || impl.Bound.Kind != types.BoundInterface || impl.Bound.Item == types.NoItem {
		return false
	}
	idecl, ok := items.Get(impl.Bound.Item).Decl.(*types.InterfaceDecl)
	if !ok {
		return false
	}
	c.readSig(impl.Bound.Item)
	for i := range idecl.Methods {
		if im := &idecl.Methods[i]; im.Name == it.Name {
			return namesSource(im.Flows.Sources, im.Flows.Fields)
		}
	}
	return false
}

// namesSource reports whether a clause names a source: a `from` list or a
// per-field clause. implSourced reads it off the interface method's
// signature and resultGlobal off the same signature at an interface call
// (Env.Method), so the call trusts an interface method's sources exactly
// when every impl's boundary is judged as a sourced one.
func namesSource(sources []types.Source, fields []types.FieldSources) bool {
	return len(sources) > 0 || len(fields) > 0
}

// sigRawParam is the first parameter of the body's signature that holds a
// raw pointer (Io does not count, holdsRaw), or NoParam.
func (c *checker) sigRawParam() types.ParamIndex {
	seen := map[types.TypeID]bool{}
	for i, p := range c.env.Sig.Params {
		if c.w.holdsRaw(p.Type, seen) {
			return types.ParamIndex(i + 1)
		}
	}
	return types.NoParam
}

// ret is TermReturn: every parameter ends; a linear parameter
// still holding a value is E0402 except at a fail or try exit, where
// ownership returns to the caller; at an E0411 exit every
// change site of a protected parameter is recorded.
func (c *checker) ret(st *state, ctx *stmtCtx, t *mir.Terminator) {
	if !c.report {
		return
	}
	exempt := t.Exit == mir.ExitFail || t.Exit == mir.ExitTry
	for p := 1; p <= int(c.body.ParamCount); p++ {
		r := rootID(p)
		if !c.roots[r].linear || exempt || initOf(st.get(r))&initI == 0 {
			continue
		}
		d := c.errAt(diag.E0402, c.body.Locals[p].Span, "%s is not consumed on this exit", c.rootName(r)).
			Text("place", c.rootName(r)).Loc("path", c.body.Span(ctx.span))
		c.releaseFacts(d, r, c.roots[r].typ)
	}
	if !t.Exit.IsE0411Exit() {
		return
	}
	exit := c.body.Span(ctx.span)
	for p := 1; p <= int(c.body.ParamCount); p++ {
		r := rootID(p)
		if !c.e0411Param(r) {
			continue
		}
		for _, s := range c.sets.get(st.get(r).changed) {
			if old, ok := c.e0411[s]; !ok || exit.Start < old.Start {
				c.e0411[s] = exit
			}
			c.e0411Root[s] = r
		}
	}
}

// emitE0411 reports each recorded change site once, with the first error
// exit by span reached from it.
func (c *checker) emitE0411() {
	var sites []uint32
	for s := range c.e0411 {
		sites = append(sites, s)
	}
	sortU32(sites)
	for _, s := range sites {
		sp := c.siteSpan(s)
		r := c.e0411Root[s]
		c.errAt(diag.E0411, sp, "%s is changed here, but the function can still fail afterward", c.rootName(r)).
			Loc("moved_at", c.body.Span(sp)).Loc("error_exit", c.e0411[s])
	}
}
