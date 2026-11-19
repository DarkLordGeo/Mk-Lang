// Why: a generic call rarely writes its type arguments in brackets; they are
// inferred from the arguments and the expected type.
//
// What: infer, the state of one call, construction or generic fn value, with
// arg, argOrLater and lateArgs, unify and unifyConst, poison, finish and
// final.
//
// How: each generic parameter not written in brackets is a variable;
// arguments are unified structurally left to right, untyped literals last,
// then the expected type. A variable left unbound is E0711; bounds are
// judged after inference, and the instance is recorded in Body.Generic.

package check

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// infer is the inference state of one call, construction or generic fn
// value: one slot per generic parameter of the callee (owner's first). An
// unbound type slot holds Table.Infer(i); an unbound const slot holds the
// placeholder ConstArg{Kind: CANone, Param: i+1}. Neither ever leaves the
// call: final substitutes Poison for what is still unbound.
type infer struct {
	u     *unit
	gens  []types.GenericParam
	binds []types.GenericArg
	bound []bool
	// checked marks slots whose bounds were already checked where they were
	// written (a type expression's arguments), so finish skips them.
	checked []bool
	// poisoned marks slots an argument of Poison type met while unbound: if
	// nothing else binds them, they become Poison with no E0711 (the
	// argument's own error is the one diagnostic).
	poisoned []bool
	// badExplicit is set when an argument written in brackets for a type
	// parameter is Poison (its error reported, explicitArgs): finish's ok is
	// then false, so the call or fn value is Poison, never a type holding
	// Poison, which the MIR verifier's rule M11 rejects (FuzzCheck
	// 77640a14944bb5b4).
	badExplicit bool
	self        types.TypeID
	// later are untyped-literal arguments whose parameter type was open when
	// reached; they are checked after the call's expected type (lateArgs).
	later []lateArg
	// pending are const arguments written in brackets whose generic's type
	// was open when read (explicitArgs, judgePending).
	pending []pendingConst
	// fmtSlot is 1 + the slot of a Print-shaped callee's record parameter
	// whose Fields[Format] bound the call's format text judges instead,
	// 0 for none; with fmtSkip the bound is not judged at all (the
	// enclosing function's f and args passed on, judged at its callers).
	// fmtFailed is set when finish refused that slot's record.
	fmtSlot   int
	fmtSkip   bool
	fmtFailed bool
}

// lateArg is one deferred argument.
type lateArg struct {
	e ast.Expr
	p types.TypeID
}

func (u *unit) newInfer(gens []types.GenericParam, self types.TypeID) *infer {
	in := &infer{u: u, gens: gens, self: self, binds: make([]types.GenericArg, len(gens)), bound: make([]bool, len(gens))}
	for i, g := range gens {
		if g.Const {
			in.binds[i] = types.ConstGenericArg(types.ConstArg{Kind: types.CANone, Param: uint32(i) + 1, Type: g.Type})
		} else {
			in.binds[i] = types.TypeArg(u.tab.Infer(uint32(i)))
		}
	}
	return in
}

// set binds slot i (an argument written in brackets, an owner argument
// read from a type head, or a unification).
func (in *infer) set(i int, a types.GenericArg) {
	if i < len(in.binds) {
		in.binds[i], in.bound[i] = a, true
	}
}

// preset binds the slots of known arguments; a slot whose argument is the
// zero GenericArg stays a variable.
func (in *infer) preset(from int, args []types.GenericArg) {
	for k, a := range args {
		if a.IsConst || a.Type != types.NoType {
			in.set(from+k, a)
		}
	}
}

// firstUnbound is the first slot nothing bound yet, or -1.
func (in *infer) firstUnbound() int {
	for i, b := range in.bound {
		if !b {
			return i
		}
	}
	return -1
}

// poisonUnbound marks every unbound slot poisoned, so finish reports no
// E0711 for it: a diagnostic already explains the call (another E0711, or
// E0304 for a wrong bracket count).
func (in *infer) poisonUnbound() {
	if len(in.poisoned) < len(in.bound) {
		in.poisoned = append(in.poisoned, make([]bool, len(in.bound)-len(in.poisoned))...)
	}
	for i, b := range in.bound {
		if !b {
			in.poisoned[i] = true
		}
	}
}

