// Why: structured concurrency and contracts both lower to calls placed at
// fixed points of a block's exits: a scope joins its tasks on every exit, a
// contract's predicate runs on entry and on every success exit.
//
// What: scopeStmt, tryScopeResult and goExpr (scope s := e, try scope, and
// s.go as a spawned TermCall with no error edge); predicateCall,
// entryChecks, ensuresChecks and the invariant checks (requires, ensures,
// invariant).
//
// How: a scope's exit runs defers and onerrs, then cancel if the exit is
// early, then StJoin, then the scope's loans and locals end. Each contract
// clause is a call of its predicate item with shared references to the
// places it names, then a PanicContract when it returns false.

package lower

import (
	"mk/internal/ast"
	"mk/internal/check"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// scopeStmt is scope s := e { body } and try scope s := e { body }
// (tryScopeResult).
func (bl *builder) scopeStmt(s *ast.ScopeStmt) {
	call, ok := unparen(s.Value).(*ast.CallExpr)
	if !ok {
		// A Poison value (`scope a := io.Scope { }`, a method value with no
		// call, a check gap) is the tagged ICE; any other is lower's.
		bl.typeOf(s.Value)
		source.ICEf(s.Value.Span(), "lower: a scope's value is not a call")
	}
	c := bl.in.Call(call.ID())
	bd := bl.in.Binding(s.Name.ID())
	t := bd.Type
	if !bd.OK {
		t = bl.typeOf(call)
	}
	sl := bl.b.AddLocal(mir.Local{Type: t, Kind: mir.LocalScope, Name: s.Name.Name, Loop: mir.NoLoop, Span: bl.span(s.Name.Span())})
	bl.bindLocal(s.Name, sl)
	bl.callDest = sl
	bl.emitCall(call, c, nil, mir.NoLocal)
	bl.openScope(scopeStmtScope)
	sc := &bl.scopes[len(bl.scopes)-1]
	sc.scopeVar = sl
	sc.tryScope, sc.tryAt = s.Try, s.Span()
	sc.locals = append(sc.locals, sl)
	bl.blockStmts(s.Body)
	bl.closeScope(closeBrace(s.Body))
}

// tryScopeResult is the fall-through end of try scope over Scope[E]
// (the err field is read in place of a Finish call): after the block's cleanups
// and its StJoin, and before its scope ends, the statement's E!void value is
// the err field of the scope local, field 1 of Scope[E]'s layout
// {hdr: Scope, err: E!void} (check's Layout). An ok
// value continues to the scope ends; an error copies the error out, ends the
// block's temporaries and locals, then leaves as a try of the enclosing
// block: the enclosing scopes' chain with exit kind try, never the block's
// own cleanups or join again, ExitTry. Early exits never read err.
func (bl *builder) tryScopeResult(i int, at source.Span) {
	if !bl.open() {
		return
	}
	s := bl.scopes[i]
	st := bl.tab.Get(bl.b.Locals[s.scopeVar].Type)
	if st.Kind != types.KNamed || bl.cfg.Known == nil || st.Item != bl.cfg.Known.Scope || len(st.Args) != 1 ||
		bl.tab.Kind(st.Args[0].Type) != types.KErrorSet {
		source.ICEf(s.tryAt, "lower: try scope over %s, not a Scope[E] (E0804 is check's)", bl.tab.TypeString(bl.b.Locals[s.scopeVar].Type, bl.sig.Generics))
	}
	es := bl.tab.Get(st.Args[0].Type).Errors
	errP := bl.extend(bl.b.Place(s.scopeVar), mir.Projection{Kind: mir.ProjField, A: 1, Type: bl.tab.ErrorUnion(es, types.Void)})
	d := bl.discr(errP, s.tryAt)
	ok, fail := bl.newBlock(), bl.newBlock()
	bl.switchOn(d, []mir.SwitchCase{{Value: 0, Target: ok}}, fail, s.tryAt)
	bl.cur = fail
	et := bl.tab.ErrorSetType(es)
	if !bl.plain(et, s.tryAt) {
		// The runtime records a task's error by copying its bytes (the
		// runtime's Fail), so the err field is read by copy and the
		// scope local is never partially moved.
		source.ICEf(s.tryAt, "lower: try scope over a non-plain error set %s", bl.tab.TypeString(et, bl.sig.Generics))
	}
	e := bl.hiddenLocal(et, mir.LocalTemp, s.tryAt)
	bl.assign(bl.b.Place(e), use(bl.valueOf(bl.extend(errP, mir.Projection{Kind: mir.ProjDowncast, Sel: mir.SelErr, Type: et}), true, s.tryAt)), s.tryAt)
	bl.endScopeLocals(i, at)
	saved := bl.scopes
	// A copy: cleanup copies push scopes, which must not overwrite saved[i].
	// The error edge's temporaries are a frame of the enclosing scope, so
	// the chain ends them and the ok path never sees them.
	bl.scopes = append([]scope(nil), saved[:i]...)
	bl.pushFrame(s.tryAt)
	bl.temps[len(bl.temps)-1].temps = append(bl.temps[len(bl.temps)-1].temps, e)
	bl.errorExit(bl.valueOfTemp(e, s.tryAt), es, s.tryAt, mir.ExitTry, false)
	bl.dropFrame()
	bl.scopes = saved
	bl.cur = ok
}

// goExpr is s.go F(args): a spawned call, arguments evaluated in the parent
// with no reborrow of mut arguments, no error edge.
func (bl *builder) goExpr(x *ast.GoExpr) mir.Rvalue {
	call, ok := unparen(x.Call).(*ast.CallExpr)
	if !ok {
		source.ICEf(x.Span(), "lower: s.go of something that is not a call")
	}
	sp := bl.place(x.Scope)
	if sp.Proj.Len != 0 {
		l := bl.newTemp(bl.b.PlaceType(sp), x.Scope.Span())
		bl.assign(bl.b.Place(l), use(mir.Copy(sp)), x.Scope.Span())
		sp = bl.b.Place(l)
	}
	bl.emitCallAt(call, bl.in.Call(call.ID()), nil, sp.Local, x.Span())
	return bl.voidValue(x.Span())
}

// identityArgs forwards generics as themselves: KParam(i) for a type
// parameter, CAParam(i) for a const one (a requires clause on a function
// generic over a const N alone, such as the cookbook's SumN, gave its
// predicate a type argument for N, which mono refused as an ICE).
func (bl *builder) identityArgs(gs []types.GenericParam) []types.GenericArg {
	out := make([]types.GenericArg, len(gs))
	for i, g := range gs {
		if g.Const {
			out[i] = types.ConstGenericArg(types.ConstArg{Kind: types.CAParam, Param: uint32(i), Type: g.Type})
			continue
		}
		out[i] = types.TypeArg(bl.tab.Param(uint32(i)))
	}
	return out
}

// predicateCall calls predicate item with shared references to the
// places, then panics with PanicContract when it returns false, with the
// clause kind ("requires", "ensures" or "invariant") as the panic's str
// message, so the report reads "contract: requires" (panic code 14). The
// check is a temporary frame of its own.
func (bl *builder) predicateCall(item types.ItemID, places []mir.Place, gen []types.GenericArg, clause string, at source.Span) {
	if !bl.open() {
		return
	}
	bl.pushFrame(at)
	args := make([]mir.Arg, len(places))
	for i, p := range places {
		rt := bl.tab.Ref(bl.b.PlaceType(p))
		l := bl.newTemp(rt, at)
		bl.assign(bl.b.Place(l), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: p, Type: rt}, at)
		args[i] = mir.Arg{Operand: mir.Copy(bl.b.Place(l)), Mode: mir.ArgCopy}
	}
	k := bl.newTemp(types.Bool, at)
	next := bl.newBlock()
	bl.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: item, Args: bl.b.AddGenericArgs(gen...)}},
		Args: bl.b.AddArgs(args...), Dest: bl.b.Place(k), Target: next, Spawn: mir.NoLocal}, at)
	bl.cur = next
	ok := bl.newBlock()
	msg := bl.strConst([]byte(clause), at)
	bl.boolSwitch(mir.Copy(bl.b.Place(k)), ok, bl.panicBlock(types.PanicContract, &msg, at), at)
	bl.cur = ok
	bl.popFrame()
}

