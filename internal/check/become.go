// Why: `become f(args)` replaces the caller's frame with the callee's, so
// the call is legal only when nothing in the frame can outlive it and the
// arguments can move without a copy the target ABI cannot make in place.
//
// What: becomeStmt and its helpers: E0312 (signature), E0406 and E0806
// (pending cleanup), E0313 (an argument passed in memory) and E0314 (an
// extern, intrinsic, wider-cpu, interface-bound or naked target).
// InvariantStruct, shared with lower.
//
// How: an argument passes if every built target passes its type in
// registers (registerKind), or it is the caller's own parameter at the same
// position passed on unchanged. A live linear value (E0402) and a loan on a
// local (E0603) are borrow's checks, at the TermTailCall.

package check

import (
	"slices"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// becomeStmt checks `become f(args)`. It always diverges.
func (u *unit) becomeStmt(s *ast.BecomeStmt) {
	if u.inDefer() {
		u.cleanupExit(s.Span(), "become")
	}
	call := s.Call
	n0 := len(u.b.Diags)
	got := u.expr(call, types.NoType)
	if got == types.Poison || u.sig == nil {
		if u.sig == nil {
			u.err(diag.E0314, s.Span(), "`become` leaves a function: here there is none").Text("reason", "no-function").
				Alt("`return` the call")
		}
		return
	}
	fd, _ := u.it.Decl.(*types.FuncDecl)
	if fd != nil && fd.Naked {
		u.tailTarget(s, "naked", "a `naked` function has no frame to replace")
		return
	}
	use := u.b.Uses
	var cu Use
	if use != nil {
		cu = use[u.b.slot(call.ID())]
	}
	var calleeFT types.TypeID
	var callee *types.Signature
	switch cu.Kind {
	case UseItem, UseMethod:
		if cu.Item == types.NoItem {
			// A call of a const fn parameter (UseItem with its index):
			// the caller has a const parameter, which no tail call keeps.
			u.tailSig(s, "the caller's `const` parameters are fixed per instance; a tail call keeps none")
			return
		}
		it := u.c.cfg.Items.Get(cu.Item)
		d, ok := u.c.declOfItem(it).(*types.FuncDecl)
		if !ok {
			u.tailTarget(s, "not-a-function", "`become` calls a Mk function or a `fn` value")
			return
		}
		switch {
		case d.Sig.Extern:
			why := "is an `extern` function, defined outside Mk"
			if !d.Bodiless {
				why = "is `export extern`: rule 5 refuses every `extern` target, bodied or not"
			}
			u.tailTarget(s, "extern", "`"+u.c.cfg.Interner.Str(it.Name)+"` "+why)
			return
		case d.Intrinsic || d.Bodiless || u.isFormatter(cu.Item):
			u.tailTarget(s, "intrinsic", "`"+u.c.cfg.Interner.Str(it.Name)+"` is implemented by the compiler")
			return
		case !cpuWithin(d.Sig.CPU, u.sig.CPU):
			u.tailTarget(s, "cpu", "`"+u.c.cfg.Interner.Str(it.Name)+"` needs cpu features the caller's clause lacks")
			return
		}
		callee = &d.Sig
		var args []types.GenericArg
		if cu.Inst != 0 && int(cu.Inst) < len(u.b.Generic) {
			args = u.b.Generic[cu.Inst]
		}
		calleeFT = u.c.subst(d.Sig.FnType(u.tab), args, types.NoType)
		if len(args) > 0 {
			// Flows the type arguments make vacuous go, as a written
			// `from x` on a non-carrier has none: G[i32]'s automatic
			// `from x` is no flow of fn(i32): i32.
			calleeFT = u.instFlows(calleeFT)
		}
	case UseIfaceMethod:
		u.tailTarget(s, "bound", "a method through an interface bound is chosen per instance")
		return
	case UseFnValueCall:
		calleeFT = u.b.TypeOf(call.Fun.ID())
		if u.tab.Kind(calleeFT) != types.KFn {
			return
		}
	default:
		u.tailTarget(s, "not-a-function", "`become` calls a Mk function or a `fn` value")
		return
	}
	if u.capBase != 0 {
		u.tailSig(s, "a capturing literal's frame carries its environment")
		return
	}
	for _, c := range u.sig.Contracts {
		if c.Kind == types.Ensures {
			u.tailSig(s, "the caller's `ensures` clause runs after its callee returns")
			return
		}
	}
	if u.mutSelfInvariants() {
		u.tailSig(s, "a `mut self` method checks its struct's invariants when it returns")
		return
	}
	if u.it.Kind != types.KindLiteral && InvariantStruct(u.c.cfg.Items, u.tab, u.it.ID, u.sig.Result) != types.NoItem {
		u.tailSig(s, "the result is a `"+u.str(u.sig.Result)+"`, whose invariants are checked when this function returns")
		return
	}
	for _, sig := range []*types.Signature{u.sig, callee} {
		if sig == nil {
			continue
		}
		for _, p := range sig.Params {
			if p.Const {
				u.tailSig(s, "a `const` parameter is fixed per instance; a tail call keeps none")
				return
			}
		}
	}
	own := u.sig.FnType(u.tab)
	if calleeFT != own {
		if cf, of := u.tab.Get(calleeFT), u.tab.Get(own); cf.Kind == types.KFn && of.Kind == types.KFn && types.SameFnShape(cf.Fn, of.Fn) {
			vf, vi := types.FnFlows(cf.Fn)
			tf, ti := types.FnFlows(of.Fn)
			cs, os := u.c.fnFlowsString(vf, vi, cf.Fn.Result), u.c.fnFlowsString(tf, ti, of.Fn.Result)
			u.err(diag.E0312, call.Span(), "the callee's flows are `"+cs+"`, the calling function's `"+os+"`: a tail call needs one signature").
				Text("callee", u.str(calleeFT)+" (flows: "+cs+")").Text("caller", u.str(own)+" (flows: "+os+")").
				Alt("`return` the call")
			return
		}
		u.err(diag.E0312, call.Span(), "the callee is `"+u.str(calleeFT)+"`, the calling function `"+u.str(own)+"`: a tail call needs one signature").
			Text("callee", u.str(calleeFT)).Text("caller", u.str(own)).
			Alt("`return` the call")
		return
	}
	if u.pendingCleanup(s) {
		return
	}
	u.tailArgs(call, cu, len(u.b.Diags) > n0)
}

// tailTarget is E0314 tail-call-target with its reason.
func (u *unit) tailTarget(s *ast.BecomeStmt, reason, msg string) {
	u.err(diag.E0314, s.Call.Span(), "`become` cannot call this: "+msg).Text("reason", reason).
		Alt("`return` the call")
}

// tailSig is E0312 tail-call-signature for a frame the callee's signature
// cannot replace.
func (u *unit) tailSig(s *ast.BecomeStmt, msg string) {
	u.err(diag.E0312, s.Call.Span(), "the tail call cannot replace this frame: "+msg).Text("reason", "frame").
		Alt("`return` the call")
}

// cpuWithin reports whether every feature of need is in have (both sorted
// closures, types.Signature.CPU).
func cpuWithin(need, have []string) bool {
	for _, f := range need {
		if !slices.Contains(have, f) {
			return false
		}
	}
	return true
}

// pendingCleanup is E0406 when a `defer`, an `onerr` or a `scope` of the
// function encloses s, or precedes it in an enclosing block: it would run
// after the callee, which a tail call leaves nothing to run after.
func (u *unit) pendingCleanup(s *ast.BecomeStmt) bool {
	body := u.b.Node
	var path []ast.Node
	found := false
	var walk func(n ast.Node) bool
	walk = func(n ast.Node) bool {
		if n == nil || found {
			return false
		}
		if _, lit := n.(*ast.FnLit); lit && n != body {
			return false
		}
		path = append(path, n)
		if n == ast.Node(s) {
			found = true
			return false
		}
		ast.EachChild(n, func(c ast.Node) {
			if !found {
				walk(c)
			}
		})
		if !found {
			path = path[:len(path)-1]
		}
		return false
	}
	walk(body)
	if !found {
		return false
	}
	for i, n := range path {
		switch n := n.(type) {
		case *ast.ScopeStmt:
			u.err(diag.E0406, s.Span(), "the scope `"+u.c.cfg.Interner.Str(n.Name.Name)+"` joins its tasks after this `become`").
				Text("reason", "scope").Alt("`return` the call")
			return true
		case *ast.Block:
			if i+1 >= len(path) {
				continue
			}
			next := path[i+1]
			for _, st := range n.Stmts {
				if ast.Node(st) == next {
					break
				}
				if d, ok := st.(*ast.DeferStmt); ok {
					word := "defer"
					if d.Onerr {
						word = "onerr"
					}
					u.err(diag.E0406, s.Span(), "the `"+word+"` before this `become` would run after its callee").
						Text("reason", word).Alt("`return` the call")
					return true
				}
			}
		}
	}
	return false
}

// tailArgs is E0313 tail-call-argument: each argument's type is
// passed in registers on every target, or the argument is the caller's own
// parameter of its position passed on unchanged (not `var`), whose memory
// the caller's caller owns and whose incoming pointer codegen forwards. At
// another position the memory would move between argument slots, which
// System V's byval copies cannot do in place. The register-passed kinds are
// integers, floats, bool, char, references, raw pointers, fn values, and
// plain structs and records of 1, 2, 4 or 8 bytes (Win64 passes any other
// size through memory the caller makes). reported says the call itself had
// an error.
func (u *unit) tailArgs(call *ast.CallExpr, cu Use, reported bool) {
	var args []ast.Expr
	if cu.Kind == UseMethod {
		fun := call.Fun
		if ix, ok := fun.(*ast.IndexExpr); ok {
			fun = ix.X // self.G[U](..): the type arguments wrap the dot
		}
		if fe, ok := fun.(*ast.FieldExpr); ok && u.b.Uses[u.b.slot(fe.ID())].Kind == UseMethod {
			args = append(args, fe.X) // a dot call's receiver is Params[0]
		}
	}
	for _, a := range call.Args {
		if a == nil || a.Value == nil {
			return // a parse error, reported
		}
		args = append(args, a.Value)
	}
	if len(args) != len(u.sig.Params) {
		if reported {
			return // the call's own error (E0304 arity) says it
		}
		// E0312 made the callee's parameters the caller's, and the call
		// checked clean: one argument per parameter, receiver first.
		source.ICEf(call.Span(), "check: a tail call with %d arguments for %d parameters", len(args), len(u.sig.Params))
	}
	for i, a := range args {
		t := u.sig.Params[i].Type
		if u.registerKind(t) {
			continue
		}
		if p, ok := u.ownParam(a); ok && int(p) == i+1 {
			continue
		}
		name := u.c.cfg.Interner.Str(u.sig.Params[i].Name)
		if !u.inMemory(t) {
			// A register-sized value of a kind the register list does not
			// name (a niche optional, an enum): the list, not memory.
			u.err(diag.E0313, a.Span(), "the argument for `"+name+"` is a `"+u.str(t)+"`, which is not a kind every target passes in registers").
				Text("param", name).Text("type", u.str(t)).
				Alt("pass the caller's own `" + name + "` unchanged, or a scalar, reference, `fn` value or plain struct of 1, 2, 4 or 8 bytes")
			continue
		}
		u.err(diag.E0313, a.Span(), "the argument for `"+name+"` is a `"+u.str(t)+"`, which some target passes in memory this frame owns").
			Text("param", name).Text("type", u.str(t)).
			Alt("pass a reference: `&" + u.str(t) + "`")
	}
}

// ownParam reports whether e names one of the caller's own parameters that
// the body cannot have changed (not `var`), and which.
func (u *unit) ownParam(e ast.Expr) (uint32, bool) {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			break
		}
		e = p.X
	}
	id, ok := e.(*ast.Ident)
	if !ok {
		return 0, false
	}
	r := u.ref(id)
	if r.Kind != resolve.RefParam || types.ItemID(r.B) != u.it.ID || r.A < 1 || int(r.A) > len(u.sig.Params) {
		return 0, false
	}
	if u.sig.Params[r.A-1].Var || u.b.Adjust != nil && u.b.Adjust[u.b.slot(id.ID())].Kind != AdjNone {
		return 0, false
	}
	return r.A, true
}