// at is callee type p with the bindings so far; variables stay. Error-set
// parameters still unbound are dropped from the sets (lenient), which is
// why unify does not compare error sets.
func (in *infer) at(p types.TypeID) types.TypeID {
	s := substituter{c: in.u.c, args: in.binds, self: in.self, lenient: true}
	return s.typ(p)
}

// open reports whether t still mentions a variable of this call.
func (in *infer) open(t types.TypeID) bool {
	for i := range in.bound {
		if !in.bound[i] {
			return in.u.c.hasHole(t)
		}
	}
	return false
}

// argOrLater is arg, except that an untyped literal (an integer or float
// literal, negated or not, `none`, `[]`) for a parameter type still open is
// deferred until the call's expected type has been unified (lateArgs), so
// that `var c: Cell[i32] = Cell.New(0)` binds T to i32 as the literal adapts.
func (in *infer) argOrLater(e ast.Expr, p types.TypeID) {
	if in.open(in.at(p)) && in.u.untypedLit(e) {
		in.later = append(in.later, lateArg{e, p})
		return
	}
	in.arg(e, p)
}

// lateArgs checks the deferred arguments, in order.
func (in *infer) lateArgs() {
	for _, l := range in.later {
		if in.open(in.at(l.p)) && !in.u.numericLit(l.e) {
			// `none` or `[]` with nothing to bind T: the callee's E0711 is
			// the one diagnostic, so the literal adds none.
			in.u.expr(l.e, types.Poison)
			continue
		}
		in.arg(l.e, l.p)
	}
	in.later = nil
}

// numericLit reports whether e is an integer or float literal, negated or not.
func (u *unit) numericLit(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return u.numericLit(e.X)
	case *ast.BasicLit:
		return e.Kind == ast.TokInt || e.Kind == ast.TokFloat
	case *ast.UnaryExpr:
		return e.Op == ast.TokMinus && u.numericLit(e.X)
	}
	return false
}

// untypedLit reports whether e is a literal whose type comes from context.
func (u *unit) untypedLit(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return u.untypedLit(e.X)
	case *ast.BasicLit:
		return e.Kind == ast.TokInt || e.Kind == ast.TokFloat
	case *ast.UnaryExpr:
		return e.Op == ast.TokMinus && u.untypedLit(e.X)
	case *ast.Ident:
		r := u.ref(e)
		return r.Kind == resolve.RefUniverse && source.Symbol(r.A) == source.SymNone
	case *ast.ArrayLit:
		return len(e.Elems) == 0
	}
	return false
}

// arg checks argument e against callee parameter type p: with the
// substituted type as the expectation when it is closed, else on its own
// and unified. A failed unification is E0301 at the argument.
func (in *infer) arg(e ast.Expr, p types.TypeID) types.TypeID {
	u := in.u
	w := in.at(p)
	var t types.TypeID
	if !in.open(w) {
		t = u.expr(e, w)
	} else {
		t = u.expr(e, types.NoType)
		if !in.unify(w, t) {
			u.mismatchExpr(e, w, t)
		} else if wt, tt := u.tab.Get(w), u.tab.Get(t); wt.Kind == types.KFn && tt.Kind == types.KFn {
			// Flows are compared before substitution: the declared
			// type's flows, which binding its parameters does not change.
			u.fnFlowsCovered(e, w, t)
		} else {
			in.errorSetsAfterUnify(e, in.at(p), t)
		}
	}
	u.scopeEscapes(e, t) // E1003: a Scope passed or stored by value
	return t
}

// errorSetsAfterUnify compares the error sets unify skipped: an argument of
// type E!T or an error set, met by a parameter that the unification closed to
// E'!T or a set E', widens when E is within E' (AdjWiden) and is E0301
// otherwise. A parameter set that names an error-set parameter is still open
// and is not judged.
func (in *infer) errorSetsAfterUnify(e ast.Expr, w, t types.TypeID) {
	u := in.u
	if w == t || in.open(w) || !u.closedSets(w) {
		return
	}
	wt, tt := u.tab.Get(w), u.tab.Get(t)
	switch {
	case wt.Kind == types.KErrorUnion && tt.Kind == types.KErrorUnion && wt.Elem == tt.Elem:
	case wt.Kind == types.KErrorSet && tt.Kind == types.KErrorSet:
	default:
		return
	}
	if u.widens(w, t) {
		*u.b.adjust(e.ID()) = Adjust{Kind: AdjWiden, To: w}
		return
	}
	u.mismatch(e.Span(), w, t)
}

