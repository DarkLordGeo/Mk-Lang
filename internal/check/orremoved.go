// Why: a default for `?T` and `E!T` has one spelling, `e else { x }`; code
// that calls an `Or` method on one, as other libraries spell a default,
// should be fixed automatically.
//
// What: orRemoved, E0207 on `e.Or(x)` with a fix, plus wholeExpr and
// quietArg.
//
// How: the fix rewrites the call in place, parenthesised when the call is an
// operand. `std.simd`'s `Vec.Or` and `Mask.Or` and user methods named `Or`
// are other members and never reach here.

package check

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
)

// orRemoved reports `e.Or(x)` on a `?T` or `E!T` value (E0207 at the name).
// The fix rewrites the call in place: `.Or(` becomes ` else { ` and the
// closing `)` ` }`, the whole parenthesised where the call is an operand or
// a receiver, since a fallback is the loosest expression. It is
// proven when x's evaluation cannot be seen (a literal, a name, a field path
// from one, a negated literal): `Or` took x eagerly and the block is lazy.
// Otherwise it compiles, and mk check's recompile settles it.
func (u *unit) orRemoved(e *ast.CallExpr, fe *ast.FieldExpr) {
	d := u.err(diag.E0207, fe.Name.Span(), "`?T` and `E!T` have no `Or`: write a default as `e else { x }`").In(diag.PhaseTypes).
		Text("found", "Or").Text("expected", "else")
	if len(e.Args) != 1 || e.Args[0] == nil || e.Args[0].Name != nil || e.Args[0].Value == nil || fe.X == nil {
		d.Alt("x := e else { default }")
		return
	}
	cs, xs, vs := e.Span(), fe.X.Span(), e.Args[0].Value.Span()
	if cs.File != xs.File || cs.File != vs.File || xs.End > vs.Start || vs.End > cs.End || cs.File == source.NoFile {
		d.Alt("x := e else { default }")
		return
	}
	edits := []diag.Edit{
		{Span: source.Span{File: cs.File, Start: xs.End, End: vs.Start}, Text: " else { "},
		{Span: source.Span{File: cs.File, Start: vs.End, End: cs.End}, Text: " }"},
	}
	if !u.wholeExpr(e) {
		edits = []diag.Edit{
			{Span: source.Span{File: cs.File, Start: xs.Start, End: xs.Start}, Text: "("},
			edits[0],
			{Span: source.Span{File: cs.File, Start: vs.End, End: cs.End}, Text: " })"},
		}
	}
	class := diag.Compiles
	if quietArg(e.Args[0].Value) {
		class = diag.Proven
	}
	d.Fix(diag.Fix{Class: class, Edits: edits, Why: "a default is an else block, not Or"})
}

// wholeExpr reports whether e stands whole in the unit's body, where a
// fallback needs no parentheses.
func (u *unit) wholeExpr(e ast.Expr) bool {
	if u.orWhole == nil {
		u.orWhole = map[ast.Expr]bool{}
		if u.b == nil || u.b.Node == nil {
			return false
		}
		ast.Inspect(u.b.Node, func(n ast.Node) bool {
			switch n := n.(type) {
			case nil:
				return false
			case *ast.BindStmt:
				u.orWhole[n.Value] = true
			case *ast.AssignStmt:
				u.orWhole[n.Value] = true
			case *ast.ReturnStmt:
				u.orWhole[n.Value] = true
			case *ast.DiscardStmt:
				u.orWhole[n.Value] = true
			case *ast.Arg:
				u.orWhole[n.Value] = true
			case *ast.ParenExpr:
				u.orWhole[n.X] = true
			}
			return true
		})
	}
	return u.orWhole[e]
}

// quietArg reports whether evaluating x early or late cannot be told
// apart: a literal, a name, a field path from one, or a negated literal.
func quietArg(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.BasicLit, *ast.Ident:
		return true
	case *ast.FieldExpr:
		return x.X != nil && quietArg(x.X)
	case *ast.ParenExpr:
		return quietArg(x.X)
	case *ast.UnaryExpr:
		_, lit := x.X.(*ast.BasicLit)
		return lit && x.Op == ast.TokMinus
	}
	return false
}
