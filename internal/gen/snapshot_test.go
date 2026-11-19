// Why: the codes gen emits itself have a fixed JSON form that tools read.
// What: JSON snapshots of E0903, E0905 and E0906 under
// internal/gen/testdata/ (E0901, E0902, E0908 and E0909 are eval's).
// How: snapshot writes the diagnostics through diag's JSON form; each test
// compares it with its golden.

package gen

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// snapshot renders a group of diagnostics as one JSON document of the
// driver's --json form (diag.Order, then WriteJSON), ending in a newline.
func snapshot(w *world, ds []*diag.Diagnostic) string {
	return string(diag.Order(ds, w.sm, diag.NoCap).WriteJSON(w.sm))
}

// TestE0903_Snapshot: Fail at the gen line, FailAt at the field's
// declaration, FailOn at the type's declaration, from one run.
func TestE0903_Snapshot(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("lib", "User", true, "Name", types.Str)
	g := w.gen1("Check", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		fr, b := x.fieldAt(0, tr, 0)
		f := x.local(w.named(w.known.Field))
		x.assign(b, lp(f), use(mir.Copy(x.deref(fr))))
		_, b2 := x.api(b, "Gen:FailAt", cp(1), cp(f), w.strConst("field Name: str has no Eq"))
		ty := x.local(w.typeT())
		x.assign(b2, lp(ty), use(mir.Copy(x.deref(tr))))
		_, b3 := x.api(b2, "Gen:FailOn", cp(1), cp(ty), w.strConst("User is not a value type"))
		_, b4 := x.api(b3, "Gen:Fail", cp(1), w.strConst("derive.Check needs a struct"))
		return b4
	})
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.Check", g, typeArg(user))})
	wantCodes(t, out, "E0903", "E0903", "E0903")
	golden(t, "e0903.golden", snapshot(w, out.Diags))
}

// TestE0905_Snapshot: a parse error in output, a gen item in output, and a
// later phase's diagnostic inside a generated item through Wrap. Each is its
// own document, one per line, since the phase rule keeps only the earliest
// phase of one; each has its own world, so each output is the module's one
// gen.mk.
func TestE0905_Snapshot(t *testing.T) {
	bad := func(name, text string) string {
		w := newWorld(t)
		g := w.generator("lib", name)
		x := w.body(g)
		q, b1 := x.quote(0, text)
		_, b2 := x.api(b1, "Gen:Emit", cp(1), cp(q))
		x.ret(b2)
		x.done()
		out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib."+name, g)})
		if len(out.Diags) == 0 || out.Diags[0].Code != diag.E0905 {
			t.Fatalf("not an E0905: %v", out.Diags)
		}
		return snapshot(w, out.Diags)
	}
	parse := bad("Bad", "\nstruct S {\na: \n}\n")
	genIn := bad("Nested", "\ngen derive.Eq(S)\n")
	w := newWorld(t)
	_, user := w.structType("main", "User", false, "a", types.Int)
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "derive.Eq", w.deriveEq(), typeArg(user))})
	wantCodes(t, out)
	at := out.Items[0].Output
	at.Start += 5
	at.End = at.Start + 2
	wrapped := Wrap(diag.New(diag.E0301, at, "mismatched types: expected bool, found int").In(diag.PhaseTypes), out)
	if wrapped.Code != diag.E0905 {
		t.Fatalf("not an E0905: %v", wrapped)
	}
	golden(t, "e0905.golden", parse+genIn+snapshot(w, []*diag.Diagnostic{wrapped}))
}

// TestE0906_Snapshot: a structure read of a type only generated code
// defines, at the gen line, and a gen argument naming a generated item, at
// the argument; both kept by Finish once the names resolve to generated
// items of the module.
func TestE0906_Snapshot(t *testing.T) {
	w := newWorld(t)
	_, s := w.structType("main", "S", false, "a", "Made")
	deep := w.gen1("Deep", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		fr, b := x.fieldAt(0, tr, 0)
		ft, b2 := x.api(b, "Field:Type", cp(fr))
		ftr := x.ref(ft, false)
		x.flush(b2)
		_, b3 := x.api(b2, "Type:Fields", cp(ftr))
		return b3
	})
	plain := w.gen1("Plain", func(x *bb, tr, gr mir.LocalID) mir.BlockID { return x.emit(0, "const K = 1") })
	made := w.item("main", "Made", types.KindStruct, &types.StructDecl{})
	w.items.Get(made).Origin = &types.GenOrigin{}
	arg := Arg{Kind: ArgDeferred, Name: w.in.Intern("Made"), Span: w.span()}
	c := w.context(1)
	out := c.Run(w.module("main"), []Line{w.line(0, "lib.Deep", deep, typeArg(s)), w.line(1, "lib.Plain", plain, arg)})
	wantCodes(t, out)
	ds := c.Finish(out, func(n source.Symbol) types.ItemID {
		if w.in.Str(n) == "Made" {
			return made
		}
		return types.NoItem
	})
	if len(ds) != 2 || ds[0].Code != diag.E0906 || ds[1].Code != diag.E0906 {
		t.Fatalf("Finish: %v", ds)
	}
	golden(t, "e0906.golden", snapshot(w, ds))
}
