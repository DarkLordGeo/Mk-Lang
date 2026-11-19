// Why: a quote's holes can splice only some types, and std.build's File reads
// a file at compile time; check reports misuse of either before gen's host
// or a running program meets it.
//
// What: spliceOperand and spliceable (the splice table), buildOnly and
// isBuildFile.
//
// How: every splice operand whose type is outside the table is E0301 at the
// splice; a call of build.File outside a const initializer, a compile-time
// requires or a generator is E0705.

package check

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/types"
)

// spliceOperand is E0301 at x unless t is a type the host splices: str, an
// integer, bool, char, a float, a fn value, or Type, Code or Block, by value
// or through & or mut (gen's host.go splice). A poisoned operand has its
// own diagnostic. An operand whose type names a type parameter is judged at
// each instance by the host: E0301 at the splice for an instance outside
// the table.
func (u *unit) spliceOperand(x ast.Expr, t types.TypeID) {
	if t == types.Poison || t == types.NoType || u.spliceable(t) || u.mentionsParam(t, 0) {
		return
	}
	u.err(diag.E0301, x.Span(), "a splice is a str, an integer, a bool, a char, a float, a fn value, a Type, a Code or a Block; this one is `"+u.str(t)+"`").
		Text("expected", "str, an integer, bool, char, a float, a fn value, Type, Code or Block").Text("found", u.str(t))
}

// spliceable is the splice table.
func (u *unit) spliceable(t types.TypeID) bool {
	ty := u.tab.Get(t)
	switch ty.Kind {
	case types.KStr, types.KInt, types.KIntPtr, types.KBool, types.KChar, types.KFloat, types.KFn:
		return true
	case types.KRef, types.KMut:
		ty = u.tab.Get(ty.Elem)
		if ty.Kind != types.KNamed {
			return false
		}
	case types.KNamed:
	default:
		return false
	}
	k := &u.c.known
	return ty.Item != types.NoItem && (ty.Item == k.TypeInfo || ty.Item == k.Code || ty.Item == k.Block)
}

// buildOnly is E0705 for a call of std.build's File outside a unit that
// runs only at compile time (a const or global initializer, a compile-time
// requires, a generator), and for a fn value of it outside a generator: it
// reads a file at compile time, and a run of the program has no such file,
// so no native code ever reaches it.
func (u *unit) buildOnly(at ast.Node, s *types.Signature, what string) {
	if s == nil || s.Item == types.NoItem || !u.c.isBuildFile(s.Item) {
		return
	}
	if (what == "call" && u.compileTimeUnit()) || u.genOnlyUnit() {
		return
	}
	u.err(diag.E0705, at.Span(), "`build.File` reads a file at compile time: call it in a const initializer or a generator").
		Text("param", "build.File")
}

// isBuildFile reports whether it is std.build's File.
func (c *Checker) isBuildFile(id types.ItemID) bool {
	it := c.cfg.Items.Get(id)
	if it.Owner != types.NoItem || it.Kind != types.KindFunc || c.cfg.Interner.Str(it.Name) != "File" {
		return false
	}
	return c.cfg.Items.Module(it.Module).Path == "std.build"
}
