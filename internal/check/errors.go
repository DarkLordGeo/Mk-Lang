// Why: an `E!T` value must be handled where it is produced: passed on with
// `try`, failed with `fail`, or handled with `else` or `match`, never bound,
// stored or passed as is, and only with errors the function's set holds.
//
// What: tryExpr (E0804, E0801), failStmt and setCovers (E0801), elseExpr
// (E0307, and E0803 for a handler that falls through where a value is
// needed), unhandled, fallibleArgs and storedFallible (E0802).
//
// How: the error sets are compared member by member (missingErrors names
// what the function's set lacks). Widening an error value to a larger set
// is expr's (widens).

package check

import (
	"slices"
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// tryExpr types `try e`: e must be E!T (E0804 otherwise, ?T
// included, proven fix: remove try); the result is T; e's set must be a
// subset of the enclosing function's (E0801 at the try, fact missing),
// except in a test.
func (u *unit) tryExpr(e *ast.UnaryExpr, want types.TypeID) types.TypeID {
	if u.inDefer() {
		u.cleanupExit(e.Span(), "try")
	}
	if u.inContract {
		u.contractForm(keywordSpan(u.fx, e.Span().Start, ast.TokTry), "`try`")
	}
	t := u.operand(e.X, types.NoType)
	if t == types.Poison {
		return types.Poison
	}
	ty := u.tab.Get(t)
	if ty.Kind != types.KErrorUnion {
		sp := e.Span()
		u.err(diag.E0804, keywordSpan(u.fx, sp.Start, ast.TokTry), "`try` needs a value that can fail, found `"+u.str(t)+"`").Text("found", u.str(t)).
			Fix(diag.Fix{Class: diag.Proven, Edits: []diag.Edit{{Span: between(sp, e.X.Span()), Text: ""}}, Why: "the value cannot fail"})
		// The `try` failed, so it is Poison: returning the
		// operand's own type (`?int` for `try o`) cascaded into an E0301
		// at the next use.
		return types.Poison
	}
	u.setCovers(e.Span(), "try", ty.Errors)
	return ty.Elem
}

// failStmt is `fail e`: e is an error value (E0301 otherwise)
// whose set is a subset of the function's (E0801).
func (u *unit) failStmt(s *ast.FailStmt) {
	if u.inDefer() {
		u.cleanupExit(s.Span(), "fail")
	}
	t := u.expr(s.Value, types.NoType)
	if t == types.Poison {
		return
	}
	ty := u.tab.Get(t)
	if ty.Kind != types.KErrorSet {
		u.err(diag.E0301, s.Value.Span(), "`fail` needs an error value, found `"+u.str(t)+"`").Text("expected", "an error").Text("found", u.str(t))
		return
	}
	u.setCovers(s.Span(), "fail", ty.Errors)
}

// setCovers is E0801: the errors of es that the enclosing
// function's set lacks. A test's try and fail are not checked, nor
// a unit with no signature.
func (u *unit) setCovers(at source.Span, tok string, es types.ErrorSetID) {
	if u.sig == nil || u.inContract || u.sig.Result == types.Poison {
		return // a try in a predicate is E0101 (contractForm)
	}
	if _, ok := u.b.Node.(*ast.TestItem); ok {
		return
	}
	if u.tab.Subset(es, u.sig.Errors) {
		return
	}
	missing := u.missingErrors(es, u.sig.Errors)
	at.End = min(at.End, at.Start+uint32(len(tok)))
	u.err(diag.E0801, at, "the function's error set does not include "+strings.Join(missing, ", ")).Fact("missing", diag.TextList(missing...)).
		Alt("add them to the result's error set").Alt("handle them with `else`")
}

// missingErrors names the members of es that target lacks: error names, or
// set parameters by their names.
func (u *unit) missingErrors(es, target types.ErrorSetID) []string {
	have := u.tab.Members(target)
	var missing []string
	for _, m := range u.tab.Members(es) {
		if !slices.Contains(have, m) {
			if m.IsParam {
				if int(m.Param) < len(u.x.gens) {
					missing = append(missing, u.c.cfg.Interner.Str(u.x.gens[m.Param].Name))
				}
				continue
			}
			missing = append(missing, u.c.cfg.Interner.Str(u.c.cfg.Items.Get(m.Item).Name))
		}
	}
	return missing
}

// elseExpr types `e else { B }` and `e else x { B }`: e is E!T (x
// the error set) or ?T (no x: E0307), the expression is T. Where a value
// is needed and B can fall through, E0803 at the else.
func (u *unit) elseExpr(e *ast.ElseExpr) types.TypeID {
	needed := u.dropAt != ast.Expr(e)
	discard := u.discardAt == ast.Expr(e)
	t := u.expr(e.X, types.NoType)
	elem, errT := types.Poison, types.Poison
	switch ty := u.tab.Get(t); {
	case t == types.Poison:
	case ty.Kind == types.KErrorUnion:
		elem, errT = ty.Elem, u.tab.ErrorSetType(ty.Errors)
	case ty.Kind == types.KOptional:
		elem = ty.Elem
		if e.Name != nil {
			u.err(diag.E0307, e.Name.Span(), "`?T` has no error to bind").Text("found", u.str(t))
		}
	default:
		u.err(diag.E0307, e.X.Span(), "`else` handles an error or a none, found `"+u.str(t)+"`").Text("found", u.str(t))
	}
	if e.Name != nil {
		u.bindName(e.Name, errT)
	}
	saved, savedDiscard := u.dropAt, u.discardAt
	u.dropAt, u.discardAt = nil, nil
	switch {
	case needed && elem != types.Poison && elem != types.Void:
		// The handler's last line is its value, or it does not fall
		// through; otherwise E0803 (valueArm's else case).
		u.elseValue(e, elem)
	case discard && elem != types.Poison && elem != types.Void:
		u.elseDiscard(e, elem)
	default:
		u.block(e.Handler)
	}
	u.dropAt, u.discardAt = saved, savedDiscard
	return elem
}

// unhandled is E0802: an E!T value where its T is needed or
// where it is dropped; the fix passes the error on with try.
func (u *unit) unhandled(e ast.Expr, t types.TypeID) {
	at := e.Span()
	u.err(diag.E0802, at, "the error of `"+u.str(t)+"` is not handled").Text("type", u.str(t)).
		Fix(diag.Fix{Class: diag.Compiles, Edits: []diag.Edit{{Span: source.Span{File: at.File, Start: at.Start, End: at.Start}, Text: "try "}},
			Why: "pass the error to the caller"}).
		// When the caller's set lacks these errors, the try fix does not
		// compile and the driver moves it here; these two always apply.
		Alt("handle it here: `" + "<value> else e { ... }`, or a `match`").
		Alt("widen the function's error set to hold these errors, then pass it on with `try`")
}

// fallibleArgs is E0802 for an argument whose value is `E!T`: an `E!T` is
// used only under `try`, `else`
// or `match`, so `Get(L1())` with `L1(): A!int` is reported at the
// argument even when the parameter is an `E!T` (widened or generic). expr
// runs it on every call and construction; an argument already reported at
// the same span (E0802, or E0301 from the expected type) is not reported
// twice.
func (u *unit) fallibleArgs(e *ast.CallExpr) {
	for _, a := range e.Args {
		if a != nil {
			u.storedFallible(a.Value)
		}
	}
}

// storedFallible is E0802 for one value that is passed or stored (an
// argument, a construction field, an array or record element, a repeat
// value: an `E!T` value "cannot be bound, stored or passed") when
// its type is `E!T` and no E0802 or E0301 stands at it yet.
func (u *unit) storedFallible(x ast.Expr) {
	if x == nil {
		return
	}
	t := u.b.TypeOf(x.ID())
	if t == types.NoType || t == types.Poison || u.tab.Kind(t) != types.KErrorUnion {
		return
	}
	at := x.Span()
	for _, d := range u.b.Diags {
		if d.At == at && (d.Code == diag.E0802 || d.Code == diag.E0301) {
			return
		}
	}
	u.unhandled(x, t)
}
