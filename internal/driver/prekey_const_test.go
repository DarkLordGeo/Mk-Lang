// Why: a pack's pre-emission key must cover the layouts its consts and
// globals read. A var global's initializer is hashed by value and its types
// by name, so a layout change that reaches a pack only through a global's
// value, a const's data relocation or an asm sym operand could keep the key
// and link a stale object.
// What: TestGlobalConstLayout over the wrapper, declare, IsLinear and
// asm sym shapes (wrapperFiles, declFiles, linearFiles, asmSymFiles,
// asmSymExternFiles), and TestSupportPackKeyedByIR.
// How: builds each program with a narrow and a wide layout of shape.S into
// one cache that starts with std's packs and requires the warm build to
// miss and link the bytes of a --no-cache build; MK_CACHE_VERIFY builds
// check the keys. The subtests run in parallel.

package driver

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/types"
)

// constLayoutShape is shape.S, 8 bytes or, wide, 24.
func constLayoutShape(wide bool) string {
	if wide {
		return "export struct S {\n    export a: int\n    export b: int\n    export c: int\n}\n\n" +
			"export Make(a: int): S {\n    return S(a: a, b: 0, c: 0)\n}\n"
	}
	return "export struct S {\n    export a: int\n}\n\nexport Make(a: int): S {\n    return S(a: a)\n}\n"
}

// wrapperFiles: tbl.T's step is raw.Offset[shape.S], an intrinsic used as
// a fn value, so tbl's pack, which defines T and holds tbl.Other, defines
// the wrapper of Offset[S], whose body strides by S's size. No member of
// tbl's pack names S. Main reads arr[2] through T.step and exits 0.
func wrapperFiles(wide bool) map[string]string {
	return map[string]string{
		"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "app", root: "app", link_script: "", cpu: "", subsystem: "console")]`,
			"unsafe_modules": `["tbl", "app"]`}),
		"shape/s.mk": constLayoutShape(wide),
		"tbl/tbl.mk": "import std.raw\nimport shape\n\nexport struct Table {\n    export step: fn(*shape.S, int): *shape.S\n}\n\n" +
			"MakeTable(): Table {\n    var t := Table(step: Same)\n    unsafe \"the table's step is raw.Offset, which the callers use in bounds\" {\n" +
			"        t = Table(step: raw.Offset[shape.S])\n    }\n    return t\n}\n\n" +
			"Same(p: *shape.S, n: int): *shape.S {\n    return p\n}\n\n" +
			"export var T: Table = MakeTable()\n\nexport Other(): int {\n    return 1\n}\n",
		"app/app.mk": "import std.raw\nimport shape\nimport tbl\n\nexport Main(sys: &System): int {\n" +
			"    arr := [shape.Make(1), shape.Make(2), shape.Make(3)]\n    p := raw.Of(&arr[0])\n    t := &tbl.T\n    var got := 0\n" +
			"    unsafe \"q points into arr, which outlives the read\" {\n        q := t.step(p, 2)\n        v := raw.Read(q)\n        got = v.a\n    }\n" +
			"    return got + tbl.Other() - 4\n}\n",
	}
}

// declFiles: tbl.T's f is h.H, a function of another pack, so tbl's pack
// declares H for T's initializer; S's size moves H's parameter from a
// register to a pointer. No member of tbl's pack names S.
func declFiles(wide bool) map[string]string {
	return map[string]string{
		"pkg.mk":     manifest(map[string]string{"bins": `[pkg.Bin(name: "app", root: "app", link_script: "", cpu: "", subsystem: "console")]`}),
		"shape/s.mk": constLayoutShape(wide),
		"h/h.mk":     "import shape\n\nexport H(s: shape.S): int {\n    return s.a + 1\n}\n",
		"tbl/tbl.mk": "import h\nimport shape\n\nexport struct Table {\n    export f: fn(shape.S): int\n}\n\n" +
			"export var T: Table = Table(f: h.H)\n\nexport Other(): int {\n    return 1\n}\n",
		"app/app.mk": "import shape\nimport tbl\n\nexport Main(sys: &System): int {\n    t := &tbl.T\n    return t.f(shape.Make(3)) + tbl.Other() - 5\n}\n",
	}
}

