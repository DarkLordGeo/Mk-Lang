// Why: an `if` on the Target constant emits code for the taken branch only,
// so an `asm` under an arch test false for the target never reaches mono's
// register walk; both branches are still type-checked.
//
// What: foldTargetCompare, targetCompare and targetField (a Target field
// compared with a constant), foldLogic and foldNot (`&&`, `||`, `!`),
// isTargetFolded, boolConstOf and setTargetConst.
//
// How: `Target.arch == Arch.X64` and the other comparisons of a Target
// field with a constant of its type fold to a bool constant for the build
// target, and so do `&&`, `||` and `!` over such operands; lower then lowers
// only the taken branch. The field read is already recorded as a ReadTarget
// of that field (universe.go), so a cached body is keyed by the value it
// folded.

package check

import (
	"bytes"

	"mk/internal/ast"
	"mk/internal/source"
	"mk/internal/types"
)

// foldTargetCompare sets e's constant when e is `Target.f == v` or `!=`
// (either order) and v is a constant of f's type: a unit variant of the
// field's enum (`Arch.X64`, `Os.Linux`) or an integer constant.
func (u *unit) foldTargetCompare(e *ast.BinaryExpr) {
	if e.Op != ast.TokEqEq && e.Op != ast.TokNotEq {
		return
	}
	eq, ok := u.targetCompare(e.X, e.Y)
	if !ok {
		eq, ok = u.targetCompare(e.Y, e.X)
	}
	if !ok {
		return
	}
	if e.Op == ast.TokNotEq {
		eq = !eq
	}
	u.setTargetConst(e, eq)
}

// targetCompare compares side, when it is a field of Target with a value,
// with the constant other.
func (u *unit) targetCompare(side, other ast.Expr) (bool, bool) {
	fe, ok := unparenExpr(side).(*ast.FieldExpr)
	if !ok {
		return false, false
	}
	if tu := *u.b.use(unparenExpr(fe.X).ID()); tu.Kind != UseBuiltin || Builtin(tu.Index) != BuiltinTarget {
		return false, false
	}
	val, fl, ok := u.targetField(fe.Name.Name)
	if !ok {
		return false, false
	}
	items, tab := u.c.cfg.Items, u.tab
	ft := tab.Get(fl.Type)
	o := unparenExpr(other)
	switch {
	case ft.Kind == types.KNamed:
		ed, isEnum := items.Get(ft.Item).Decl.(*types.EnumDecl)
		ou := *u.b.use(o.ID())
		if !isEnum || ou.Kind != UseVariant || ou.Item != ft.Item || int(ou.Index) >= len(ed.Variants) || len(ed.Variants[ou.Index].Fields) > 0 {
			return false, false
		}
		el := u.c.Layout(fl.Type)
		if !el.HasTag || int(ou.Index) >= len(el.Variants) || int(fl.Offset+el.Tag.Offset)+int(el.Tag.Size) > len(val) {
			return false, false
		}
		tag := types.LoadUint(val[fl.Offset+el.Tag.Offset:], int(el.Tag.Size))
		return tag == el.Variants[ou.Index].Discr, true
	case ft.IsInteger():
		k := u.nodeConst(o.ID())
		if k == types.NoConst {
			return false, false
		}
		ov := u.c.cfg.Consts.Get(k).Bytes
		size := u.c.Layout(fl.Type).Shape.Size
		if uint64(len(ov)) != size || fl.Offset+size > uint64(len(val)) {
			return false, false
		}
		return bytes.Equal(val[fl.Offset:fl.Offset+size], ov), true
	}
	return false, false
}

// targetField is the bytes of Target's value and the layout of its field
// called name; ok is false while Target has no value (std.target's own
// check, where the driver writes it; or a world with no driver).
func (u *unit) targetField(name source.Symbol) ([]byte, types.FieldLayout, bool) {
	k := u.c.known
	if k.Target == types.NoItem || k.TargetInfo == types.NoItem {
		return nil, types.FieldLayout{}, false
	}
	cd, ok := u.c.cfg.Items.Get(k.Target).Decl.(*types.ConstDecl)
	if !ok || !cd.HasValue {
		return nil, types.FieldLayout{}, false
	}
	sd, ok := u.c.cfg.Items.Get(k.TargetInfo).Decl.(*types.StructDecl)
	if !ok {
		return nil, types.FieldLayout{}, false
	}
	lay := u.c.Layout(u.tab.Named(k.TargetInfo, nil))
	if len(lay.Fields) != len(sd.Fields) {
		return nil, types.FieldLayout{}, false
	}
	for i, f := range sd.Fields {
		if f.Name == name {
			return u.c.cfg.Consts.Get(cd.Value).Bytes, lay.Fields[i], true
		}
	}
	return nil, types.FieldLayout{}, false
}

// foldLogic sets the constant of `a && b` and `a || b` when an operand is a
// folded Target comparison and the result is known: both operands are bool
// constants, or the left one decides it (false && b, true || b, which
// never evaluate b). Other constant operands (`x && true`) fold nothing, so
// conditions that do not read Target lower as they always did.
func (u *unit) foldLogic(e *ast.BinaryExpr) {
	if !u.isTargetFolded(e.X) && !u.isTargetFolded(e.Y) {
		return
	}
	x, okx := u.boolConstOf(e.X)
	if !okx {
		return
	}
	switch {
	case e.Op == ast.TokAmpAmp && !x:
		u.setTargetConst(e, false)
	case e.Op == ast.TokPipePipe && x:
		u.setTargetConst(e, true)
	default:
		if y, oky := u.boolConstOf(e.Y); oky {
			u.setTargetConst(e, y)
		}
	}
}

// foldNot sets the constant of `!a` for a folded Target comparison a.
func (u *unit) foldNot(e *ast.UnaryExpr) {
	if !u.isTargetFolded(e.X) {
		return
	}
	if x, ok := u.boolConstOf(e.X); ok {
		u.setTargetConst(e, !x)
	}
}

func (u *unit) isTargetFolded(e ast.Expr) bool {
	return u.targetFolded[unparenExpr(e).ID()]
}

// boolConstOf is the value of e when check recorded a bool constant for it
// (a folded comparison above; a literal; a named bool const).
func (u *unit) boolConstOf(e ast.Expr) (bool, bool) {
	k := u.nodeConst(unparenExpr(e).ID())
	if k == types.NoConst {
		return false, false
	}
	v := u.c.cfg.Consts.Get(k)
	if v.Type != types.Bool || len(v.Bytes) != 1 {
		return false, false
	}
	return v.Bytes[0] != 0, true
}

// setTargetConst records e's bool constant, folded from Target.
func (u *unit) setTargetConst(e ast.Expr, v bool) {
	var b uint64
	if v {
		b = 1
	}
	u.b.setConst(e.ID(), u.c.scalarConst(types.Bool, 1, b))
	if u.targetFolded == nil {
		u.targetFolded = map[ast.NodeID]bool{}
	}
	u.targetFolded[e.ID()] = true
}
