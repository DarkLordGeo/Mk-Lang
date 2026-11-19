// Why: the systems forms (asm, naked, align, section, extern var) have their
// own grammar corners that ordinary parse tests never reach.
// What: parse tests of the asm statement and its operand forms, naked,
// align(n), the section clause after a const or var initializer, and extern var.
// How: parses short sources and inspects the AsmStmt and declaration nodes.

package syntax

import (
	"strings"
	"testing"

	"mk/internal/ast"
)

// asmIn returns the first AsmStmt of the file, looking inside unsafe blocks.
func asmIn(t *testing.T, p parsed) *ast.AsmStmt {
	t.Helper()
	var found *ast.AsmStmt
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if found != nil {
			return
		}
		if a, ok := n.(*ast.AsmStmt); ok {
			found = a
			return
		}
		ast.EachChild(n, walk)
	}
	walk(p.file)
	if found == nil {
		t.Fatalf("no asm statement in:\n%s", p.tree())
	}
	return found
}

func TestAsmOperandForms(t *testing.T) {
	src := "F() {\n\tunsafe \"r\" {\n\t\tasm(\"mov {t:r}, rbx\", t: out(reg) b, inout(\"eax\") a, in(reg) p, lateout(reg) _, inlateout(xmm_reg) v, k: const 4, s: sym m.G, clobber_abi(\"C\"), options(nomem, nostack, preserves_flags),)\n\t}\n}\n"
	p := parse(t, src, Options{})
	if len(p.diags) != 0 {
		t.Fatalf("diagnostics: %v", p.codes())
	}
	a := asmIn(t, p)
	if a.Template.Raw != `"mov {t:r}, rbx"` {
		t.Fatalf("template %q", a.Template.Raw)
	}
	want := []struct {
		kind    ast.AsmOperandKind
		label   string
		reg     string
		discard bool
	}{
		{ast.AsmOut, "t", "reg", false},
		{ast.AsmInOut, "", `"eax"`, false},
		{ast.AsmIn, "", "reg", false},
		{ast.AsmLateOut, "", "reg", true},
		{ast.AsmInLateOut, "", "xmm_reg", false},
		{ast.AsmConst, "k", "", false},
		{ast.AsmSym, "s", "", false},
		{ast.AsmClobberABI, "", "", false},
		{ast.AsmOptions, "", "", false},
	}
	if len(a.Operands) != len(want) {
		t.Fatalf("%d operands, want %d", len(a.Operands), len(want))
	}
	for i, w := range want {
		op := a.Operands[i]
		if op.Kind != w.kind {
			t.Errorf("operand %d: kind %v, want %v", i, op.Kind, w.kind)
		}
		label := ""
		if op.Label != nil {
			label = p.in.Str(op.Label.Name)
		}
		if label != w.label {
			t.Errorf("operand %d: label %q, want %q", i, label, w.label)
		}
		reg := ""
		switch r := op.Reg.(type) {
		case *ast.Ident:
			reg = p.in.Str(r.Name)
		case *ast.BasicLit:
			reg = r.Raw
		}
		if reg != w.reg {
			t.Errorf("operand %d: reg %q, want %q", i, reg, w.reg)
		}
		if op.Discard != w.discard {
			t.Errorf("operand %d: discard %v", i, op.Discard)
		}
		if w.kind.IsDirection() && !w.discard && op.Value == nil {
			t.Errorf("operand %d: no value", i)
		}
	}
	if s := a.Operands[6].Sym; s == nil || len(s.Names) != 2 || a.Operands[6].Value != nil {
		t.Errorf("sym operand: %+v", a.Operands[6])
	}
	if a.Operands[5].Value == nil {
		t.Error("const operand has no value")
	}
	if a.Operands[7].ABI == nil || a.Operands[7].ABI.Raw != `"C"` {
		t.Error("clobber_abi string missing")
	}
	if n := len(a.Operands[8].Options); n != 3 {
		t.Errorf("%d options", n)
	}
	for _, bad := range []string{
		"F() { asm(x, in(reg) y) }\n",                // template not a string
		"F() { asm(b\"x\") }\n",                      // byte string
		"F() { asm(\"\", in(reg)) }\n",               // no value
		"F() { asm(\"\", out() x) }\n",               // no register
		"F() { asm(\"\", t: options(att)) }\n",       // a label on options
		"F() { asm(\"\", t: clobber_abi(\"C\")) }\n", // a label on clobber_abi
		"F() { asm(\"\", over(reg) x) }\n",           // unknown direction
		"F() { asm(\"\", options()) }\n",             // no option
		"F() { asm }\n",
	} {
		if p := parse(t, bad, Options{}); len(p.diags) == 0 {
			t.Errorf("no diagnostic for %q", bad)
		} else if !strings.HasPrefix(p.codeList(), "E0101") {
			t.Errorf("%q: %s", bad, p.codeList())
		}
	}
}

