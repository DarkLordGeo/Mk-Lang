// Why: a C import binds a C function pointer as an Mk fn type (`?fn` in a
// struct field), which safe code could unwrap and call, running unchecked C
// code. A call through a fn value cannot know where the value came from.
//
// What: cFnPointer (E1101 c-fn-pointer) and holdsFn.
//
// How: the obligation sits where the value leaves C: reading a field of a C
// import's struct whose type holds a fn (a fn, a ?fn, or an array of them)
// is E1101 outside unsafe. The other ways out of C need unsafe already: a
// bodiless extern's call, an extern var and a raw pointer's pointee
// (E1103). A store into such a field reads nothing out of C and stays safe.

package check

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/types"
)

// cFnPointer is E1101 c-fn-pointer for fe, a read of field type ft of the
// struct it, when it is a C import's struct and ft holds a fn, outside
// unsafe and outside the binding module itself.
func (u *unit) cFnPointer(fe *ast.FieldExpr, it *types.Item, ft types.TypeID) {
	cimp := u.c.cfg.CImport
	if u.unsafeDepth > 0 || cimp == nil || fe.ID() == u.cFnStore || (u.m != nil && it.Module == u.m.ID) || !cimp(it.Module) || !u.holdsFn(ft) {
		return
	}
	u.err(diag.E1101, fe.Span(), "reading a C function pointer needs an `unsafe \"reason\" { }` block: calling it runs C code").
		Text("operation", "c-fn-pointer")
}

// holdsFn reports a fn type, an optional one, or an array of either.
func (u *unit) holdsFn(t types.TypeID) bool {
	for {
		ty := u.tab.Get(t)
		switch ty.Kind {
		case types.KFn:
			return true
		case types.KOptional, types.KArray:
			t = ty.Elem
		default:
			return false
		}
	}
}
