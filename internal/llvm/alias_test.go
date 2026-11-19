// Why: codegen's noalias facts reach LLVM as scoped alias metadata, which LLVM
// accepts only in an exact shape.
// What: TestAliasScopeMetadata over aliasModule.
// How: checks that the scoped alias nodes are distinct and name themselves,
// the declaration intrinsic is declared once after the functions, and clang 21
// accepts the module with the verifier on for every smoke target, at -O0 and
// -O2.

package llvm

import (
	"strings"
	"testing"
)

func aliasModule(tg target) *Module {
	m := NewModule("alias.mk", tg.triple, tg.layout)
	md := m.Metadata()
	for _, name := range []string{"f", "g"} {
		f := m.AddFunction(name, FuncSig{Ret: m.I(64), Params: []Type{m.Ptr(), m.Ptr()}, FnAttrs: Attrs{}.Flag("nounwind")})
		dom := md.AliasDomain(name)
		sa, sb := md.AliasScope(dom, name+": param 0"), md.AliasScope(dom, name+": param 1")
		la, lb := md.Tuple(MDRef(sa)), md.Tuple(MDRef(sb))
		b := f.NewBlock("b0")
		b.NoAliasScopeDecl(la)
		b.NoAliasScopeDecl(lb)
		b.Store(m.ConstInt(m.I(64), 1), f.Param(0), 8, AliasScope(la), NoAlias(lb))
		v := b.Load(m.I(64), f.Param(1), 8, AliasScope(lb), NoAlias(la))
		b.Ret(&v)
	}
	return m
}

func TestAliasScopeMetadata(t *testing.T) {
	host := findHostClang(t)
	text := aliasModule(smokeTriples[0]).String()
	for _, want := range []string{
		`= distinct !{!0, !"f"}`,
		`= distinct !{!1, !0, !"f: param 0"}`,
		`call void @llvm.experimental.noalias.scope.decl(metadata !`,
		`store i64 1, ptr %p0, align 8, !alias.scope !`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("module lacks %q:\n%s", want, text)
		}
	}
	if n := strings.Count(text, "declare void @llvm.experimental.noalias.scope.decl(metadata)"); n != 1 {
		t.Errorf("the intrinsic is declared %d times, want 1", n)
	}
	if strings.Count(text, `!"f"}`) != 1 || strings.Count(text, `!"g"}`) != 1 {
		t.Errorf("want one domain per function:\n%s", text)
	}
	for _, tg := range smokeTriples {
		r := resolve(t, host, tg)
		if out, ok := compile(host, r, aliasModule(r)); !ok {
			t.Error(out)
		}
	}
}