// registerKind reports whether a value of t is passed in registers on
// every target: a scalar kind, or a plain struct or record of 0,
// 1, 2, 4 or 8 bytes that every built target's C ABI classifier passes
// Direct (System V passes one with an unaligned field, a packed struct's,
// in memory; Config.ParamClass).
func (u *unit) registerKind(t types.TypeID) bool {
	ty := u.tab.Get(t)
	switch ty.Kind {
	case types.KInt, types.KIntPtr, types.KFloat, types.KBool, types.KChar,
		types.KRef, types.KMut, types.KRawPtr, types.KFn:
		return true
	case types.KNamed, types.KRecord:
		if ty.Kind == types.KNamed {
			if _, ok := u.c.declOfItem(u.c.cfg.Items.Get(ty.Item)).(*types.StructDecl); !ok {
				return false
			}
		}
		if !u.c.closed(t) || u.c.FactsIn(u.env, t).Kind != types.Plain {
			return false
		}
		switch u.c.Layout(t).Shape.Size {
		case 0:
			return true // nothing travels
		case 1, 2, 4, 8:
			return u.c.directOnBuilt(t)
		}
	}
	return false
}

// inMemory reports whether a value of t, which registerKind refused, is
// passed in memory on some built target: a type parameter (any size), or
// a closed type not of 0, 1, 2, 4 or 8 bytes or that a classifier passes
// Indirect. Otherwise it is register-sized but of a kind the register list
// does not name, and E0313 says so rather than "memory".
func (u *unit) inMemory(t types.TypeID) bool {
	if !u.c.closed(t) {
		return true
	}
	switch u.c.Layout(t).Shape.Size {
	case 0, 1, 2, 4, 8:
		return !u.c.directOnBuilt(t)
	}
	return true
}