// closedSets reports whether the outer error set of t (an E!T or an error
// set) names no error-set parameter.
func (u *unit) closedSets(t types.TypeID) bool {
	ty := u.tab.Get(t)
	if ty.Kind != types.KErrorUnion && ty.Kind != types.KErrorSet {
		return false
	}
	for _, m := range u.tab.Members(ty.Errors) {
		if m.IsParam {
			return false
		}
	}
	return true
}

// unify binds the variables of w (a callee type with bindings applied)
// structurally against t, a type of the caller that has none: Named with
// equal items unify their arguments, references and other wrappers their
// elements, fn types position by position. Poison and never unify with
// anything. Error sets are not compared (error-set widening decides them).
func (in *infer) unify(w, t types.TypeID) bool {
	if t == types.Poison {
		in.poison(w)
		return true
	}
	if w == t || w == types.Poison || t == types.Never || t == types.NoType {
		return true
	}
	tab := in.u.tab
	wt, tt := tab.Get(w), tab.Get(t)
	if wt.Kind == types.KInfer {
		i := int(wt.Index)
		if i < len(in.binds) && !in.bound[i] {
			in.set(i, types.TypeArg(t))
			return true
		}
		return i < len(in.binds) && in.binds[i].Type == t
	}
	if wt.Kind != tt.Kind {
		return false
	}
	switch wt.Kind {
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice, types.KRawPtr, types.KOptional:
		return in.unify(wt.Elem, tt.Elem)
	case types.KArray:
		return in.unifyConst(wt.Len, tt.Len) && in.unify(wt.Elem, tt.Elem)
	case types.KErrorUnion:
		return in.unify(wt.Elem, tt.Elem)
	case types.KErrorSet:
		return true
	case types.KRecord:
		if len(wt.Fields) != len(tt.Fields) {
			return false
		}
		for i, f := range wt.Fields {
			if f.Name != tt.Fields[i].Name || !in.unify(f.Type, tt.Fields[i].Type) {
				return false
			}
		}
		return true
	case types.KNamed:
		if wt.Item != tt.Item || len(wt.Args) != len(tt.Args) {
			return false
		}
		for i, a := range wt.Args {
			b := tt.Args[i]
			switch {
			case a.IsConst != b.IsConst:
				return false
			case a.IsConst:
				if !in.unifyConst(a.Const, b.Const) {
					return false
				}
			case !in.unify(a.Type, b.Type):
				return false
			}
		}
		return true
	case types.KFn:
		if len(wt.Fn.Params) != len(tt.Fn.Params) {
			return false
		}
		for i, p := range wt.Fn.Params {
			// A parameter's into is a flow, compared after unification
			// (arg's fnFlowsCovered), not a shape.
			if !in.unify(p.Type, tt.Fn.Params[i].Type) {
				return false
			}
		}
		return in.unify(wt.Fn.Result, tt.Fn.Result)
	}
	return false
}

// poison marks every slot w mentions, at any depth, as met by a Poison
// argument, so finish reports no E0711 for it: `Use(&s)` with `s` poisoned
// meets `&T` with Poison, and T has no other source.
func (in *infer) poison(w types.TypeID) {
	mark := func(i int) {
		if i < 0 || i >= len(in.binds) {
			return
		}
		if in.poisoned == nil {
			in.poisoned = make([]bool, len(in.binds))
		}
		in.poisoned[i] = true
	}
	markConst := func(c types.ConstArg) {
		if c.Kind == types.CANone && c.Param > 0 {
			mark(int(c.Param) - 1)
		}
	}
	tab := in.u.tab
	ty := tab.Get(w)
	switch ty.Kind {
	case types.KInfer:
		mark(int(ty.Index))
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice, types.KRawPtr, types.KOptional, types.KErrorUnion:
		in.poison(ty.Elem)
	case types.KArray:
		markConst(ty.Len)
		in.poison(ty.Elem)
	case types.KRecord:
		for _, f := range ty.Fields {
			in.poison(f.Type)
		}
	case types.KNamed:
		for _, a := range ty.Args {
			if a.IsConst {
				markConst(a.Const)
			} else {
				in.poison(a.Type)
			}
		}
	case types.KFn:
		for _, p := range ty.Fn.Params {
			in.poison(p.Type)
		}
		in.poison(ty.Fn.Result)
	}
}