// linearShape is shape.S, plain or linear (the keyword only, one field):
// raw.IsLinear[S] reads Facts(S).Kind, and nothing else changes.
func linearShape(linear bool) string {
	if linear {
		return "export linear struct S {\n    export a: int\n}\n"
	}
	return "export struct S {\n    export a: int\n}\n"
}

// linearFiles: tbl holds the wrapper of raw.IsLinear[shape.S] in an H{f:
// fn(): bool}, whose type does not name S, so only the wrapper tbl's pack
// defines reads S. tbl.Read calls it through, by variant:
//
//	var: a var global G that tbl's pack defines (the u.Globals walk and
//	     the function relocation);
//	val: a const C read by value (the member-body walk, RvConstRef or a
//	     const operand; C is in no u.Globals);
//	ref: &C, an RvGlobalRef of a const item, whose storage globalRef
//	     emits in tbl's pack;
//	nestval, nestref: const P: &H = &C, read by
//	     value or through &P. P's value reaches C's bytes, and so the
//	     wrapper, only through a data relocation.
//
// Main exits 0 while S is plain, 11 once it is linear.
func linearFiles(variant string) func(bool) map[string]string {
	var tbl string
	head := "import std.raw\nimport shape\n\nexport struct H {\n    export f: fn(): bool\n}\n\n"
	nest := head + "export const C: H = H(f: raw.IsLinear[shape.S])\n\nexport const P: &H = &C\n\n"
	switch variant {
	case "var":
		tbl = head + "export var G: H = H(f: raw.IsLinear[shape.S])\n\nexport Read(): bool {\n    g := &G\n    return g.f()\n}\n"
	case "val":
		tbl = head + "export const C: H = H(f: raw.IsLinear[shape.S])\n\nexport Read(): bool {\n    c := C\n    return c.f()\n}\n"
	case "ref":
		tbl = head + "export const C: H = H(f: raw.IsLinear[shape.S])\n\nexport Read(): bool {\n    c := &C\n    return c.f()\n}\n"
	case "nestval":
		tbl = nest + "export Read(): bool {\n    p := P\n    return p.f()\n}\n"
	case "nestref":
		tbl = nest + "export Read(): bool {\n    q := &P\n    p := *q\n    return p.f()\n}\n"
	default:
		panic(variant)
	}
	tbl += "\nexport Other(): int {\n    return 1\n}\n"
	return func(linear bool) map[string]string {
		return map[string]string{
			"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "app", root: "app", link_script: "", cpu: "", subsystem: "console")]`,
				"unsafe_modules": `["tbl"]`}),
			"shape/s.mk": linearShape(linear),
			"tbl/tbl.mk": tbl,
			"app/app.mk": "import tbl\n\nexport Main(sys: &System): int {\n    if tbl.Read() {\n        return 10 + tbl.Other()\n    }\n    return tbl.Other() - 1\n}\n",
		}
	}
}

// asmSymFiles: tbl.Addr names h.H only through an asm sym operand, so tbl's
// pack declares H from H's lowered signature, which S's size moves (i64 to a
// pointer); no member of tbl's pack names S. The executable's bytes do not
// change (a symbol reference does not read the declared signature), so only
// the key and the verify build can see the miss.
func asmSymFiles(wide bool) map[string]string {
	return map[string]string{
		"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "app", root: "app", link_script: "", cpu: "", subsystem: "console")]`,
			"unsafe_modules": `["tbl"]`}),
		"shape/s.mk": constLayoutShape(wide),
		"h/h.mk":     "import shape\n\nexport H(s: shape.S): int {\n    return s.a + 1\n}\n",
		"tbl/tbl.mk": "import h\n\nexport Addr(): uint {\n    var a: uint = 0\n" +
			"    unsafe \"the test only takes a symbol's address\" {\n" +
			"        asm(\"lea {r}, [rip + {s}]\", r: out(reg) a, s: sym h.H, options(pure, nomem, nostack, preserves_flags))\n" +
			"    }\n    return a\n}\n\nexport Other(): int {\n    return 1\n}\n",
		"app/app.mk": "import h\nimport shape\nimport tbl\n\nexport Main(sys: &System): int {\n    if tbl.Addr() == 0 {\n        return 9\n    }\n" +
			"    return h.H(shape.Make(3)) + tbl.Other() - 5\n}\n",
	}
}

