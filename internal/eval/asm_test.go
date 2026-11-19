// Why: inline assembly has no meaning at compile time, so the evaluator must
// refuse it with a diagnostic instead of guessing.
// What: TestAsmRefused and TestExternVarRefused.
// How: hand-built bodies holding a StAsm, a naked function call and a read of
// an extern var; each must stop with E0901 (refused: asm, refused: global).

package eval

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestAsmRefused: a body holding an asm statement is refused when the
// statement runs, and a call of a naked function is refused at the call,
// before its body would run.
func TestAsmRefused(t *testing.T) {
	t.Run("asm statement", func(t *testing.T) {
		v := newVWorld(t)
		g := v.fn("main", "G", types.Void)
		x := v.body(g, types.Void)
		s := x.sp()
		id := x.b.AddAsm(mir.Asm{Template: "nop", Options: mir.AsmNomem | mir.AsmNostack, Span: s})
		x.b.Blocks[0].Stmts = append(x.b.Blocks[0].Stmts, mir.Statement{Kind: mir.StAsm, Asm: id, Span: s})
		x.ret(0)
		x.done()
		wantRefusal(t, v.call(v.evaluator(0), g, 0), types.RefuseAsm)
	})
	t.Run("naked call", func(t *testing.T) {
		v := newVWorld(t)
		n := v.fn("main", "Stub", types.Void)
		v.world.items.Get(n).Decl.(*types.FuncDecl).Naked = true
		y := v.body(n, types.Void)
		s := y.sp()
		id := y.b.AddAsm(mir.Asm{Template: "ret", Options: mir.AsmNoreturn, Span: s})
		y.b.Blocks[0].Stmts = append(y.b.Blocks[0].Stmts, mir.Statement{Kind: mir.StAsm, Asm: id, Span: s})
		y.term(0, mir.Terminator{Kind: mir.TermUnreachable})
		y.done()
		g := v.fn("main", "G", types.Void)
		x := v.body(g, types.Void)
		next := x.block()
		x.call(0, n, lp(0), next)
		x.ret(next)
		x.done()
		wantRefusal(t, v.call(v.evaluator(0), g, 0), types.RefuseAsm)
	})
}

// TestExternVarRefused: an extern var has no value at compile time
// (the linker defines it): a read through its address, as lower writes
// `name` (*(&name)), is refused as a global's is (RefuseGlobal).
func TestExternVarRefused(t *testing.T) {
	w := newWorld(t)
	g := w.item("main", "KernelEnd", types.KindGlobal, &types.GlobalDecl{Type: types.U8, Extern: true})
	ref := w.tab.Ref(types.U8)
	read := w.constItem("main", "ReadIt", types.U8, 0)
	x := w.body(read, types.U8)
	p := x.local(ref)
	x.assign(0, lp(p), mir.Rvalue{Kind: mir.RvGlobalRef, Item: g})
	x.assign(0, lp(0), use(mir.Copy(x.b.Place(p, mir.Projection{Kind: mir.ProjDeref, Type: types.U8}))))
	x.ret(0)
	x.done()
	wantRefusal(t, w.evaluator(0).Eval(constReq(w, read, types.StageBodies)), types.RefuseGlobal)
}