func (in *infer) unifyConst(w, t types.ConstArg) bool {
	if w.Kind == types.CANone && w.Param > 0 {
		i := int(w.Param) - 1
		if i < len(in.binds) && !in.bound[i] {
			in.set(i, types.ConstGenericArg(t))
			return true
		}
		return i < len(in.binds) && in.binds[i].IsConst && in.binds[i].Const == t
	}
	return w == t
}

// finish reports E0711 at name for each parameter still unbound,
// then checks every bound with the inferred arguments (E0701 at
// at), records the arguments in Body.Generic and returns their Inst (0
// when the callee is not generic). ok is false after an E0711, and after
// an explicit type argument that is Poison (badExplicit).
func (in *infer) finish(name source.Span, at source.Span) (inst uint32, ok bool) {
	u := in.u
	if len(in.gens) == 0 {
		return 0, true
	}
	ok = !in.badExplicit
	for i, g := range in.gens {
		if in.bound[i] {
			continue
		}
		ok = false
		pn := u.c.cfg.Interner.Str(g.Name)
		if i >= len(in.poisoned) || !in.poisoned[i] {
			u.err(diag.E0711, name, "cannot infer `"+pn+"`; write it in brackets").Text("param", pn)
		}
		if g.Const {
			in.binds[i] = types.ConstGenericArg(types.ConstArg{})
		} else {
			in.binds[i] = types.TypeArg(types.Poison)
		}
		in.bound[i] = true
	}
	if ok {
		for i, g := range in.gens {
			if g.Const || in.binds[i].Type == types.Poison || i < len(in.checked) && in.checked[i] {
				continue
			}
			for _, b := range u.c.substBounds(g.Bounds, in.binds, in.self) {
				t := in.binds[i].Type
				if i+1 == in.fmtSlot && u.c.isFieldsFormat(b) {
					// The text judges each hole's value (formatCheck),
					// so the record need only be a record or a struct here.
					if in.fmtSkip || u.recordLike(t) {
						continue
					}
				}
				if !u.c.satisfies(u.env, t, b, u.x.rec) {
					if i+1 == in.fmtSlot {
						in.fmtFailed = true
					}
					if u.c.isFieldsFormat(b) && u.fmtA > 0 && t == u.tab.Param(uint32(u.fmtA-1)) {
						u.argsMisuse(at, "needs `"+u.str(t)+": Fields[Format]`")
						continue
					}
					u.b.Diags = append(u.b.Diags, u.c.boundFailure(u.env, at, g.Name, b, t, u.x.gens))
				}
			}
		}
	}
	u.b.Generic = append(u.b.Generic, append([]types.GenericArg(nil), in.binds...))
	return uint32(len(u.b.Generic) - 1), ok
}

// final is callee type p with every argument substituted.
func (in *infer) final(p types.TypeID) types.TypeID {
	s := substituter{c: in.u.c, args: in.binds, self: in.self, lenient: true}
	return s.typ(p)
}

// hasHole reports whether t mentions an inference variable or a const
// placeholder (infer above).
func (c *Checker) hasHole(t types.TypeID) bool {
	tab := c.cfg.Types
	ty := tab.Get(t)
	switch ty.Kind {
	case types.KInfer:
		return true
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice, types.KRawPtr, types.KOptional, types.KErrorUnion:
		return c.hasHole(ty.Elem)
	case types.KArray:
		return ty.Len.Kind == types.CANone && ty.Len.Param > 0 || c.hasHole(ty.Elem)
	case types.KRecord:
		for _, f := range ty.Fields {
			if c.hasHole(f.Type) {
				return true
			}
		}
	case types.KNamed:
		for _, a := range ty.Args {
			if a.IsConst && a.Const.Kind == types.CANone && a.Const.Param > 0 || !a.IsConst && c.hasHole(a.Type) {
				return true
			}
		}
	case types.KFn:
		for _, p := range ty.Fn.Params {
			if c.hasHole(p.Type) {
				return true
			}
		}
		return c.hasHole(ty.Fn.Result)
	}
	return false
}
