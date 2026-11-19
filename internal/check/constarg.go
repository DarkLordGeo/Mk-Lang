// Why: a const value argument must be a constant mono can make per
// instance. Forms mono cannot make (`Lane(2 + 3)`, `Lane(-K)`,
// `Lane(Tab.Len())`, `Lane(SizeOf[T]())` in a generic) would otherwise
// reach mono's instance-limit walk as an E0000.
//
// What: constArgForms (E0705 for a const value argument mono cannot make a
// constant), monoForm, foldArg, constArgCall, readsFolded and
// constParamPath.
//
// How: mono takes literals, named consts, `const` parameters and their fields,
// struct and record literals of those, payload-free variants, and `+`, `-`, `*`
// over them (mono's canon.go foldArith). Check folds every closed part and
// records its value on the node for lower; any other part is E0705 at the
// argument.

package check

import (
	"math/big"
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/resolve"
	"mk/internal/types"
)

// constArgForms judges the argument x of const value parameter p of the
// call (constArgs has found no local, var or call in it): every part is a
// form mono makes a constant (monoForm), else E0705 at x naming the first
// part that is not. A `fn` parameter takes a function's name or a literal,
// which constArgs and fnConst judge.
func (u *unit) constArgForms(x ast.Expr, p types.Param) {
	if p.Type == types.Poison || u.tab.Kind(p.Type) == types.KFn {
		return
	}
	pn := u.c.cfg.Interner.Str(p.Name)
	if bad := u.monoForm(x, p.Type, true); bad != nil {
		var b strings.Builder
		u.c.exprText(&b, bad)
		what := "`" + b.String() + "`"
		if strings.Contains(what, "...") {
			what = "this operand"
		}
		u.err(diag.E0705, bad.Span(), "`"+pn+"` is a const parameter; "+what+" is not a compile-time constant here: a const argument is a literal, a named const, a `const` parameter or generic or a field of one, a struct, record or array literal of these, a variant without a payload, or `+`, `-` and `*` over integers of these").
			Text("param", pn)
	}
}

// monoForm is nil when x is a constant mono makes per instance, else the
// first part of x that is not. A part that is closed and folds (folder,
// constArgCall) gets its value recorded on its node, want typing a whole
// argument; a recorded value (a literal, `-1`, `SizeOf[u8]()`, a folded
// Target test) is one already. lower reads a recorded value for a whole
// argument of any form (call.go's literal of a const argument), and inside
// one at an operator and a conversion or layout call (readsFolded), not at
// a field read or `Len`, which fold only as the whole argument or inside a
// folded operator (whole).
func (u *unit) monoForm(x ast.Expr, want types.TypeID, whole bool) ast.Expr {
	if u.b.Consts != nil && u.b.Consts[u.b.slot(x.ID())] != types.NoConst {
		return nil
	}
	switch e := x.(type) {
	case *ast.ParenExpr:
		// A whole argument's value is read at its own node; inside an
		// operator lower reads the value of what is inside (exprConv).
		if whole && u.foldArg(e, want) {
			return nil
		}
		return u.monoForm(e.X, want, false)
	case *ast.Ident, *ast.BasicLit, *ast.FnLit:
		// A name constArgs let through: a const, a `const` parameter or
		// generic, a function, true or false.
		return nil
	case *ast.FieldExpr:
		if u.useOf(e.ID()).Kind != UseField || u.constParamPath(e.X) {
			// `Mode.B`, `lib.K`, `lib.F`, `o.v`, `q.p.y`; any other use is
			// left to mono.
			return nil
		}
		if whole && u.foldArg(e, want) {
			return nil
		}
		return e // `KP.x`, a field of a named const
	case *ast.IndexExpr:
		// `Tab[1]`, an index into a value, is listed; `G[int]` or `G[T]`,
		// a generic function's instance, is a name mono passes.
		xt := u.b.TypeOf(e.X.ID())
		if xt == types.NoType || xt == types.Poison {
			return nil
		}
		if v, _ := u.deref(xt); u.tab.Kind(v) != types.KArray && u.tab.Kind(v) != types.KSlice {
			return nil
		}
		if whole && u.foldArg(e, want) {
			return nil
		}
		return e
	case *ast.UnaryExpr:
		if u.foldArg(e, want) {
			return nil
		}
		return e
	case *ast.BinaryExpr:
		if u.foldArg(e, want) {
			return nil
		}
		switch e.Op {
		case ast.TokPlus, ast.TokMinus, ast.TokStar:
			if t := u.b.TypeOf(e.ID()); t != types.NoType && u.tab.Get(t).IsInteger() {
				if bad := u.monoForm(e.X, types.NoType, false); bad != nil {
					return bad
				}
				return u.monoForm(e.Y, types.NoType, false)
			}
		}
		return e
	case *ast.CallExpr:
		if u.useOf(e.ID()).Kind == UseConstruct {
			for _, a := range e.Args {
				if a == nil || a.Value == nil {
					continue
				}
				if bad := u.monoForm(a.Value, types.NoType, false); bad != nil {
					return bad
				}
			}
			return nil
		}
		if (whole || u.readsFolded(e)) && u.foldArg(e, want) {
			return nil
		}
		return e
	case *ast.RecordLit:
		for _, f := range e.Fields {
			if f == nil || f.Value == nil {
				continue // a bare name: `(a)` is `(a: a)`
			}
			if bad := u.monoForm(f.Value, types.NoType, false); bad != nil {
				return bad
			}
		}
		return nil
	case *ast.ArrayLit:
		for _, el := range e.Elems {
			if bad := u.monoForm(el, types.NoType, false); bad != nil {
				return bad
			}
		}
		return nil
	}
	// Any other form (an if or match expression, a slice) is left to mono:
	// E0705 is only for the forms listed above, which would reach mono's
	// E0000 (`Lane(if K > 3 { 1 } else { 2 })` is accepted).
	return nil
}