// namedParams are the function's parameter places a predicate names, in
// the predicate's parameter order, and whether it takes result.
func (bl *builder) namedParams(pred types.ItemID) ([]mir.Place, bool) {
	ps := bl.cfg.callee(pred)
	if ps == nil {
		return nil, false
	}
	var out []mir.Place
	result := false
	for _, p := range ps.Params {
		if p.Name == source.SymResult {
			result = true
			continue
		}
		if i := bl.sig.ParamByName(p.Name); i != types.NoParam {
			out = append(out, bl.b.Place(mir.LocalID(i)))
		}
	}
	return out, result
}

// entryChecks are the requires clauses at function entry, in clause order.
func (bl *builder) entryChecks() {
	if bl.kind != BodyFunc && bl.kind != BodyLiteral {
		return
	}
	for _, c := range bl.sig.Contracts {
		if c.Kind != types.Requires {
			continue
		}
		places, _ := bl.namedParams(c.Body)
		bl.predicateCall(c.Body, places, bl.identityArgs(bl.sig.Generics), "requires", c.Span)
	}
}

// ensuresChecks are the ensures clauses on a success exit, before _0 is
// assigned, on the place holding the success value.
func (bl *builder) ensuresChecks(val *mir.Operand, at source.Span) {
	if bl.kind != BodyFunc && bl.kind != BodyLiteral {
		return
	}
	var vp *mir.Place
	valuePlace := func() {
		if vp != nil || val == nil {
			return
		}
		var p mir.Place
		if val.Kind == mir.OpCopy || val.Kind == mir.OpMove {
			p = val.Place
		} else {
			l := bl.newTemp(bl.b.OperandType(*val), at)
			bl.assign(bl.b.Place(l), use(*val), at)
			p = bl.b.Place(l)
		}
		vp = &p
	}
	// A void success value (a void or E!void result) is still the
	// predicate's result: &void argument (every ensures predicate takes
	// one), a void temporary that holds nothing.
	var voidP *mir.Place
	for _, c := range bl.sig.Contracts {
		if c.Kind != types.Ensures {
			continue
		}
		places, result := bl.namedParams(c.Body)
		switch {
		case result && val != nil:
			valuePlace()
			places = append(places, *vp)
		case result:
			if voidP == nil {
				l := bl.newTemp(types.Void, at)
				c := bl.cfg.Consts.Intern(types.ConstValue{Type: types.Void, Align: 1})
				bl.assign(bl.b.Place(l), use(mir.Const(types.Void, c)), at)
				p := bl.b.Place(l)
				voidP = &p
			}
			places = append(places, *voidP)
		}
		bl.predicateCall(c.Body, places, bl.identityArgs(bl.sig.Generics), "ensures", at)
	}
	if val != nil {
		if d, _ := bl.structInvariants(bl.b.OperandType(*val)); d != nil {
			valuePlace()
			bl.returningInvariant(vp, at)
		}
	}
	bl.mutSelfInvariant(at)
}

