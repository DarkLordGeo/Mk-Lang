// Why: on x86_64 Windows an image's sections are aligned to a page unless it
// links with a larger /align, so static data aligned over a page needs both.
// What: tests that such a const or var is emitted in the section of its
// kind and alignment, padded to a multiple of its alignment, and that
// StaticAlign gives the image's /align from what the unit's IR can emit.
// How: IR checks per target; the run tests are std.core's static table tests
// in std/core/tests_arena.mk and tests/programs/staticalign.

package codegen

import (
	"regexp"
	"strings"
	"testing"

	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// staticUnit is Data(): []Huge returning a const slice of two align(65536)
// Huge, and Use(): u64 reading the align(8192) var Var's field, plus the
// align(8192) zero var Zero; Huge and Big are structs of one u64.
func staticUnit(e *testEnv) *Unit {
	field := []types.FieldDecl{{Name: e.in.Intern("x"), Type: types.U64}}
	hi := e.item("Huge", types.KindStruct, &types.StructDecl{Align: 65536, Fields: field})
	huge := e.tt.Named(hi, nil)
	bi := e.item("Big", types.KindStruct, &types.StructDecl{Align: 8192, Fields: field})
	big := e.tt.Named(bi, nil)
	arr := e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, 2)}, huge)
	data := e.consts.Intern(types.ConstValue{Type: arr, Align: 65536, Bytes: make([]byte, 2*65536)})
	hdr := make([]byte, 16)
	types.StoreUint(hdr[8:], 8, 2)
	sl := e.tt.Slice(huge)
	c := e.consts.Intern(types.ConstValue{Type: sl, Align: 8, Bytes: hdr,
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocData, Data: data}}})
	d := e.body(e.fn("Data", nil, sl, 0))
	d.assign(place(0), use(mir.Const(sl, c)), d.at(2, 1))
	d.ret(d.at(2, 1))

	v := e.item("Var", types.KindGlobal, &types.GlobalDecl{Type: big, HasValue: true,
		Value: e.consts.Intern(types.ConstValue{Type: big, Align: 8192, Bytes: make([]byte, 8192)})})
	z := e.item("Zero", types.KindGlobal, &types.GlobalDecl{Type: big})
	f := e.body(e.fn("Use", nil, types.U64, 0))
	vp, zp := f.local(e.tt.Ref(big)), f.local(e.tt.Ref(big))
	f.assign(place(vp), mir.Rvalue{Kind: mir.RvGlobalRef, Item: v, Type: e.tt.Ref(big)}, f.at(3, 1))
	f.assign(place(zp), mir.Rvalue{Kind: mir.RvGlobalRef, Item: z, Type: e.tt.Ref(big)}, f.at(4, 1))
	vx := f.b.Place(vp, mir.Projection{Kind: mir.ProjDeref, Type: big}, mir.Projection{Kind: mir.ProjField, A: 0, Type: types.U64})
	zx := f.b.Place(zp, mir.Projection{Kind: mir.ProjDeref, Type: big}, mir.Projection{Kind: mir.ProjField, A: 0, Type: types.U64})
	f.assign(place(0), bin(mir.Add, mir.Copy(vx), mir.Copy(zx)), f.at(5, 1))
	f.ret(f.at(5, 1))
	u := unitOf(e, []*mir.Body{d.b, f.b})
	u.Globals = []Global{{Item: v, Pack: 0, Local: true}, {Item: z, Pack: 0, Local: true}}
	return u
}

// TestStaticDataOverPage: on x86_64 Windows, in both profiles, the
// align(65536) const blob is in .mkalr$a and the align(8192) vars in
// .mkalw$d, each aligned 4096, and StaticAlign is 65536; x86_64 Linux keeps
// the alignments in the IR, names no such section, and StaticAlign is 0.
// The module compiles for its target.
func TestStaticDataOverPage(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(target.Name+"/"+map[types.Profile]string{types.Dev: "dev", types.Release: "release"}[prof], func(t *testing.T) {
				e := newTestEnv()
				u := staticUnit(e)
				ll := emit(t, e, u, target, prof)
				win := target.OS == types.OSWindows
				// The blob's bytes print as a long c"..." string: only each
				// line's end is shown.
				tail := func(s string) string { return s[max(0, len(s)-120):] }
				blob := regexp.MustCompile(`(?m)^@mk\.c\.\d+ = private unnamed_addr constant <\{ \[131072 x i8\] \}> .*$`).FindString(ll)
				if blob == "" {
					t.Fatalf("no 131072-byte const blob:\n%s", tail(ll))
				}
				vr := regexp.MustCompile(`(?m)^@mk\.main\.Var = .*$`).FindString(ll)
				zr := regexp.MustCompile(`(?m)^@mk\.main\.Zero = .*$`).FindString(ll)
				want := map[bool][3]string{
					true:  {`, section ".mkalr$a", align 4096`, `, section ".mkalw$d", align 4096`, `, section ".mkalw$d", align 4096`},
					false: {`, align 65536`, `, align 8192`, `, align 8192`},
				}[win]
				for i, got := range []string{blob, vr, zr} {
					if !strings.HasSuffix(got, want[i]) {
						t.Errorf("want %q at the end of: ...%s", want[i], tail(got))
					}
				}
				if !win && strings.Contains(ll, ".mkal") {
					t.Error("an over-page data section off Windows")
				}
				// staticPin: every such global is in @llvm.compiler.used.
				used := regexp.MustCompile(`(?m)^@llvm\.compiler\.used = .*$`).FindString(ll)
				for _, sym := range []string{"@mk.main.Var", "@mk.main.Zero", "@mk.c."} {
					if strings.Contains(used, "ptr "+sym) != win {
						t.Errorf("%s in @llvm.compiler.used: %v, want %v: %s", sym, !win, win, used)
					}
				}
				wa := map[bool]uint32{true: 65536, false: 0}[win]
				if got := StaticAlign(u, e, Config{Target: target, Profile: prof}); got != wa {
					t.Errorf("StaticAlign %d, want %d", got, wa)
				}
				clangC(t, target, t.TempDir(), "static", ll, "-O2")
			})
		}
	}
}

