// Why: the systems fields (cpu sets, naked, link sections, extern, alignment,
// asm) change generated code, so they must reach the cache and the hashes.
// What: tests of sys.go: Signature.CPU, the systems fields of
// StructDecl, ConstDecl, GlobalDecl and FuncDecl, ShapeTerm.Align, a body's asm
// statements, InstanceNameCPU, and a ReadTarget read of vector_bits.
// How: each round-trips in store form into the same session and a fresh one
// with other IDs (equal hashes, equal re-encoded bytes) and is in its hash.

package iface

import (
	"bytes"
	"reflect"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// cpuSig is geo.Add's signature with a cpu clause's closure.
func cpuSig(s *session) types.Signature {
	sig := s.items.Get(s.id("geo.Add")).Decl.(*types.FuncDecl).Sig
	sig.CPU = []string{"avx", "avx2", "sse", "sse2"}
	return sig
}

func TestSignatureCPU(t *testing.T) {
	a := withDecls(newGeo(false))
	sig := cpuSig(a)
	write := func(w *walker) { w.writeSig(&sig, false) }
	var got types.Signature
	read := func(r *reader, d *Decoder) { got = r.readSig(d) }
	if _, err := storeTrip(a, a.env, write, read); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, sig) {
		t.Errorf("same-session signature:\n got %+v\nwant %+v", got, sig)
	}
	b := withDecls(newGeo(true))
	enc, err := storeTrip(a, b.env, write, read)
	if err != nil {
		t.Fatal(err)
	}
	if a.h.Signature(&sig) != b.h.Signature(&got) {
		t.Error("signature hash differs across sessions")
	}
	if re, err := storeTrip(b, b.env, func(w *walker) { w.writeSig(&got, false) }, read); err != nil || !bytes.Equal(re, enc) {
		t.Errorf("re-encoding the signature changed the bytes (err %v)", err)
	}
	plain := a.items.Get(a.id("geo.Add")).Decl.(*types.FuncDecl).Sig
	if a.h.Signature(&plain) == a.h.Signature(&sig) {
		t.Error("a cpu clause left the signature hash equal")
	}
	other := cpuSig(a)
	other.CPU = []string{"sse", "sse2"}
	if a.h.Signature(&other) == a.h.Signature(&sig) {
		t.Error("two feature sets hash equal")
	}
	// No clause decodes as nil, as check leaves it.
	if _, err := storeTrip(a, a.env, func(w *walker) { w.writeSig(&plain, false) }, read); err != nil || got.CPU != nil {
		t.Errorf("no clause: err %v, CPU %#v", err, got.CPU)
	}
}

// sysDecls are one declaration of each kind with systems fields, with its new
// fields set.
func sysDecls(s *session) []types.Decl {
	sv := s.uintConst(9)
	return []types.Decl{
		&types.StructDecl{Fields: []types.FieldDecl{{Name: s.in.Intern("x"), Type: types.U64, Exported: true}}, Align: 4096},
		&types.ConstDecl{Type: types.Uint, Fuel: 5, Value: sv, HasValue: true, Section: ".rodata.tags"},
		&types.GlobalDecl{Type: types.Uint, Extern: true, Section: ""},
		&types.GlobalDecl{Type: types.Uint, Value: sv, HasValue: true, Section: ".data.boot"},
		&types.FuncDecl{Sig: cpuSig(s), Naked: true, Section: ".text.boot"},
	}
}

func TestDeclFieldsRoundTrip(t *testing.T) {
	a := withDecls(newGeo(false))
	ds := sysDecls(a)
	writeAll := func(ds []types.Decl) func(w *walker) {
		return func(w *walker) {
			w.e.Uvarint(uint64(len(ds)))
			for _, d := range ds {
				w.writeDecl(d)
			}
		}
	}
	var got []types.Decl
	read := func(r *reader, d *Decoder) {
		got = nil
		n := d.Count(1)
		for i := 0; i < n; i++ {
			got = append(got, r.readDecl(d))
		}
	}
	if _, err := storeTrip(a, a.env, writeAll(ds), read); err != nil {
		t.Fatal(err)
	}
	for i := range ds {
		if i >= len(got) || !reflect.DeepEqual(got[i], ds[i]) {
			t.Errorf("decl %d (%T) did not round-trip: got %+v", i, ds[i], got)
		}
	}
	b := withDecls(newGeo(true))
	enc, err := storeTrip(a, b.env, writeAll(ds), read)
	if err != nil {
		t.Fatal(err)
	}
	if re, err := storeTrip(b, b.env, writeAll(got), read); err != nil || !bytes.Equal(re, enc) {
		t.Errorf("re-encoding the decls changed the bytes (err %v)", err)
	}
}

