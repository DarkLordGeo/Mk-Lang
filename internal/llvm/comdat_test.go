// Why: codegen's FuncTable rows sit in a section the linker must drop with
// their function, which takes comdat, !associated and llvm.compiler.used.
// What: TestLLVMComdatAssociated and TestLLVMModuleFlagMD.
// How: builds a module with such a row and checks its printed COFF and ELF
// forms.

package llvm

import (
	"strings"
	"testing"
)

// rowModule defines f and a row { ptr @f } in section sec: on COFF in f's
// comdat (an associative section), elsewhere with !associated !{ptr @f};
// both rows are in @llvm.compiler.used.
func rowModule(tg target) *Module {
	m := NewModule("rows", tg.triple, tg.layout)
	coff := tg.os == "windows"
	f := m.AddFunction("mk.main.F", FuncSig{Linkage: External, DSOLocal: true, Ret: m.Void()})
	f.NewBlock("entry").Ret(nil)
	g := m.AddFunction("mk.main.G", FuncSig{Linkage: Internal, Ret: m.Void()})
	g.NewBlock("entry").Ret(nil)
	var rows []Value
	for _, fn := range []*Function{f, g} {
		a := GlobalAttrs{Linkage: Private, Constant: true, Align: 8, Type: m.Struct(false, m.Ptr()),
			Init: m.ConstStruct(false, m.FuncRef(fn))}
		switch {
		case coff:
			m.Comdat(fn.Name())
			m.Comdat(fn.Name()) // a no-op
			fn.SetComdat(fn.Name())
			a.Section, a.Comdat = ".mkfn$M", fn.Name()
		case tg.os == "linux":
			a.Section, a.Associated = "mk_funcs", m.FuncRef(fn)
		}
		rows = append(rows, m.GlobalRef(m.AddGlobal("row."+fn.Name(), a)))
	}
	m.CompilerUsed(rows...)
	return m
}

// TestLLVMComdatAssociated: the printed forms, and every clang compiles them.
func TestLLVMComdatAssociated(t *testing.T) {
	coff := rowModule(smokeTriples[0]).String()
	for _, want := range []string{
		"$mk.main.F = comdat nodeduplicate\n$mk.main.G = comdat nodeduplicate\n",
		"@row.mk.main.F = private constant { ptr } { ptr @mk.main.F }, section \".mkfn$M\", comdat($mk.main.F), align 8\n",
		"define dso_local void @mk.main.F() comdat($mk.main.F) {",
		"define internal void @mk.main.G() comdat($mk.main.G) {",
		"@llvm.compiler.used = appending global [2 x ptr] [ptr @row.mk.main.F, ptr @row.mk.main.G], section \"llvm.metadata\"\n",
	} {
		if !strings.Contains(coff, want) {
			t.Errorf("COFF: want %q in\n%s", want, coff)
		}
	}
	elf := rowModule(smokeTriples[1]).String()
	for _, want := range []string{
		"@row.mk.main.F = private constant { ptr } { ptr @mk.main.F }, section \"mk_funcs\", align 8, !associated !0\n",
		"!0 = !{ptr @mk.main.F}", "!1 = !{ptr @mk.main.G}",
	} {
		if !strings.Contains(elf, want) {
			t.Errorf("ELF: want %q in\n%s", want, elf)
		}
	}
	if strings.Contains(elf, "comdat") {
		t.Error("ELF rows need no comdat")
	}
	m := NewModule("bad", "", "")
	f := m.AddFunction("f", FuncSig{Ret: m.Void()})
	f.NewBlock("entry").Ret(nil)
	mustPanicLLVM(t, "undeclared comdat", func() { f.SetComdat("f") })
	mustPanicLLVM(t, "global in an undeclared comdat", func() {
		m.AddGlobal("g", GlobalAttrs{Linkage: Private, Type: m.I(8), Init: m.ConstInt(m.I(8), 0), Comdat: "f"})
	})
	mustPanicLLVM(t, "associated declaration", func() {
		m.AddGlobal("h", GlobalAttrs{Type: m.I(8), Associated: m.FuncRef(f)})
	})
	mustPanicLLVM(t, "comdat of a declaration", func() { m.Declare("d", FuncSig{Ret: m.Void()}).SetComdat("f") })
	smokeAll(t, rowModule)
}

// TestLLVMModuleFlagMD: a module flag whose value is a node prints as
// !{i32 1, !"key", !N}, once per key; another value for the key panics.
func TestLLVMModuleFlagMD(t *testing.T) {
	m := NewModule("flags", "", "")
	mt := m.Metadata()
	v := mt.Tuple(MDRef(mt.Tuple(MDStr("ProfileFormat"), MDStr("InstrProf"))))
	mt.ModuleFlagMD(FlagError, "ProfileSummary", v)
	mt.ModuleFlagMD(FlagError, "ProfileSummary", v)
	s := m.String()
	if !strings.Contains(s, `!llvm.module.flags = !{!2}`) || !strings.Contains(s, `!2 = !{i32 1, !"ProfileSummary", !1}`) {
		t.Errorf("flag not printed once:\n%s", s)
	}
	mustPanicLLVM(t, "another value", func() { mt.ModuleFlagMD(FlagError, "ProfileSummary", mt.Tuple()) })
}
