// Why: an operator whose operands are all integer literals has a value
// check can know. When that value overflows its type, divides by zero or
// shifts past the width, the operator panics every time it runs:
// `x: u8 = 200 + 100`.
//
// What: (*unit).litFold, run on every unary and binary operator a body
// types: E0303 at an operator over integer literals whose run-time
// operator panics; litOperand.
//
// How: folder (fold.go) folds the operator with a leaf hook that gives
// each operand as the body typed it: an integer literal (or a negated one)
// by its Body.Consts entry, an inner operator by its own fold, kept in
// unit.litVals. Every intermediate result is so judged at the type the
// run time computes it in, and the verdict is the run time's. The wrapping
// operators never report; `<<` drops the bits shifted out and reports only
// an amount out of range. The check is on the operator wherever it is
// written, reached or not, as for a literal that does not fit. Any other
// operand (a name, a call, a byte literal) leaves the operator to run
// time; the operands are checked before a folder is made, so a common
// operator costs no allocation. An initializer the evaluator runs (a
// const's, a global var's) is left to its evaluation (E0901).

package check

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/types"
)

// litFold folds operator e of type t over integer literals, records its
// value for an enclosing operator, and reports E0303 when it panics.
func (u *unit) litFold(e ast.Expr, t types.TypeID) {
	delete(u.litVals, e.ID())
	if t == types.Poison || t == types.NoType || !u.isInt(t) || u.evalInit() {
		return
	}
	switch e := e.(type) {
	case *ast.UnaryExpr:
		if u.negLit(e) {
			// A negated literal is one constant, recorded on e.
			if v, ok := u.c.fvalOf(u.bodyConst(e)); ok {
				u.keepLit(e, v)
			}
			return
		}
		if !u.litOperand(e.X) {
			return
		}
	case *ast.BinaryExpr:
		if !u.litOperand(e.X) || !u.litOperand(e.Y) {
			return
		}
	default:
		return
	}
	f := &folder{m: u.m, fx: u.fx, rec: u.x.rec}
	f.leaf = func(x ast.Expr) (fval, bool, bool) {
		switch x := x.(type) {
		case *ast.ParenExpr:
			return fval{}, false, false
		case *ast.BasicLit:
			k := u.bodyConst(x)
			if x.Kind != ast.TokInt || k == types.NoConst {
				return fval{}, false, true
			}
			v, ok := u.c.fvalOf(k)
			return v, ok, true
		case *ast.UnaryExpr:
			if x == e {
				return fval{}, false, false
			}
			if u.negLit(x) {
				v, ok := u.c.fvalOf(u.bodyConst(x))
				return v, ok, true
			}
		case *ast.BinaryExpr:
			if x == e {
				return fval{}, false, false
			}
		}
		v, ok := u.litVals[x.ID()]
		return v, ok, true
	}
	v, ok, panicked := f.fold(e)
	switch {
	case ok && !v.isBool && v.enum == types.NoConst:
		u.keepLit(e, v)
	case panicked && f.panicNode == ast.Node(e):
		code := f.panicCode.String()
		msg := "this operation on literals panics with `" + code + "` when it runs"
		if f.panicCode == types.PanicOverflow {
			msg = "this operation on literals does not fit `" + u.str(t) + "`: it panics with `overflow` when it runs"
		}
		u.err(diag.E0303, e.Span(), msg).Text("panic", code).Text("type", u.str(t))
	}
}

// evalInit reports a unit whose initializer the evaluator runs at compile
// time, a const's or a global var's: its verdict and code (E0901) are the
// evaluation's.
func (u *unit) evalInit() bool {
	switch u.b.Node.(type) {
	case *ast.ConstDecl, *ast.GlobalDecl:
		return true
	}
	return false
}

// keepLit records the folded value of operator e.
func (u *unit) keepLit(e ast.Expr, v fval) {
	if u.litVals == nil {
		u.litVals = map[ast.NodeID]fval{}
	}
	u.litVals[e.ID()] = v
}

// litOperand reports whether x is an operand litFold folds: an integer
// literal the body gave a value (negated or not), or an operator it folded.
func (u *unit) litOperand(x ast.Expr) bool {
	for {
		p, ok := x.(*ast.ParenExpr)
		if !ok {
			break
		}
		x = p.X
	}
	switch x := x.(type) {
	case *ast.BasicLit:
		return x.Kind == ast.TokInt && u.bodyConst(x) != types.NoConst
	case *ast.UnaryExpr, *ast.BinaryExpr:
		_, ok := u.litVals[x.ID()]
		return ok
	}
	return false
}

// negLit reports `-n` with n an integer literal, whose value the body
// recorded on the UnaryExpr.
func (u *unit) negLit(e *ast.UnaryExpr) bool {
	lit, ok := e.X.(*ast.BasicLit)
	return ok && e.Op == ast.TokMinus && lit.Kind == ast.TokInt && u.bodyConst(e) != types.NoConst
}

// bodyConst is the value the body recorded for e, NoConst for none.
func (u *unit) bodyConst(e ast.Expr) types.ConstID {
	if u.b.Consts == nil {
		return types.NoConst
	}
	return u.b.Consts[u.b.slot(e.ID())]
}