// readsFolded reports whether lower reads call e's recorded value wherever
// e is, not only as a whole argument: a conversion, `Cast` and `Trunc`
// (its builtin lowering's literal) and `SizeOf` and `AlignOf` of a closed
// type (`N + int(3)`). `Len` reads its receiver there (RvLen), so it folds
// only as the whole argument or inside a folded operator.
func (u *unit) readsFolded(e *ast.CallExpr) bool {
	use := u.useOf(e.ID())
	if use.Kind != UseBuiltin {
		return false
	}
	switch Builtin(use.Index) {
	case BuiltinConvert, BuiltinCast, BuiltinTrunc, BuiltinSizeOf, BuiltinAlignOf:
		return true
	}
	return false
}

// constParamPath reports whether x is a `const` parameter of the function
// u checks, or a field path from one.
func (u *unit) constParamPath(x ast.Expr) bool {
	switch e := x.(type) {
	case *ast.Ident:
		r := u.ref(e)
		return r.Kind == resolve.RefParam && u.sig != nil && r.A >= 1 && int(r.A) <= len(u.sig.Params) && u.sig.Params[r.A-1].Const
	case *ast.FieldExpr:
		return u.useOf(e.ID()).Kind == UseField && u.constParamPath(e.X)
	case *ast.ParenExpr:
		return u.constParamPath(e.X)
	}
	return false
}

// foldArg folds x when it is closed and records its value on x: an integer
// or bool of x's type (want's, when x is a whole argument). An overflow or
// a division by zero is E0901 at the operator (the folder's); a value
// that does not fit want is E0303. True when x now has a value or a
// diagnostic was reported for it.
func (u *unit) foldArg(x ast.Expr, want types.TypeID) bool {
	t := u.b.TypeOf(x.ID())
	if want == types.NoType {
		want = t
	}
	if want == types.NoType || want == types.Poison || want != types.Bool && !u.tab.Get(want).IsInteger() {
		return false
	}
	reported := false
	f := &folder{m: u.m, fx: u.fx, rec: u.x.rec, want: want, emit: func(d *diag.Diagnostic) {
		u.b.Diags = append(u.b.Diags, d)
		reported = true
	}}
	f.call = func(ce *ast.CallExpr) (fval, bool, bool) { return u.constArgCall(f, ce) }
	v, ok, panicked := f.fold(x)
	if panicked || reported {
		return true
	}
	if !ok {
		return false
	}
	id, fits := u.c.internFolded(v, want)
	if !fits {
		if v.isBool || want == types.Bool {
			return false
		}
		u.err(diag.E0303, x.Span(), "the value does not fit in `"+u.str(want)+"`").Text("value", v.n.String()).Text("type", u.str(want))
		return true
	}
	u.b.setConst(x.ID(), id)
	return true
}

// constArgCall folds the builtin calls of a closed const argument: one
// whose value check recorded (`SizeOf[u32]()`, `AlignOf[T]()` of a closed
// T), an integer conversion `T(x)` or `Cast[T](x)` of a folded integer
// that fits T (else no fold), `Trunc[T](x)` wrapped to T, and `xs.Len()`
// of an array of known length.
func (u *unit) constArgCall(f *folder, ce *ast.CallExpr) (fval, bool, bool) {
	if u.b.Consts != nil {
		if id := u.b.Consts[u.b.slot(ce.ID())]; id != types.NoConst {
			if v, ok := u.c.fvalOf(id); ok {
				return v, true, false
			}
		}
	}
	use := u.useOf(ce.ID())
	if use.Kind != UseBuiltin {
		return f.notConst(ce)
	}
	t := u.b.TypeOf(ce.ID())
	switch Builtin(use.Index) {
	case BuiltinConvert, BuiltinCast, BuiltinTrunc:
		if len(ce.Args) != 1 || ce.Args[0] == nil || ce.Args[0].Value == nil || t == types.NoType || !u.tab.Get(t).IsInteger() {
			return f.notConst(ce)
		}
		v, ok, p := f.fold(ce.Args[0].Value)
		if !ok || v.isBool || v.enum != types.NoConst {
			return v, false, p
		}
		r := fval{n: new(big.Int).Set(v.n), t: t}
		switch {
		case Builtin(use.Index) == BuiltinTrunc:
			r.n = f.wrap(r.n, t)
		case u.c.inRange(r.n, t):
		case Builtin(use.Index) == BuiltinCast:
			// `Cast` panics when the value does not fit, here at compile
			// time.
			f.panicAt(ce, types.PanicCast)
			return fval{}, false, true
		default:
			return f.notConst(ce)
		}
		return r, true, false
	case BuiltinLen:
		fe, ok := ce.Fun.(*ast.FieldExpr)
		if !ok || len(ce.Args) != 0 {
			return f.notConst(ce)
		}
		at := u.b.TypeOf(fe.X.ID())
		for at != types.NoType && (u.tab.Kind(at) == types.KRef || u.tab.Kind(at) == types.KMut) {
			at = u.tab.Get(at).Elem
		}
		if at == types.NoType || u.tab.Kind(at) != types.KArray {
			return f.notConst(ce)
		}
		n := u.tab.Get(at).Len
		if n.Kind != types.CAValue {
			return f.notConst(ce)
		}
		k, ok := u.c.constInt(n.Value)
		if !ok {
			return f.notConst(ce)
		}
		return fval{n: big.NewInt(k), t: types.Int}, true, false
	}
	return f.notConst(ce)
}
