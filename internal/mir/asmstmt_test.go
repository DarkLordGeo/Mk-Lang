// Why: inline asm reaches MIR as one statement whose operands the validator
// must check before codegen trusts them.
// What: TestAsmStatement over asmBody, for StAsm.
// How: a well-formed asm validates and prints; each broken form is invariant
// M13 (M2 for an index out of range).

package mir

import (
	"strings"
	"testing"

	"mk/internal/types"
)

// asmBody is Show with one asm in bb1, before its assignment:
//
//	asm "mov {1}, {0}", in(reg) copy _1, out("rax") _4, const 7, sym Load,
//	    clobber_abi("C"), options(nomem, nostack)
//
// _4 is a fresh i64 local.
func asmBody(f *fixture) *Body {
	b := f.showBody()
	sp := b.Blocks[1].Term.Span
	out := b.AddLocal(Local{Type: types.I64, Kind: LocalTemp, Loop: NoLoop, Span: sp})
	seven := f.consts.Intern(types.ConstValue{Type: types.I64, Align: 8, Bytes: []byte{7, 0, 0, 0, 0, 0, 0, 0}})
	id := b.AddAsm(Asm{Template: "mov {1}, {0}", ClobberABI: "C", Options: AsmNomem | AsmNostack, Span: sp, Operands: []AsmOperand{
		{Kind: AsmIn, Reg: "reg", In: Copy(LocalPlace(1)), Type: types.I64},
		{Kind: AsmOut, Reg: "rax", Explicit: true, Out: LocalPlace(out), Type: types.I64},
		{Kind: AsmConst, Const: seven, Type: types.I64},
		{Kind: AsmSym, Sym: f.load},
	}})
	b.Blocks[1].Stmts = append([]Statement{{Kind: StAsm, Asm: id, Span: sp}}, b.Blocks[1].Stmts...)
	return b
}

// TestAsmStatement: a well-formed asm validates and prints; each broken
// form is M13 (M2 for an index out of range); a noreturn asm must end its
// block before unreachable; with Known naming Vec, a struct operand must be
// a Vec or Mask.
func TestAsmStatement(t *testing.T) {
	f := newFixture()
	b := asmBody(f)
	if err := b.Validate(f.env()); err != nil {
		t.Fatalf("asm refused: %v", err)
	}
	got := (&Printer{Types: f.tab, Items: f.items}).Print(b)
	if !strings.Contains(got, `asm "mov {1}, {0}", in(reg) copy _1, out("rax") _4, const `) ||
		!strings.Contains(got, `, clobber_abi("C"), options(nomem, nostack)`) {
		t.Errorf("printer:\n%s", got)
	}
	for name, brk := range map[string]func(f *fixture, b *Body){
		"in type differs":     func(f *fixture, b *Body) { b.Asms[0].Operands[0].Type = types.I32 },
		"reference operand":   func(f *fixture, b *Body) { b.Asms[0].Operands[0].Type = f.tab.Ref(types.I64) },
		"str operand":         func(f *fixture, b *Body) { b.Asms[0].Operands[1].Type = types.Str },
		"out place differs":   func(f *fixture, b *Body) { b.Asms[0].Operands[1].Type = types.U64 },
		"sym on in":           func(f *fixture, b *Body) { b.Asms[0].Operands[0].Sym = f.load },
		"sym missing":         func(f *fixture, b *Body) { b.Asms[0].Operands[3].Sym = types.NoItem },
		"const not integer":   func(f *fixture, b *Body) { b.Asms[0].Operands[2].Type = types.F64 },
		"const missing":       func(f *fixture, b *Body) { b.Asms[0].Operands[2].Const = types.NoConst },
		"direction no reg":    func(f *fixture, b *Body) { b.Asms[0].Operands[0].Reg = "" },
		"const with reg":      func(f *fixture, b *Body) { b.Asms[0].Operands[2].Reg = "reg" },
		"operand kind 0":      func(f *fixture, b *Body) { b.Asms[0].Operands[0].Kind = 0 },
		"operand kind past":   func(f *fixture, b *Body) { b.Asms[0].Operands[3].Kind = AsmSym + 1 },
		"unknown clobber_abi": func(f *fixture, b *Body) { b.Asms[0].ClobberABI = "fastcall" },
		"unknown option":      func(f *fixture, b *Body) { b.Asms[0].Options |= AsmRaw << 1 },
		"noreturn mid-block":  func(f *fixture, b *Body) { b.Asms[0].Options |= AsmNoreturn },
	} {
		f := newFixture()
		b := asmBody(f)
		brk(f, b)
		if err := b.Validate(f.env()); err == nil || !strings.Contains(err.Error(), "M13: ") {
			t.Errorf("%s: want M13, got %v", name, err)
		}
	}
	f = newFixture()
	b = asmBody(f)
	b.Blocks[1].Stmts[0].Asm = 1
	if err := b.Validate(f.env()); err == nil || !strings.Contains(err.Error(), "M2: ") {
		t.Errorf("asm index out of range: want M2, got %v", err)
	}

	// A noreturn asm alone in a block ending in unreachable.
	f = newFixture()
	b = asmBody(f)
	b.Asms[0].Options |= AsmNoreturn
	b.Blocks[1].Stmts = b.Blocks[1].Stmts[:1]
	b.Blocks[1].Term = Terminator{Kind: TermUnreachable, Span: b.Blocks[1].Term.Span}
	if err := b.Validate(f.env()); err != nil {
		t.Fatalf("noreturn asm before unreachable refused: %v", err)
	}

	// With Known, a named operand type must be std.simd's Vec or Mask.
	f = newFixture()
	m := f.items.Get(f.show).Module
	vec := f.items.AddItem(&types.Item{Module: m, Name: f.in.Intern("Vec"), Kind: types.KindStruct, Key: types.ItemKey("std.simd", "Vec")})
	other := f.items.AddItem(&types.Item{Module: m, Name: f.in.Intern("Other"), Kind: types.KindStruct, Key: types.ItemKey("main", "Other")})
	for _, tc := range []struct {
		item types.ItemID
		ok   bool
	}{{vec, true}, {other, false}} {
		b = asmBody(f)
		nt := f.tab.Named(tc.item, nil)
		v := b.AddLocal(Local{Type: nt, Kind: LocalTemp, Loop: NoLoop, Span: 0})
		b.Asms[0].Operands[0] = AsmOperand{Kind: AsmIn, Reg: "xmm_reg", In: Copy(LocalPlace(v)), Type: nt}
		env := f.env()
		env.Known = &types.KnownItems{Vec: vec}
		err := b.Validate(env)
		if tc.ok != (err == nil) || (err != nil && !strings.Contains(err.Error(), "M13: ")) {
			t.Errorf("operand of %s: ok %v, got %v", f.tab.TypeString(nt, nil), tc.ok, err)
		}
	}
	if StAsm != StNop+1 {
		t.Errorf("StAsm %d must follow StNop %d (iface encodes the kind)", StAsm, StNop)
	}
}