// invariantCheck calls each invariant of struct type t on the value at p,
// after its construction.
func (bl *builder) invariantCheck(t types.TypeID, p mir.Place, at source.Span) {
	ty := bl.tab.Get(t)
	if ty.Kind != types.KNamed {
		return
	}
	d, ok := bl.cfg.Items.Get(ty.Item).Decl.(*types.StructDecl)
	if !ok || len(d.Invariants) == 0 {
		return
	}
	// The invariant item's own body does not check its own construction.
	if bl.kind == BodyInvariant {
		return
	}
	for _, inv := range d.Invariants {
		bl.predicateCall(inv, []mir.Place{p}, ty.Args, "invariant", at)
	}
}

// structInvariants is t's struct declaration and its generic arguments when
// t is a struct with invariants declared in the unit's module (the
// returning and mut self checks are a same-module function's), resolving
// Self to the method's owner with its identity arguments.
func (bl *builder) structInvariants(t types.TypeID) (*types.StructDecl, []types.GenericArg) {
	if bl.kind != BodyFunc {
		return nil, nil
	}
	// check.InvariantStruct is the one predicate: check refuses a become
	// exactly where this emits a returning or mut self check (E0312).
	item := check.InvariantStruct(bl.cfg.Items, bl.tab, bl.in.Item(), t)
	if item == types.NoItem {
		return nil, nil
	}
	d := bl.cfg.Items.Get(item).Decl.(*types.StructDecl)
	ty := bl.tab.Get(t)
	if ty.Kind == types.KSelf {
		return d, bl.identityArgs(d.Generics)
	}
	return d, ty.Args
}

// returningInvariant is the invariant check on the success value of a same-module
// function returning a struct with invariants, before _0 is assigned (with
// the ensures checks, on the same place).
func (bl *builder) returningInvariant(vp *mir.Place, at source.Span) {
	if vp == nil {
		return
	}
	d, args := bl.structInvariants(bl.b.PlaceType(*vp))
	if d == nil {
		return
	}
	for _, inv := range d.Invariants {
		bl.predicateCall(inv, []mir.Place{*vp}, args, "invariant", at)
	}
}

// mutSelfInvariant is the invariant check before every exit of a same-module
// method taking mut self of a struct with invariants: I(&*self).
func (bl *builder) mutSelfInvariant(at source.Span) {
	if bl.sig.Receiver != types.ByMut || len(bl.sig.Params) == 0 {
		return
	}
	st := bl.tab.Get(bl.sig.Params[0].Type)
	if st.Kind != types.KMut {
		return
	}
	d, args := bl.structInvariants(st.Elem)
	if d == nil {
		return
	}
	self := bl.deref(bl.b.Place(1), at)
	for _, inv := range d.Invariants {
		bl.predicateCall(inv, []mir.Place{self}, args, "invariant", at)
	}
}