func TestAsmTemplateRawString(t *testing.T) {
	p := parse(t, "F() {\n\tunsafe \"r\" {\n\t\tasm(r\"lea {r}, [rip + {s}]\", r: out(reg) x, s: sym G)\n\t}\n}\n", Options{})
	if len(p.diags) != 0 {
		t.Fatalf("diagnostics: %v", p.codes())
	}
	if a := asmIn(t, p); a.Template.Kind != ast.TokRawStr {
		t.Fatalf("template kind %v", a.Template.Kind)
	}
}

// The cookbook's interrupt recipe: a """ template whose operands go on on
// the closing line.
func TestAsmTripleQuoteTemplate(t *testing.T) {
	src := "naked Int3Stub() {\n\tunsafe \"entry\" {\n\t\tasm(\"\"\"\n\t\t\tpush rax\n\t\t\tcall {h}\n\t\t\tpop rax\n\t\t\tiretq\n\t\t\t\"\"\", h: sym OnBreakpoint)\n\t}\n}\n"
	p := parse(t, src, Options{})
	if len(p.diags) != 0 {
		t.Fatalf("diagnostics: %v", p.codes())
	}
	a := asmIn(t, p)
	if a.Template.Kind != ast.TokMultiStr || len(a.Operands) != 1 || a.Operands[0].Kind != ast.AsmSym {
		t.Fatalf("template %v, operands %d", a.Template.Kind, len(a.Operands))
	}
	if !p.file.Items[0].(*ast.FuncDecl).Naked {
		t.Fatal("naked not recorded")
	}
}

func TestAsmIsKeyword(t *testing.T) {
	if got := identKind("asm"); got != ast.TokAsm {
		t.Fatalf("asm lexes as %v", got)
	}
	if p := parse(t, "asm() {\n}\n", Options{}); len(p.diags) == 0 {
		t.Error("a function named asm accepted")
	}
	for _, src := range []string{"F() {\n\tasm := 1\n}\n", "struct S {\n\tasm: int\n}\n"} {
		p := parse(t, src, Options{})
		if !strings.Contains(p.codeList(), "E0102") {
			t.Errorf("%q: %s, want E0102", src, p.codeList())
		}
	}
	// asm in expression position is no call.
	if p := parse(t, "F() {\n\tx := asm(\"nop\")\n}\n", Options{}); len(p.diags) == 0 {
		t.Error("asm accepted as an expression")
	}
}

func TestNakedForms(t *testing.T) {
	src := "naked Entry() {\n}\nexport extern naked Halt() {\n}\nnaked(x: int): int {\n\treturn x\n}\nextern naked(x: int): int {\n\treturn x\n}\n"
	p := parse(t, src, Options{})
	if len(p.diags) != 0 {
		t.Fatalf("diagnostics: %v", p.codes())
	}
	if fd := p.file.Items[0].(*ast.FuncDecl); !fd.Naked || p.in.Str(fd.Sig.Name.Name.Name) != "Entry" {
		t.Error("naked Entry")
	}
	if ed := p.file.Items[1].(*ast.ExternDecl); !ed.Naked || !ed.Export {
		t.Error("export extern naked Halt")
	}
	if fd := p.file.Items[2].(*ast.FuncDecl); fd.Naked || p.in.Str(fd.Sig.Name.Name.Name) != "naked" {
		t.Error("a function named naked")
	}
	if ed := p.file.Items[3].(*ast.ExternDecl); ed.Naked {
		t.Error("an extern named naked")
	}
}