// asmSymExternFiles: tbl.Addr names ext.KernelEnd, an extern var of type
// shape.S that module ext defines (an export extern var), only through an
// asm sym operand. ext owns a pack (Touch), so the extern var's u.Globals
// row is ext's pack, not tbl's, and only asmSymTypes reaches S for tbl's
// pack, whose declare of KernelEnd prints S's size and alignment. lea does
// not read the declared size, so tbl's object differs only through that
// declare: only its key and the verify build see tbl's miss.
func asmSymExternFiles(wide bool) map[string]string {
	return map[string]string{
		"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "app", root: "app", link_script: "", cpu: "", subsystem: "console")]`,
			"unsafe_modules": `["tbl", "ext"]`}),
		"shape/s.mk": constLayoutShape(wide),
		"ext/ext.mk": "import shape\n\nexport extern var KernelEnd: shape.S\n\nexport Touch(): int {\n    return 1\n}\n",
		"tbl/tbl.mk": "import ext\n\nexport Addr(): uint {\n    var a: uint = 0\n" +
			"    unsafe \"the test only takes a symbol's address\" {\n" +
			"        asm(\"lea {r}, [rip + {s}]\", r: out(reg) a, s: sym ext.KernelEnd, options(pure, nomem, nostack, preserves_flags))\n" +
			"    }\n    return a\n}\n\nexport Other(): int {\n    return 1\n}\n",
		"app/app.mk": "import ext\nimport shape\nimport tbl\n\nexport Main(sys: &System): int {\n    if tbl.Addr() == 0 {\n        return 9\n    }\n" +
			"    return shape.Make(3).a + ext.Touch() - 4\n}\n",
	}
}

// TestGlobalConstLayout: for each shape, dev and release, S changes
// after a cold build (it grows from 8 to 24 bytes, or becomes linear). The
// edit build must emit tbl's pack (the one whose IR reads S only through a
// const or a declare), link the bytes of a --no-cache build of the edited
// tree, and its MK_CACHE_VERIFY build must report no stale memo; a second
// build with no change emits only the support pack.
//
// The cold builds start from std's packs (stdSeedBuild), so the program's
// packs are the ones built cold. The subtests run in parallel, at most
// constLayoutJobs at once, each in its own tree and cache.
func TestGlobalConstLayout(t *testing.T) {
	tools := needTools(t)
	seed := stdSeedBuild(t, Config{Target: pkTarget(), Profile: types.Dev, Tools: tools},
		Config{Target: pkTarget(), Profile: types.Release, Tools: tools})
	slots := make(chan struct{}, constLayoutJobs)
	for _, c := range []struct {
		name  string
		files func(bool) map[string]string
	}{{"wrapper", wrapperFiles}, {"declare", declFiles},
		{"islinear-var", linearFiles("var")}, {"islinear-val", linearFiles("val")}, {"islinear-ref", linearFiles("ref")},
		{"islinear-nestval", linearFiles("nestval")}, {"islinear-nestref", linearFiles("nestref")},
		{"asm-sym", asmSymFiles}, {"asm-sym-extern", asmSymExternFiles}} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(c.name+"/"+prof.String(), func(t *testing.T) {
				t.Parallel()
				slots <- struct{}{}
				defer func() { <-slots }()
				dir := writeTree(t, c.files(false))
				out := t.TempDir()
				cfg := Config{Root: dir, Target: pkTarget(), Profile: prof, Tools: tools, CacheDir: seededCache(t, seed)}
				cold := pkBuild(t, cfg, filepath.Join(out, "narrow.exe"))
				pkVerify(t, cfg, filepath.Join(out, "narrow-verify.exe"), cold.exe)
				pkWrite(t, dir, c.files(true))
				edit := pkBuild(t, cfg, filepath.Join(out, "wide.exe"))
				tblEmitted, tblKeyed := false, false
				for _, e := range edit.events {
					if strings.HasPrefix(e.First, "tbl.") {
						tblEmitted, tblKeyed = tblEmitted || e.Emitted, tblKeyed || e.HasPre
					}
				}
				if !tblKeyed || !tblEmitted {
					t.Errorf("after S grew: tbl's pack keyed %v, emitted %v; want a keyed miss (emitted %v)", tblKeyed, tblEmitted, edit.emittedMods())
				}
				nc := cfg
				nc.NoCache = true
				fresh := pkBuild(t, nc, filepath.Join(out, "wide-nocache.exe"))
				if !bytes.Equal(edit.exe, fresh.exe) {
					t.Error("the edit build's executable differs from a --no-cache build of the edited tree: a stale object was linked")
				}
				pkVerify(t, cfg, filepath.Join(out, "wide-verify.exe"), edit.exe)
				warm := pkBuild(t, cfg, filepath.Join(out, "wide-warm.exe"))
				if got := warm.emittedMods(); !slices.Equal(got, []string{"support"}) || !bytes.Equal(warm.exe, edit.exe) {
					t.Errorf("a build with no change after the edit emitted %v (want the support pack only), executable equal %v", got, bytes.Equal(warm.exe, edit.exe))
				}
			})
		}
	}
}

