// Why: two universe names are not ordinary items and need their own typing
// rules: `some(x)`, which builds an optional, and `Target`, the compile-time
// description of the build target.
//
// What: isTarget, target (the universe name Target, a TargetInfo value) and
// someCall (`some(x)`).
//
// How: some(x) is checked under its expected type, so a literal adapts and
// a mismatch is reported once, at x. Target is recorded as BuiltinTarget,
// which lower reads as the constant Known.Target; each field read is
// recorded so a target change reruns only what read the field.

package check

import (
	"mk/internal/ast"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// isTarget reports whether e is the universe name Target.
func (u *unit) isTarget(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	r := u.ref(id)
	return r.Kind == resolve.RefUniverse && source.Symbol(r.A) == source.SymTarget
}

// target types the universe name Target: a value of std's TargetInfo,
// recorded as UseBuiltin/BuiltinTarget (lower reads it as the constant
// Known.Target). A use under a field access reads that field (field
// records it); any other use reads every field (ReadTarget). With no
// prelude TargetInfo is NoItem and the name is Poison, the inert case of
// types.KnownItems.
func (u *unit) target(e ast.Expr) types.TypeID {
	ti := u.c.known.TargetInfo
	if ti == types.NoItem {
		return types.Poison
	}
	*u.b.use(e.ID()) = Use{Kind: UseBuiltin, Index: uint32(BuiltinTarget)}
	it := u.c.cfg.Items.Get(ti)
	u.x.rec.shape(it.Key)
	if e.ID() != u.targetOne {
		for _, f := range declFields(u.c.declOfItem(it)) {
			u.x.rec.target(u.c.cfg.Interner.Str(f.Name))
		}
	}
	return u.tab.Named(ti, nil)
}

// someCall types `some(x)`, the optional's constructor: under an expected
// `?T`, x is checked against T and the call is the expected type, so a
// literal adapts (`var o: ?i32 = some(1)` holds an i32) and a mismatch is
// reported once, at x; with no expected optional it is `?` of x's type, and
// the caller's expected type judges it (E0301 for `c: i32 = some(0)`). It
// takes exactly one positional argument (E0304, E0305). No Use is recorded:
// lower builds the optional from the call's type.
func (u *unit) someCall(e *ast.CallExpr, want types.TypeID) types.TypeID {
	u.argShape(e, 1, false, "some(x: T): ?T", nil, []source.Symbol{0})
	if len(e.Args) != 1 || e.Args[0] == nil || e.Args[0].Value == nil {
		u.walkArgs(e)
		return types.Poison
	}
	x := e.Args[0].Value
	if want != types.NoType && want != types.Poison && u.tab.Kind(want) == types.KOptional {
		if t := u.expr(x, u.tab.Get(want).Elem); t == types.Poison {
			return types.Poison
		}
		return want
	}
	t := u.expr(x, types.NoType)
	if t == types.Poison || want == types.Poison {
		return types.Poison
	}
	return u.tab.Optional(t)
}