// directOnBuilt reports whether every built target's classifier passes a
// parameter of closed type t Direct (or ignores it). With no classifier
// (a test's Config) it answers true, the size rule's verdict.
func (c *Checker) directOnBuilt(t types.TypeID) bool {
	if c.cfg.ParamClass == nil {
		return true
	}
	layout := func(t types.TypeID) *types.Layout {
		l := c.Layout(t)
		return &l
	}
	for _, tg := range types.AllTargets() {
		if !tg.Built {
			continue
		}
		switch c.cfg.ParamClass(c.cfg.Types, layout, tg, t).Kind {
		case types.AbiDirect, types.AbiIgnore:
		default:
			return false
		}
	}
	return true
}

// mutSelfInvariants reports whether the caller is a `mut self` method of a
// struct with invariants, which lower checks before every exit: a
// tail call would leave without them.
func (u *unit) mutSelfInvariants() bool {
	if u.sig.Receiver != types.ByMut || len(u.sig.Params) == 0 {
		return false
	}
	st := u.tab.Get(u.sig.Params[0].Type)
	if st.Kind != types.KMut {
		return false
	}
	return InvariantStruct(u.c.cfg.Items, u.tab, u.it.ID, st.Elem) != types.NoItem
}

// InvariantStruct is the struct whose invariants function fn checks on a
// value of type t when it returns, or NoItem: t is a struct with
// invariants declared in fn's module, or Self in a method of one. Lower
// emits the returning check on the success value, and the mut self check,
// exactly when this answers a struct (lower/scope.go structInvariants, for
// a function's own body, not a literal's); check refuses a `become` there
// (E0312, reason frame), since the tail call would leave without it.
func InvariantStruct(items *types.Items, tab *types.Table, fn types.ItemID, t types.TypeID) types.ItemID {
	ty := tab.Get(t)
	var item types.ItemID
	switch ty.Kind {
	case types.KNamed:
		item = ty.Item
	case types.KSelf:
		item = items.Get(fn).Owner
		if item == types.NoItem {
			return types.NoItem
		}
	default:
		return types.NoItem
	}
	d, ok := items.Get(item).Decl.(*types.StructDecl)
	if !ok || len(d.Invariants) == 0 || items.Get(item).Module != items.Get(fn).Module {
		return types.NoItem
	}
	return item
}