// constLayoutJobs bounds TestGlobalConstLayout's subtests that run at
// once: each builds std three times (two verify builds and a --no-cache
// build), and eighteen at once would hold eighteen such builds in memory.
const constLayoutJobs = 4

// supportFiles is a program whose Main fails with the error name: the
// support pack's ErrorName rows print the error's key and display form.
func supportFiles(name string) map[string]string {
	return map[string]string{
		"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "app", root: "app", link_script: "", cpu: "", subsystem: "console")]`}),
		"app/app.mk": "error " + name + "(code: i64)\n\nexport Main(sys: &System): " + name + "!int {\n" +
			"    if sys.args.Len() > 1 {\n        fail " + name + "(code: 1)\n    }\n    return 5\n}\n",
	}
}

// TestSupportPackKeyedByIR: the support pack forms no pre-emission key.
// It reads the whole unit (the live errors' codes and display forms, Main's
// signature and layouts, the std.rt signatures, every support function for
// FuncTable), and a digest of those reads would be one more approximation of
// codegen's reads, and such digests have missed reads before. So it is
// generated on every build and keyed by its IR: a warm build emits it with
// no pre-emission key and finds its object by the IR's key, so clang does
// not run on it; an edit that changes its IR (an error renamed) gives
// another object key, compiles it again, and links the bytes of a --no-cache
// build. Dev and release, in parallel.
func TestSupportPackKeyedByIR(t *testing.T) {
	tools := needTools(t)
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			t.Parallel()
			dir := writeTree(t, supportFiles("Bad"))
			out := t.TempDir()
			cfg := Config{Root: dir, Target: pkTarget(), Profile: prof, Tools: tools, CacheDir: filepath.Join(t.TempDir(), "cache")}
			support := func(label string, r pkRun) PackEvent {
				t.Helper()
				for _, e := range r.events {
					if e.Name == "support" {
						if e.HasPre || !e.Emitted {
							t.Errorf("%s: the support pack keyed %v, emitted %v; want no pre-emission key, emitted", label, e.HasPre, e.Emitted)
						}
						return e
					}
				}
				t.Fatalf("%s: no support pack event", label)
				return PackEvent{}
			}
			cold := pkBuild(t, cfg, filepath.Join(out, "cold.exe"))
			c := support("cold", cold)
			if !slices.Contains(cold.r.Trace.PacksCompiled, "support") {
				t.Errorf("cold build: support not compiled (%v)", cold.r.Trace.PacksCompiled)
			}
			warm := pkBuild(t, cfg, filepath.Join(out, "warm.exe"))
			if w := support("warm", warm); w.Obj != c.Obj || slices.Contains(warm.r.Trace.PacksCompiled, "support") {
				t.Errorf("warm build: support's object key equal %v, compiled %v; want its IR's key and a hit", w.Obj == c.Obj, warm.r.Trace.PacksCompiled)
			}
			pkWrite(t, dir, supportFiles("Worse"))
			edit := pkBuild(t, cfg, filepath.Join(out, "edit.exe"))
			if e := support("edit", edit); e.Obj == c.Obj || !slices.Contains(edit.r.Trace.PacksCompiled, "support") {
				t.Errorf("after renaming the error: support's object key moved %v, compiled %v", e.Obj != c.Obj, edit.r.Trace.PacksCompiled)
			}
			nc := cfg
			nc.NoCache = true
			if fresh := pkBuild(t, nc, filepath.Join(out, "edit-nocache.exe")); !bytes.Equal(edit.exe, fresh.exe) {
				t.Error("the edit build's executable differs from a --no-cache build of the edited tree")
			}
			pkVerify(t, cfg, filepath.Join(out, "edit-verify.exe"), edit.exe)
		})
	}
}