// TestStaticAlignCountsWhatTheUnitEmits: StaticAlign reads the consts
// the unit's IR can emit, not the const table: an align(65536) value only
// in the table leaves a unit with nothing over a page at 0; one reached
// through a data relocation from a body's operand counts (staticUnit:
// 65536), and so does a var global's type alone (8192).
func TestStaticAlignCountsWhatTheUnitEmits(t *testing.T) {
	cfg := Config{Target: types.X86_64Windows, Profile: types.Dev}
	e := newTestEnv()
	e.consts.Intern(types.ConstValue{Type: types.U64, Align: 65536, Bytes: make([]byte, 65536)})
	f := e.body(e.fn("Plain", nil, types.U64, 0))
	f.assign(place(0), use(mir.Const(types.U64, e.intConst(types.U64, 7))), f.at(2, 1))
	f.ret(f.at(2, 1))
	u := unitOf(e, []*mir.Body{f.b})
	if got := StaticAlign(u, e, cfg); got != 0 {
		t.Errorf("a table-only align(65536) value: StaticAlign %d, want 0", got)
	}
	emit(t, e, u, types.X86_64Windows, types.Dev)

	e = newTestEnv()
	u = staticUnit(e)
	if got := StaticAlign(u, e, cfg); got != 65536 {
		t.Errorf("staticUnit: StaticAlign %d, want 65536", got)
	}
	// Without Data, only the align(8192) vars remain.
	vars := &Unit{Funcs: u.Funcs[1:], Packs: [][]int{{0}}, Globals: u.Globals}
	if got := StaticAlign(vars, e, cfg); got != 8192 {
		t.Errorf("staticUnit without Data: StaticAlign %d, want 8192", got)
	}
	// The guard: a datum over the unit's value is an ICE, not a datum off
	// its alignment.
	g := &packGen{cfg: &cfg, staticDone: true, staticMax: 8192}
	if !g.bigStatic(8192) {
		t.Error("bigStatic(8192) false on x86_64-windows")
	}
	func() {
		defer func() {
			if _, ok := recover().(*source.ICE); !ok {
				t.Error("bigStatic(16384) over a unit value of 8192 raised no ICE")
			}
		}()
		g.bigStatic(16384)
	}()
}

// TestSectionsAndPadding: the section names sort by falling
// alignment, and padStatic rounds a datum up to a multiple of its
// alignment (a size already a multiple is left as it is).
func TestSectionsAndPadding(t *testing.T) {
	var prev string
	for a := uint32(65536); a > pageBytes; a /= 2 {
		s := staticSection(a, true)
		if prev != "" && s <= prev {
			t.Errorf("%s does not sort after %s", s, prev)
		}
		if w := staticSection(a, false); strings.TrimPrefix(w, ".mkalw") != strings.TrimPrefix(s, ".mkalr") {
			t.Errorf("%s and %s differ in their alignment letter", s, w)
		}
		prev = s
	}
	if staticSection(8192, true) != ".mkalr$d" || staticSection(65536, false) != ".mkalw$a" {
		t.Errorf("sections %s, %s", staticSection(8192, true), staticSection(65536, false))
	}
	g := &packGen{m: llvm.NewModule("pad", types.X86_64Windows.LLVMTriple, "")}
	init := g.m.Bytes(make([]byte, 24))
	if p := g.padStatic(init, 24, 8192); p.Ty.String() != "<{ [24 x i8], [8168 x i8] }>" {
		t.Errorf("padded type %s", p.Ty.String())
	}
	if p := g.padStatic(init, 8192, 8192); p.Ty.String() != init.Ty.String() {
		t.Errorf("a multiple was padded: %s", p.Ty.String())
	}
}