// TestDeclHashes: each new field is in mk.decl.v1, and a function with
// neither naked nor section keeps its mk.sig.v1 hash as its decl hash.
func TestDeclHashes(t *testing.T) {
	s := withDecls(newGeo(false))
	add := s.items.Get(s.id("geo.Add"))
	fd := add.Decl.(*types.FuncDecl)
	if s.h.Decl(add) != s.h.Signature(&fd.Sig) {
		t.Error("a plain function's decl hash is not its signature hash")
	}
	for _, c := range []struct {
		name   string
		it     *types.Item
		change func()
	}{
		{"naked", add, func() { fd.Naked = true }},
		{"function section", add, func() { fd.Section = ".text.x" }},
		{"cpu", add, func() { fd.Sig.CPU = []string{"avx2"} }},
		{"align", s.items.Get(s.id("geo.Point")), func() { s.items.Get(s.id("geo.Point")).Decl.(*types.StructDecl).Align = 64 }},
	} {
		before := s.h.Decl(c.it)
		c.change()
		if s.h.Decl(c.it) == before {
			t.Errorf("%s: decl hash unchanged", c.name)
		}
	}
	// Const and global items: section and extern.
	cit := &types.Item{Kind: types.KindConst, Key: types.KeyFromString("geo.Tag"), Decl: &types.ConstDecl{Type: types.Uint}}
	before := s.h.Decl(cit)
	cit.Decl.(*types.ConstDecl).Section = ".rodata.tags"
	if s.h.Decl(cit) == before {
		t.Error("const section: decl hash unchanged")
	}
	git := &types.Item{Kind: types.KindGlobal, Key: types.KeyFromString("geo.Stack"), Decl: &types.GlobalDecl{Type: types.Uint}}
	before = s.h.Decl(git)
	git.Decl.(*types.GlobalDecl).Extern = true
	ext := s.h.Decl(git)
	if ext == before {
		t.Error("extern var: decl hash unchanged")
	}
	git.Decl.(*types.GlobalDecl).Section = ".bss.stack"
	if s.h.Decl(git) == ext {
		t.Error("global section: decl hash unchanged")
	}
}

func TestShapeTermAlign(t *testing.T) {
	a := withDecls(newGeo(false))
	leaf := types.ShapeTerm{Kind: types.TermLeaf, Leaf: types.Shape{Size: 8, Align: 8}}
	term := types.ShapeTerm{Kind: types.TermStruct, Fields: []types.ShapeTerm{leaf}, Align: 4096}
	var got types.ShapeTerm
	read := func(r *reader, d *Decoder) { got = r.readTerm(d, 0) }
	if _, err := storeTrip(a, a.env, func(w *walker) { w.writeTerm(&term) }, read); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, term) {
		t.Errorf("term: got %+v, want %+v", got, term)
	}
	plain := term
	plain.Align = 0
	if a.h.ShapeTerm(&plain) == a.h.ShapeTerm(&term) {
		t.Error("align(n) left the shape term hash equal")
	}
}