func TestAlignStructOrFunc(t *testing.T) {
	src := "align(4096) struct PageTable {\n\texport entries: [512]u64\n}\npacked align(8) struct Both {\n\tx: u8\n}\nalign(8) packed linear struct L {\n}\nalign(x: int): int {\n\treturn x\n}\n"
	p := parse(t, src, Options{})
	if len(p.diags) != 0 {
		t.Fatalf("diagnostics: %v", p.codes())
	}
	if sd := p.file.Items[0].(*ast.StructDecl); sd.Align == nil || sd.Align.Align.Raw != "4096" || sd.Packed != nil {
		t.Error("align(4096) struct")
	}
	if sd := p.file.Items[1].(*ast.StructDecl); sd.Align == nil || sd.Packed == nil {
		t.Error("packed align(8) struct")
	}
	if sd := p.file.Items[2].(*ast.StructDecl); sd.Align == nil || sd.Packed == nil || !sd.Linear {
		t.Error("align(8) packed linear struct")
	}
	if _, ok := p.file.Items[3].(*ast.FuncDecl); !ok {
		t.Errorf("a function named align: %T", p.file.Items[3])
	}
	if p := parse(t, "align(8) align(16) struct S {\n}\n", Options{}); len(p.diags) == 0 {
		t.Error("align written twice accepted")
	}
}

func TestSectionClauses(t *testing.T) {
	src := "const Header: [3]u32 = [0x1BADB002, 0, 0xE4524FFE] section \".multiboot\"\nconst C: int = F() fuel 10 section r\".x\"\nvar V: u8 = 1 section \".data.v\"\nconst D = 1\nG() section \".text.boot\" {\n}\n"
	p := parse(t, src, Options{})
	if len(p.diags) != 0 {
		t.Fatalf("diagnostics: %v", p.codes())
	}
	if c := p.file.Items[0].(*ast.ConstDecl); c.Section == nil || c.Section.Raw != `".multiboot"` {
		t.Error("const section")
	}
	if c := p.file.Items[1].(*ast.ConstDecl); c.Section == nil || c.Fuel == nil {
		t.Error("const fuel section")
	}
	if g := p.file.Items[2].(*ast.GlobalDecl); g.Section == nil || g.Extern {
		t.Error("var section")
	}
	if c := p.file.Items[3].(*ast.ConstDecl); c.Section != nil {
		t.Error("section invented")
	}
	if f := p.file.Items[4].(*ast.FuncDecl); f.Sig.Section == nil {
		t.Error("function section")
	}
	// section followed by no string is no clause.
	if p := parse(t, "const C: int = 1 section\n", Options{}); len(p.diags) == 0 {
		t.Error("a bare section accepted")
	}
}

func TestExternVar(t *testing.T) {
	p := parse(t, "extern var KernelEnd: u8\nexport extern var Stack: [4096]u8\n", Options{})
	if len(p.diags) != 0 {
		t.Fatalf("diagnostics: %v", p.codes())
	}
	g := p.file.Items[0].(*ast.GlobalDecl)
	if !g.Extern || g.Value != nil || g.Type == nil || p.in.Str(g.Name.Name) != "KernelEnd" {
		t.Errorf("extern var: %+v", g)
	}
	if g := p.file.Items[1].(*ast.GlobalDecl); !g.Extern || !g.Export {
		t.Error("export extern var")
	}
	for _, bad := range []string{"extern var E: u8 = 1\n", "extern var E\n", "extern var\n"} {
		if p := parse(t, bad, Options{}); len(p.diags) == 0 {
			t.Errorf("no diagnostic for %q", bad)
		}
	}
}
