// Why: a freestanding build (a kernel) has no OS, so the support pack must
// drop the hosted routines and let the program supply its hooks.
// What: TestFreestandingSupport: on x86_64-freestanding the support pack
// has no hosted routine and no entry stub, defines weak hook defaults and
// puts noredzone on every definition; TestFreestandingPackAttrs: the
// panic hooks are std.free's.
// How: the IR is checked and compiled with clang; hosted output is held
// byte for byte by the goldens of TestSupportGolden and TestEmitGolden.

package codegen

import (
	"debug/elf"
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// defineLines are the define lines of module ll.
func defineLines(ll string) []string {
	return regexp.MustCompile(`(?m)^define .*$`).FindAllString(ll, -1)
}

// TestFreestandingSupport: the freestanding support pack emits none of
// the hosted routines (_start, mk_start, mk_switch, mk_task_start, clone,
// sigreturn, mk_call_system, InitContext, the std.task.Start call), defines
// MkPanicHook (llvm.trap), MkPageAlloc (null) and MkPageFree weak with
// default visibility, puts noredzone on every definition, keeps the memory
// functions and the 128-bit division routines, and compiles with clang to
// an object whose three hooks are weak symbols. No hosted support pack has
// any of the three or noredzone.
func TestFreestandingSupport(t *testing.T) {
	tg := types.X86_64Freestanding
	ll := supportLL(t, tg)
	if again := supportLL(t, tg); again != ll {
		t.Fatal("two emissions of the freestanding support pack differ")
	}
	for _, bad := range []string{"module asm", "_start", "mk_start", "mk_switch", "mk_task_start", "mk_clone",
		"mk_sigreturn", "mk_call_system", "mk.support.InitContext", "mk.std.task.Start", "__chkstk", "_fltused"} {
		if strings.Contains(ll, bad) {
			t.Errorf("freestanding support pack names %s:\n%s", bad, ll)
		}
	}
	for _, want := range []string{
		"define weak dso_local void @MkPanicHook(ptr noundef %p0)",
		"define weak dso_local ptr @MkPageAlloc(i64 noundef %p0, i64 noundef %p1)",
		"define weak dso_local void @MkPageFree(ptr noundef %p0, i64 noundef %p1, i64 noundef %p2)",
		"call void @llvm.trap()", "ret ptr null",
		"@memcpy(", "@memmove(", "@memset(", "@memcmp(", "@bcmp(",
		"define internal i128 @mk.support.udivti3(i128 noundef %p0, i128 noundef %p1)", "@__udivti3 = weak alias i128 (i128, i128), ptr @mk.support.udivti3",
		"@__umodti3 = weak alias", "@__divti3 = weak alias", "@__modti3 = weak alias",
	} {
		if !strings.Contains(ll, want) {
			t.Errorf("freestanding support pack lacks %q:\n%s", want, ll)
		}
	}
	if hook := fnText(ll, "MkPanicHook"); !strings.Contains(hook, "noreturn") || !strings.Contains(hook, "unreachable") {
		t.Errorf("MkPanicHook is not a noreturn trap:\n%s", hook)
	}
	defs := defineLines(ll)
	if len(defs) != 13 {
		t.Errorf("%d definitions, want the five memory functions, the four 128-bit division routines, the three hooks and the yield hook:\n%s",
			len(defs), strings.Join(defs, "\n"))
	}
	for _, d := range defs {
		if !strings.Contains(d, "noredzone") || strings.Contains(d, "hidden") {
			t.Errorf("want noredzone and default visibility: %s", d)
		}
	}
	obj := clangC(t, tg, t.TempDir(), "support", ll, "-O2")
	f, err := elf.Open(obj)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	weak := map[string]bool{}
	for _, s := range syms {
		if elf.ST_BIND(s.Info) == elf.STB_WEAK && s.Section != elf.SHN_UNDEF {
			weak[s.Name] = true
		}
	}
	for _, n := range []string{symPanicHook, symPageAlloc, symPageFree, symThreadYield} {
		if !weak[n] {
			t.Errorf("%s is not a defined weak symbol of the object (weak: %v)", n, weak)
		}
	}
	for _, h := range types.Targets() {
		hl := supportLL(t, h)
		for _, bad := range []string{"noredzone", symPanicHook, symPageAlloc, symPageFree} {
			if strings.Contains(hl, bad) {
				t.Errorf("%s support pack names %q", h.Name, bad)
			}
		}
		// Its only weak names are the 128-bit routines: aliases,
		// or on Mach-O weak definitions; and the yield hook, which hosted
		// std.rt replaces.
		for _, d := range regexp.MustCompile(`(?m)^define weak .*$`).FindAllString(hl, -1) {
			if !regexp.MustCompile(`@__(u?div|u?mod)ti3\(|@MkThreadYield\(\)`).MatchString(d) {
				t.Errorf("%s support pack has a weak definition: %s", h.Name, d)
			}
		}
	}
}

// freeHookUnit is std.free.PanicAt(code: u32, site: &Site): never over a
// std.free.Site of std.rt.Site's layout, main.AddI32, whose overflow check
// calls the hook, and a naked main.Stub; all in pack 0.
func freeHookUnit(e *testEnv) *Unit {
	site := e.structIn("std.free", "Site", types.Str, types.U32, types.U32)
	pi := e.itemIn("std.free", types.ItemKey("std.free", "PanicAt"), "PanicAt", types.KindFunc,
		&types.FuncDecl{Sig: types.Signature{Name: e.in.Intern("PanicAt"), Result: types.Never,
			Params: []types.Param{{Name: e.in.Intern("code"), Type: types.U32}, {Name: e.in.Intern("site"), Type: e.tt.Ref(site)}}}})
	e.items.Get(pi).Decl.(*types.FuncDecl).Sig.Item = pi
	ph := e.body(pi)
	ph.term(mir.Terminator{Kind: mir.TermUnreachable, Span: ph.at(9, 1)})
	add := binFn(e, "AddI32", mir.Add, types.I32, types.I32, mir.Copy(place(2)))
	stub := e.fn("Stub", nil, types.Void, 0)
	e.items.Get(stub).Decl.(*types.FuncDecl).Naked = true
	s := e.body(stub)
	s.asmBody(mir.Asm{Template: "hlt", Options: mir.AsmNoreturn}, s.at(3, 1))
	s.term(mir.Terminator{Kind: mir.TermUnreachable, Span: s.at(3, 1)})
	return unitOf(e, []*mir.Body{ph.b, add, s.b})
}

// TestFreestandingPackAttrs: on freestanding every definition of a
// pack, the naked one included, has noredzone and keeps the inline stack
// probe but the naked one; a check calls mk.std.free.PanicAt, whose site
// parameter has std.free.Site's facts (24 bytes); nothing names std.rt. A
// unit without the hook's body declares mk.std.free.PanicAt. The
// x86_64-linux pack of the same unit has no noredzone. Both compile with
// clang -O2.
func TestFreestandingPackAttrs(t *testing.T) {
	tg := types.X86_64Freestanding
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			e := newTestEnv()
			ll := emitP(t, e, freeHookUnit(e), 0, tg, prof)
			defs := defineLines(ll)
			if len(defs) != 3 {
				t.Fatalf("%d definitions, want 3:\n%s", len(defs), ll)
			}
			for _, d := range defs {
				attrs := attrGroupOf(ll, d)
				if !strings.Contains(attrs, "noredzone") {
					t.Errorf("no noredzone: %s (%s)", d, attrs)
				}
				naked := strings.Contains(attrs, "naked")
				if probe := strings.Contains(attrs, `"probe-stack"="inline-asm"`); probe == naked {
					t.Errorf("probe-stack %t on a function naked %t: %s (%s)", probe, naked, d, attrs)
				}
			}
			if !strings.Contains(fnText(ll, "mk.main.AddI32"), "@mk.std.free.PanicAt(") {
				t.Errorf("the overflow check does not call std.free.PanicAt:\n%s", fnText(ll, "mk.main.AddI32"))
			}
			if !regexp.MustCompile(`@mk\.std\.free\.PanicAt\(i32 noundef %p0, ptr noundef nonnull (readonly )?(noalias )?(readonly )?align 8 dereferenceable\(24\)`).MatchString(ll) {
				t.Errorf("PanicAt's site parameter lacks std.free.Site's facts:\n%s", ll)
			}
			if strings.Contains(ll, "std.rt") || strings.Contains(ll, "std.task") {
				t.Errorf("freestanding pack names std.rt or std.task:\n%s", ll)
			}
			if strings.Contains(ll, rowSectionMachO) { // the target is ELF
				t.Errorf("freestanding pack puts FuncTable rows in the Mach-O section %s:\n%s", rowSectionMachO, ll)
			}
			clangC(t, tg, t.TempDir(), "free", ll, "-O2")

			e = newTestEnv()
			u := unitOf(e, []*mir.Body{binFn(e, "AddI32", mir.Add, types.I32, types.I32, mir.Copy(place(2)))})
			ll = emitP(t, e, u, 0, tg, prof)
			if count(ll, `^declare .*@mk\.std\.free\.PanicAt\(i32 noundef, ptr noundef`) != 1 || strings.Contains(ll, "std.rt") {
				t.Errorf("want one declaration of mk.std.free.PanicAt:\n%s", ll)
			}
			clangC(t, tg, t.TempDir(), "decl", ll, "-O2")

			e = newTestEnv()
			ll = emitP(t, e, freeHookUnit(e), 0, types.X86_64Linux, prof)
			if strings.Contains(ll, "noredzone") {
				t.Errorf("hosted pack has noredzone:\n%s", ll)
			}
		})
	}
}
