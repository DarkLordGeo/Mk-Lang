// Why: a guaranteed tail call is only guaranteed if LLVM's musttail rules hold.
// What: TestMustTailCall over mustTailModule.
// How: the call prints as `musttail call`, a direct and an indirect one each
// followed by ret of its value, one with a forwarded sret pointer and ret void;
// clang accepts the module with the verifier on for every smoke target at -O0
// and -O2. A variadic callee panics: musttail needs the caller's prototype.

package llvm

import (
	"strings"
	"testing"
)

func mustTailModule(tg target) *Module {
	m := NewModule("musttail.mk", tg.triple, tg.layout)
	i64 := m.I(64)
	sig := FuncSig{Ret: i64, Params: []Type{i64, i64}, FnAttrs: Attrs{}.Flag("nounwind")}
	f := m.AddFunction("f", sig)
	g := m.AddFunction("g", sig)
	{
		b := f.NewBlock("b0")
		v := b.MustTailCall(g.Type(), g.Ref(), []Value{f.Param(1), f.Param(0)}, nil, Attrs{}, Attrs{}.Flag("nounwind"))
		b.Ret(&v)
	}
	{
		b := g.NewBlock("b0")
		p := b.Alloca(m.Ptr(), 8)
		b.Store(f.Ref(), p, 8)
		fp := b.Load(m.Ptr(), p, 8)
		v := b.MustTailCall(f.Type(), fp, []Value{g.Param(0), g.Param(1)}, nil, Attrs{}, Attrs{}.Flag("nounwind"))
		b.Ret(&v)
	}
	big := m.Array(24, m.I(8))
	ssig := FuncSig{Ret: m.Void(), Params: []Type{m.Ptr(), i64},
		ParamAttrs: []Attrs{Attrs{}.Typed("sret", big).Flag("noalias"), {}}, FnAttrs: Attrs{}.Flag("nounwind")}
	h := m.AddFunction("h", ssig)
	k := m.AddFunction("k", ssig)
	for _, fn := range []*Function{h, k} {
		other := k
		if fn == k {
			other = h
		}
		b := fn.NewBlock("b0")
		b.MustTailCall(other.Type(), other.Ref(), []Value{fn.Param(0), fn.Param(1)}, ssig.ParamAttrs, Attrs{}, Attrs{}.Flag("nounwind"))
		b.Ret(nil)
	}
	return m
}

func TestMustTailCall(t *testing.T) {
	host := findHostClang(t)
	text := mustTailModule(smokeTriples[0]).String()
	for _, want := range []string{"= musttail call i64 @g(", "= musttail call i64 %", "musttail call void @k(ptr sret([24 x i8]) noalias "} {
		if !strings.Contains(text, want) {
			t.Errorf("module lacks %q:\n%s", want, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "musttail") && strings.Contains(line, "call") {
			continue
		}
	}
	for _, tg := range smokeTriples {
		r := resolve(t, host, tg)
		if out, ok := compile(host, r, mustTailModule(r)); !ok {
			t.Error(out)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("a variadic musttail did not panic")
		}
	}()
	m := NewModule("v.mk", smokeTriples[0].triple, smokeTriples[0].layout)
	vf := m.AddFunction("v", FuncSig{Ret: m.Void(), Params: []Type{m.Ptr()}, Variadic: true})
	b := m.AddFunction("w", FuncSig{Ret: m.Void()}).NewBlock("b0")
	b.MustTailCall(vf.Type(), vf.Ref(), []Value{m.Null()}, nil, Attrs{}, Attrs{})
}