// asmBody is a small body with two asm statements: every operand kind, a
// const, a sym naming an item, and options.
func asmBody(s *session) *mir.Body {
	b := retBody(s, s.id("geo.Main"), types.Void)
	sp := b.AddSpan(b.Spans[0])
	x := b.AddLocal(mir.Local{Type: types.U64, Kind: mir.LocalUser, Name: s.in.Intern("x"), Var: true, Loop: mir.NoLoop, Span: sp})
	a0 := b.AddAsm(mir.Asm{Template: "mov {0}, {1}\nadd {0}, {2}", ClobberABI: "C", Span: sp,
		Options: mir.AsmNomem | mir.AsmNostack | mir.AsmPreservesFlags,
		Operands: []mir.AsmOperand{
			{Kind: mir.AsmOut, Reg: "reg", Out: mir.LocalPlace(x), Type: types.U64},
			{Kind: mir.AsmIn, Reg: "rax", Explicit: true, In: mir.Copy(mir.LocalPlace(x)), Type: types.U64},
			{Kind: mir.AsmConst, Const: s.uintConst(7), Type: types.Uint},
			{Kind: mir.AsmInOut, Reg: "rcx", Explicit: true, In: mir.Imm(types.U64, 1), Out: mir.LocalPlace(x), Type: types.U64},
			{Kind: mir.AsmLateOut, Reg: "rdx", Explicit: true, Out: mir.Place{Local: mir.NoLocal}, Type: types.U64},
			{Kind: mir.AsmInLateOut, Reg: "xmm_reg", In: mir.Copy(mir.LocalPlace(x)), Out: mir.LocalPlace(x), Type: types.U64},
		}})
	a1 := b.AddAsm(mir.Asm{Template: "call {0}", Span: sp, Options: mir.AsmNoreturn,
		Operands: []mir.AsmOperand{{Kind: mir.AsmSym, Sym: s.id("geo.Add")}}})
	b.Blocks[0].Stmts = append(b.Blocks[0].Stmts,
		mir.Statement{Kind: mir.StAsm, Asm: a0, Span: sp},
		mir.Statement{Kind: mir.StNop, Span: sp},
		mir.Statement{Kind: mir.StAsm, Asm: a1, Span: sp})
	return b
}

func TestBodyAsmsRoundTrip(t *testing.T) {
	a, _ := bodySession(false)
	body := asmBody(a)
	_, got, err := bodyTrip(a, a.env, body)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, body) {
		t.Errorf("same-session body differs:\n got %+v\nwant %+v", got, body)
	}
	b, _ := bodySession(true)
	enc, got, err := bodyTrip(a, b.env, body)
	if err != nil {
		t.Fatal(err)
	}
	if a.h.Body(body) != b.h.Body(got) {
		t.Error("body hash differs across sessions")
	}
	if re, _, err := bodyTrip(b, b.env, got); err != nil || !bytes.Equal(re, enc) {
		t.Errorf("re-encoding the body changed the bytes (err %v)", err)
	}
	before := a.h.Body(body)
	for _, change := range []func(c *mir.Body){
		func(c *mir.Body) { c.Asms[0].Template = "mov {0}, {1}" },
		func(c *mir.Body) { c.Asms[0].Options |= mir.AsmPure },
		func(c *mir.Body) { c.Asms[0].Operands[1].Reg = "rbx" },
		func(c *mir.Body) { c.Asms[1].Operands[0].Sym = a.id("geo.Main") },
		func(c *mir.Body) { c.Blocks[0].Stmts[0].Asm, c.Blocks[0].Stmts[2].Asm = 1, 0 },
	} {
		c := asmBody(a)
		change(c)
		if a.h.Body(c) == before {
			t.Errorf("an asm edit left the body hash equal: %+v", c.Asms)
		}
	}
}

func TestInstanceNameCPU(t *testing.T) {
	s := withDecls(newGeo(false))
	key := s.items.Get(s.id("geo.Wrap")).Key
	args := []types.GenericArg{types.TypeArg(types.I32)}
	base := s.h.InstanceName(key, args)
	if s.h.InstanceNameCPU(key, args, nil) != base || s.h.InstanceNameCPU(key, args, []string{}) != base {
		t.Error("InstanceNameCPU with no features is not InstanceName")
	}
	v3 := s.h.InstanceNameCPU(key, args, []string{"avx", "avx2"})
	if v3 == base || v3 == s.h.InstanceNameCPU(key, args, []string{"avx"}) {
		t.Error("a feature set does not change the instance name, or two sets agree")
	}
	if v3 != s.h.InstanceNameCPU(key, args, []string{"avx", "avx2"}) {
		t.Error("InstanceNameCPU is not stable")
	}
}

func TestTargetVectorBits(t *testing.T) {
	for _, tg := range []types.Target{types.X86_64Windows, types.X86_64Linux, types.Aarch64Linux} {
		v, ok := TargetValue(tg, types.TargetVectorBits.String())
		if !ok || v != "128" {
			t.Errorf("%s: vector_bits %q %v", tg.Name, v, ok)
		}
		if _, ok := TargetReadHash(tg, "vector_bits"); !ok {
			t.Errorf("%s: no ReadTarget hash for vector_bits", tg.Name)
		}
	}
}
