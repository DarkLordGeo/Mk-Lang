// Why: two Mk externs of one C name, or an extern of memcmp beside codegen's
// own memcmp, declare one C symbol with two function types in one module.
// What: TestDeclareAs and TestDeclareAsCompiles.
// How: DeclareAs gives one declaration and calls through views of it, never a
// panic, while Declare itself stays strict; clang compiles the result.

package llvm

import (
	"strings"
	"testing"
)

// declareAsModule: @Sleep declared as void (i32) and viewed as void (i64);
// @memcmp declared as i64 (i64, i64, i64) by a "user" and viewed as i32
// (ptr, ptr, i64) by "codegen"; @Def defined as i32 (i32) and viewed as
// i64 (i64); one function calls all of them.
func declareAsModule(tg target) (*Module, []*Function) {
	m := NewModule("cgr6", tg.triple, tg.layout)
	i32, i64, ptr := m.I(32), m.I(64), m.Ptr()
	ext := func(ret Type, ps ...Type) FuncSig {
		return FuncSig{Linkage: External, Ret: ret, Params: ps, FnAttrs: Attrs{}.Flag("nounwind")}
	}
	def := m.AddFunction("Def", FuncSig{Linkage: External, Ret: i32, Params: []Type{i32}})
	b := def.NewBlock("entry")
	p := def.Param(0)
	b.Ret(&p)
	s1 := m.DeclareAs("Sleep", ext(m.Void(), i32))
	s2 := m.DeclareAs("Sleep", ext(m.Void(), i64))
	s1again := m.DeclareAs("Sleep", ext(m.Void(), i32))
	u := m.DeclareAs("memcmp", ext(i64, i64, i64, i64))
	c := m.DeclareAs("memcmp", ext(i32, ptr, ptr, i64))
	dv := m.DeclareAs("Def", ext(i64, i64))
	f := m.AddFunction("Use", FuncSig{Linkage: External, Ret: i64, Params: []Type{ptr}})
	e := f.NewBlock("entry")
	e.CallFunc(s1, []Value{m.ConstInt(i32, 1)})
	e.CallFunc(s2, []Value{m.ConstInt(i64, 2)})
	r1 := e.CallFunc(u, []Value{m.ConstInt(i64, 0), m.ConstInt(i64, 0), m.ConstInt(i64, 0)})
	r2 := e.CallFunc(c, []Value{f.Param(0), f.Param(0), m.ConstInt(i64, 4)})
	r3 := e.CallFunc(dv, []Value{m.ConstInt(i64, 5)})
	x := e.Bin(Add, r1, e.Cast(SExt, r2, i64), 0)
	x = e.Bin(Add, x, r3, 0)
	e.Ret(&x)
	// A view's address is the symbol's.
	_ = m.FuncRef(s2)
	return m, []*Function{s1, s2, s1again, u, c, dv}
}

func TestDeclareAs(t *testing.T) {
	t.Parallel()
	m, fs := declareAsModule(target{})
	s1, s2, s1again, u, c := fs[0], fs[1], fs[2], fs[3], fs[4]
	if s1 != s1again {
		t.Error("DeclareAs of the same type is not the first declaration")
	}
	if s1 == s2 || s2.Type() == s1.Type() || u.Type() == c.Type() {
		t.Error("DeclareAs of another type is not a view with that type")
	}
	if s2.Ref().String() != "ptr @Sleep" || c.Ref().String() != "ptr @memcmp" {
		t.Errorf("view addresses %s, %s", s2.Ref(), c.Ref())
	}
	ll := m.String()
	for _, want := range []string{
		"declare void @Sleep(i32)", "declare i64 @memcmp(i64, i64, i64)",
		"call void @Sleep(i32 1)", "call void @Sleep(i64 2)",
		"call i64 @memcmp(i64 0, i64 0, i64 0)", "call i32 @memcmp(ptr %p0, ptr %p0, i64 4)", "call i64 @Def(i64 5)",
	} {
		if !strings.Contains(ll, want) {
			t.Errorf("no %q in\n%s", want, ll)
		}
	}
	for _, sym := range []string{"@Sleep(", "@memcmp(", "@Def("} {
		n := strings.Count(ll, "declare void "+sym) + strings.Count(ll, "declare i64 "+sym) + strings.Count(ll, "declare i32 "+sym)
		if n > 1 {
			t.Errorf("%s declared %d times", sym, n)
		}
	}
	if strings.Contains(ll, "declare i64 @Def") {
		t.Error("a view of a definition is printed as a declaration")
	}
	mustPanicLLVM(t, "Declare with a second type", func() {
		m := NewModule("x", "", "")
		m.Declare("f", FuncSig{Linkage: External, Ret: m.Void()})
		m.Declare("f", FuncSig{Linkage: External, Ret: m.I(32)})
	})
	mustPanicLLVM(t, "NewBlock on a view", func() { s2.NewBlock("entry") })
}

// TestDeclareAsCompiles: the module is valid IR for host clang on
// every smoke target at -O0 and -O2 (the WSL clang half is TestLLVMSyntaxSmoke's
// harness; this test stays off WSL).
func TestDeclareAsCompiles(t *testing.T) {
	host := findHostClang(t)
	for _, tg := range smokeTriples {
		t.Run(tg.name, func(t *testing.T) {
			r := resolve(t, host, tg)
			m, _ := declareAsModule(r)
			if out, ok := compile(host, r, m); !ok {
				t.Error(out)
			}
		})
	}
}
