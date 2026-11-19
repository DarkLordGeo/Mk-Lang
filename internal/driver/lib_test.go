// Why: a library bin's exports, header and archives are a C ABI that hosts
// depend on; each must be exactly the package's own and reproducible.
// What: TestLibExports_*, TestLibHeader_SelfAndMutualStructs,
// TestLibStatic_*, TestLibDebug_Reproducible, TestLibManifest_KindAndABI and
// TestLibRun_Refused, with the export-list helper noEntries.
// How: builds library packages and reads the .def or version script, the
// export tables, the header and the archives: exports are the package's
// own, the header compiles for self- and mutually referencing structs, a
// static archive is the same warm and cold, an export with no C form is
// refused, and a --debug DLL is reproducible.

package driver

import (
	"bytes"
	"debug/elf"
	"debug/pe"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// libBin is a pkg.Bin literal of a library (or an executable for kind "").
func libBin(name, root, kind, abi string) string {
	return `pkg.Bin(name: "` + name + `", root: "` + root + `", link_script: "", cpu: "", subsystem: "", kind: "` + kind + `", abi: "` + abi + `")`
}

// libBuilt is one library build's outputs and what its link saw.
type libBuilt struct {
	r     Result
	paths []string
	def   string // the .def's text (COFF dynamic)
	ver   string // the version script's text (ELF dynamic)
	err   error
}

// libBuildIn builds the package at root into out with the cache at cache
// ("" for --no-cache), reading the .def and version script at the link.
func libBuildIn(t *testing.T, root string, target types.Target, prof types.Profile, out, cache string, debug bool) libBuilt {
	t.Helper()
	var b libBuilt
	hooks := TestHooks{OnLink: func(j toolchain.LinkJob) {
		if j.Def != "" {
			d, _ := os.ReadFile(j.Def)
			b.def = string(d)
		}
		if j.VersionScript != "" {
			v, _ := os.ReadFile(j.VersionScript)
			b.ver = string(v)
		}
	}}
	cfg := Config{Root: root, Target: target, Profile: prof, Tools: needTools(t), CacheDir: cache, NoCache: cache == "", Debug: debug, TestHooks: hooks}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b.r, b.paths, b.err = s.Build(out + string(filepath.Separator))
	return b
}

// mustLib is libBuildIn that fails on any diagnostic.
func mustLib(t *testing.T, root string, target types.Target, prof types.Profile, out, cache string, debug bool) libBuilt {
	t.Helper()
	b := libBuildIn(t, root, target, prof, out, cache, debug)
	if b.err != nil {
		t.Fatal(b.err)
	}
	if len(b.r.Report.Diags) > 0 || len(b.paths) == 0 {
		t.Fatalf("build: %d paths\n%s", len(b.paths), b.r.Report.Text(b.r.Sources))
	}
	return b
}

// dllExports is the names of a PE image's export directory.
func dllExports(t *testing.T, path string) []string {
	t.Helper()
	f, err := pe.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	oh, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || oh.NumberOfRvaAndSizes < 1 || oh.DataDirectory[0].Size == 0 {
		return nil
	}
	rva := oh.DataDirectory[0].VirtualAddress
	read := func(at uint32, n int) []byte {
		for _, s := range f.Sections {
			if at >= s.VirtualAddress && at < s.VirtualAddress+s.VirtualSize {
				d, err := s.Data()
				if err != nil {
					t.Fatal(err)
				}
				o := int(at - s.VirtualAddress)
				if n < 0 {
					e := bytes.IndexByte(d[o:], 0)
					return d[o : o+e]
				}
				return d[o : o+n]
			}
		}
		t.Fatalf("%s: rva %#x in no section", path, at)
		return nil
	}
	dir := read(rva, 40)
	n := binary.LittleEndian.Uint32(dir[24:])
	names := binary.LittleEndian.Uint32(dir[32:])
	var out []string
	for i := uint32(0); i < n; i++ {
		out = append(out, string(read(binary.LittleEndian.Uint32(read(names+4*i, 4)), -1)))
	}
	slices.Sort(out)
	return out
}

// noEntries is names without library lib's two runtime entries,
// <lib>_mk_start and <lib>_mk_stop, which every library exports and
// declares beside its functions: what names lists of the library's own
// functions. It fails the test unless both are in names, once each (the
// entries are checked, not only stripped).
func noEntries(t *testing.T, lib, what string, names []string) []string {
	t.Helper()
	start, stop := lib+"_mk_start", lib+"_mk_stop"
	var out []string
	seen := map[string]int{}
	for _, n := range names {
		if n == start || n == stop {
			seen[n]++
			continue
		}
		out = append(out, n)
	}
	if seen[start] != 1 || seen[stop] != 1 {
		t.Errorf("%s lists %s %d times and %s %d times, want once each: %q", what, start, seen[start], stop, seen[stop], names)
	}
	return out
}

// soExports is the defined function symbols of an ELF shared object's
// dynamic symbol table.
func soExports(t *testing.T, path string) []string {
	t.Helper()
	f, err := elf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.DynamicSymbols()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range syms {
		if s.Section != elf.SHN_UNDEF && elf.ST_TYPE(s.Info) == elf.STT_FUNC {
			out = append(out, s.Name)
		}
	}
	slices.Sort(out)
	return out
}

// headerDecls is the function names a library header declares (lines
// "<type> Name(...);").
func headerDecls(h string) []string {
	var out []string
	for _, l := range strings.Split(h, "\n") {
		if strings.HasSuffix(l, ");") && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "/*") {
			name := l[:strings.IndexByte(l, '(')]
			out = append(out, name[strings.LastIndexAny(name, " *")+1:])
		}
	}
	slices.Sort(out)
	return out
}

// checkExports checks one dynamic library's .def or version script, its
// export table and its header against want.
func checkExports(t *testing.T, b libBuilt, target types.Target, name string, want []string) {
	t.Helper()
	lib := b.paths[0]
	for _, p := range b.paths {
		if strings.Contains(filepath.Base(p), name) {
			lib = p
		}
	}
	var got []string
	if target.OS == types.OSWindows {
		_, list, _ := strings.Cut(b.def, "EXPORTS")
		for _, l := range strings.Split(list, "\n") {
			if f := strings.Fields(l); len(f) > 0 {
				got = append(got, f[0])
			}
		}
		got = noEntries(t, name, "the .def", got)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf(".def lists %q, want %q:\n%s", got, want, b.def)
		}
		if got := noEntries(t, name, "the DLL's export table", dllExports(t, lib)); !slices.Equal(got, want) {
			t.Errorf("DLL exports %q, want %q", got, want)
		}
	} else {
		for _, w := range append([]string{name + "_mk_start", name + "_mk_stop"}, want...) {
			if !strings.Contains(b.ver, "    "+w+";") {
				t.Errorf("version script lacks %s:\n%s", w, b.ver)
			}
		}
		if n := strings.Count(b.ver, ";") - 4; n != len(want) {
			t.Errorf("version script lists %d names, want %d:\n%s", n, len(want), b.ver)
		}
		if got := noEntries(t, name, "the .so's dynamic symbols", soExports(t, lib)); !slices.Equal(got, want) {
			t.Errorf(".so exports %q, want %q", got, want)
		}
	}
	h := readFile(t, filepath.Join(filepath.Dir(lib), name+".h"))
	if got := noEntries(t, name, "the header", headerDecls(h)); !slices.Equal(got, want) {
		t.Errorf("header declares %q, want %q:\n%s", got, want, h)
	}
}

// TestLibExports_OwnPackageOnly: a library exports exactly its own package's
// export extern functions: never a path dependency's, never std.libc's
// (malloc, free, calloc...) that a C source brings in, and never an
// executable bin's of the same package. Checked in the .def or version
// script, the DLL's export table or the .so's dynamic symbols, and the
// header.
func TestLibExports_OwnPackageOnly(t *testing.T) {
	needTools(t)
	pkgs := []struct {
		name, lib string
		files     map[string]string
		root      string
		want      []string
	}{
		{"withdep", "ml", map[string]string{
			"ml/pkg.mk": manifest(map[string]string{"name": `"ml"`, "bins": "[" + libBin("ml", ".", "dynamic", "") + "]",
				"deps": `[pkg.Dep(name: "dep", path: "../dep", url: "", hash: "")]`}),
			"ml/lib.mk":  "import dep\n\nexport extern Own(n: i32): i32 {\n    return dep.DepExport(n)\n}\n",
			"dep/pkg.mk": manifest(map[string]string{"name": `"dep"`}),
			"dep/dep.mk": "/// DepExport is a dependency's export extern: section 2 keeps it local.\nexport extern DepExport(n: i32): i32 {\n    return n + 100\n}\n",
		}, "ml", []string{"Own"}},
		{"withc", "mc", map[string]string{
			"pkg.mk": manifest(map[string]string{"name": `"mc"`, "bins": "[" + libBin("mc", ".", "dynamic", "") + "]",
				"c_sources": `[pkg.CSource(path: "add.c", flags: [])]`, "unsafe_modules": `["."]`}),
			"add.c":  "int c_add(int a, int b) { return a + b; }\n",
			"lib.mk": "extern c_add(a: i32, b: i32): i32\n\nexport extern Add(a: i32, b: i32): i32 {\n    unsafe \"c_add is pure\" {\n        return c_add(a, b)\n    }\n}\n",
		}, "", []string{"Add"}},
		{"two", "alib", map[string]string{
			"pkg.mk": manifest(map[string]string{"name": `"two"`, "bins": "[" + libBin("alib", "lib", "dynamic", "") + ", " + libBin("zapp", ".", "", "") + "]"}),
			"main.mk": "import lib\n\nexport extern AppOnly(n: i32): i32 {\n    return n\n}\n\n" +
				"export Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n    v := lib.Twice(21)\n    try Print(mut out, \"{v}\\n\", (v,))\n}\n",
			"lib/lib.mk": "export extern Twice(n: i32): i32 {\n    return n * 2\n}\n",
		}, "", []string{"Twice"}},
	}
	for _, p := range pkgs {
		for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
			t.Run(p.name+"/"+target.Name, func(t *testing.T) {
				root := filepath.Join(writeTree(t, p.files), p.root)
				b := mustLib(t, root, target, types.Dev, t.TempDir(), t.TempDir(), false)
				checkExports(t, b, target, p.lib, p.want)
			})
		}
	}
}

// selfRefSource has a self-referencing struct (Node), two mutually
// referencing ones (A and B), and a struct holding another by value that
// points back at it (C and D), D reached first (AtD sorts before WalkC).
const selfRefSource = `/// A node of a list: refers to itself.
export struct Node {
    export next: *Node
    export v: i64
}

export struct A {
    export b: *B
    export n: i32
}

export struct B {
    export a: *A
    export m: i32
}

export struct C {
    export d: D
    export k: i32
}

export struct D {
    export c: *C
    export j: i32
}

export extern AtD(d: &D): i32 {
    return d.j
}

export extern Depth(n: *Node): i64 {
    return 0
}

export extern Walk(a: &A): i32 {
    return a.n
}

export extern WalkB(b: &B): i32 {
    return b.m
}

export extern WalkC(c: &C): i32 {
    return c.k + c.d.j
}
`

// TestLibHeader_SelfAndMutualStructs: the header declares every struct
// before any definition, so a struct that points at itself or at a later
// struct compiles under -Wall -Werror as C11 and C++17.
func TestLibHeader_SelfAndMutualStructs(t *testing.T) {
	tools := needTools(t)
	root := writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"refs"`, "bins": "[" + libBin("refs", ".", "static", "") + "]"}),
		"lib.mk": selfRefSource,
	})
	b := mustLib(t, root, types.X86_64Linux, types.Dev, t.TempDir(), t.TempDir(), false)
	dir := filepath.Dir(b.paths[0])
	h := readFile(t, filepath.Join(dir, "refs.h"))
	if got, want := noEntries(t, "refs", "the header", headerDecls(h)), []string{"AtD", "Depth", "Walk", "WalkB", "WalkC"}; !slices.Equal(got, want) {
		t.Errorf("header declares %q, want %q:\n%s", got, want, h)
	}
	for _, std := range [][]string{{"-x", "c", "-std=c11"}, {"-x", "c", "-std=c99", "-pedantic"}, {"-x", "c++", "-std=c++17"}} {
		runTool(t, dir, tools.Clang, append(append([]string{"--target=x86_64-unknown-linux-gnu", "-ffreestanding", "-Wall", "-Werror", "-fsyntax-only"}, std...), filepath.Join(dir, "refs.h"))...)
	}
}

// TestLibStatic_WarmColdSame: a static library's bytes do not
// depend on the cache: a warm build, a cold build, a fresh cache and
// --no-cache give the same archive, dev and release, COFF and ELF.
func TestLibStatic_WarmColdSame(t *testing.T) {
	needTools(t)
	for _, target := range []types.Target{types.X86_64Linux, types.X86_64Windows} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(target.Name+"/"+prof.String(), func(t *testing.T) {
				root := libPackage(t, "static")
				cache := t.TempDir()
				cold := mustLib(t, root, target, prof, t.TempDir(), cache, false).paths[0]
				warm := mustLib(t, root, target, prof, t.TempDir(), cache, false).paths[0]
				fresh := mustLib(t, root, target, prof, t.TempDir(), t.TempDir(), false).paths[0]
				none := mustLib(t, root, target, prof, t.TempDir(), "", false).paths[0]
				base := readFile(t, cold)
				for _, c := range []struct{ name, path string }{{"warm", warm}, {"fresh cache", fresh}, {"--no-cache", none}} {
					if readFile(t, c.path) != base {
						t.Errorf("%s build of %s differs from the cold build", c.name, filepath.Base(cold))
					}
				}
			})
		}
	}
}

// noCFormSource has exports with no C form beside one with one.
const noCFormSource = `export enum Color {
    Red
    Green
}

export error Bad(code: i64)

export extern Twice(n: i32): i32 {
    return n * 2
}

export extern Opt(n: ?i32): i32 {
    return n else { return 0 }
}

export extern Slice(b: []u8): int {
    return b.Len()
}

export extern Col(c: Color): i32 {
    return 0
}

export extern Fails(n: i32): Bad!i32 {
    if n > 0 {
        fail Bad(code: 1)
    }
    return n
}
`

// TestLibExports_NoCFormRefused: an export extern of a library whose
// parameter or result has no C form (an optional, a slice, an enum, an error
// union) is E1109 at check, once each, and the build makes no library; one
// with a C form (Twice) is not named.
func TestLibExports_NoCFormRefused(t *testing.T) {
	needTools(t)
	root := writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"nc"`, "bins": "[" + libBin("nc", ".", "dynamic", "") + "]"}),
		"lib.mk": noCFormSource,
	})
	b := libBuildIn(t, root, types.X86_64Windows, types.Dev, t.TempDir(), t.TempDir(), false)
	if b.err != nil {
		t.Fatal(b.err)
	}
	for _, d := range b.r.Report.Diags {
		if d.Diag.Code != diag.E1109 {
			t.Errorf("%v, want only E1109", d.Diag.Code)
		}
	}
	text := b.r.Report.Text(b.r.Sources)
	if n := strings.Count(text, "E1109 no-c-form"); n != 4 {
		t.Errorf("%d E1109, want 4 (Opt, Slice, Col, Fails):\n%s", n, text)
	}
	for _, want := range []string{"`?i32`", "`[]u8`", "`Color`", "`Bad!i32`"} {
		if !strings.Contains(text, want) {
			t.Errorf("no E1109 names %s:\n%s", want, text)
		}
	}
	if len(b.paths) != 0 {
		t.Errorf("a refused library was written: %v", b.paths)
	}
}

// TestLibDebug_Reproducible: two --debug builds of a dynamic library from
// different cache and output directories give the same DLL and PDB bytes:
// pdbLink names the import library and the .def by base name, as it does the
// output and the objects.
func TestLibDebug_Reproducible(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("SKIP: the PDB is checked on the Windows leg")
	}
	needTools(t)
	root := libPackage(t, "dynamic")
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			one := mustLib(t, root, types.X86_64Windows, prof, t.TempDir(), t.TempDir(), true).paths[0]
			two := mustLib(t, root, types.X86_64Windows, prof, t.TempDir(), t.TempDir(), true).paths[0]
			for _, f := range []string{"mlib.dll", "mlib.pdb", "mlib.lib"} {
				a, b := filepath.Join(filepath.Dir(one), f), filepath.Join(filepath.Dir(two), f)
				if readFile(t, a) != readFile(t, b) {
					t.Errorf("%s differs between two --debug builds", f)
				}
			}
		})
	}
}

// TestLibManifest_KindAndABI: an unknown pkg.Bin.kind ("shared", or
// "object", which is not supported) and an abi that is not a decimal number
// are E0202 at the manifest step, never a silent executable or a link-time
// E0001.
func TestLibManifest_KindAndABI(t *testing.T) {
	for _, c := range []struct{ kind, abi, field string }{
		{"shared", "", "bins[0].kind"}, {"object", "", "bins[0].kind"}, {"dynamic", "x1", "bins[0].abi"}, {"static", "1.2", "bins[0].abi"},
	} {
		t.Run(c.kind+"/"+c.abi, func(t *testing.T) {
			root := writeTree(t, map[string]string{
				"pkg.mk": manifest(map[string]string{"name": `"k"`, "bins": "[" + libBin("k", ".", c.kind, c.abi) + "]"}),
				"lib.mk": "export extern Twice(n: i32): i32 {\n    return n * 2\n}\n",
			})
			s, err := NewSession(Config{Root: root, Target: types.X86_64Windows})
			if err != nil {
				t.Fatal(err)
			}
			r := s.Check()
			text := r.Report.Text(r.Sources)
			if len(r.Report.Diags) == 0 || r.Report.Diags[0].Diag.Code != diag.E0202 || !strings.Contains(text, c.field) {
				t.Errorf("want E0202 at %s, got:\n%s", c.field, text)
			}
		})
	}
	// The accepted values still check clean.
	for _, c := range []struct{ kind, abi string }{{"dynamic", ""}, {"static", "12"}} {
		root := writeTree(t, map[string]string{
			"pkg.mk": manifest(map[string]string{"name": `"k"`, "bins": "[" + libBin("k", ".", c.kind, c.abi) + "]"}),
			"lib.mk": "export extern Twice(n: i32): i32 {\n    return n * 2\n}\n",
		})
		s, err := NewSession(Config{Root: root, Target: types.X86_64Windows})
		if err != nil {
			t.Fatal(err)
		}
		if r := s.Check(); len(r.Report.Diags) > 0 {
			t.Errorf("kind %q abi %q: %s", c.kind, c.abi, r.Report.Text(r.Sources))
		}
	}
}

// TestLibRun_Refused: mk run on a library bin is a usage error naming the
// library, not E0001 "undefined symbol CallMain".
func TestLibRun_Refused(t *testing.T) {
	tools := needTools(t)
	s, err := NewSession(Config{Root: libPackage(t, "dynamic"), Target: HostTarget(), Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, status, err := s.Run(nil, RunIO{})
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "library") || status != -1 {
		t.Errorf("status %d, err %v, report:\n%s", status, err, r.Report.Text(r.Sources))
	}
}

// helperMember is an archive member a library's link plan names.
const helperMember = "int Helper(int n) { return n + 40; }\n"

// TestLibStatic_MergesLinks: a static library applies its package's links: a
// static link's archive members join the library, so a host links the one
// library and nothing else.
func TestLibStatic_MergesLinks(t *testing.T) {
	tools := needTools(t)
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			file := "h.lib"
			if target.OS == types.OSLinux {
				file = "libh.a"
			}
			root := writeTree(t, map[string]string{
				"pkg.mk": manifest(map[string]string{"name": `"sl"`, "bins": "[" + libBin("sl", ".", "static", "") + "]",
					"unsafe_modules": `["."]`, "links": "[" + linkLit("h", "static", "lib/"+file, target.Name) + "]"}),
				"lib.mk": "extern Helper(n: i32): i32\n\nexport extern Use(n: i32): i32 {\n    unsafe \"Helper is pure\" {\n        return Helper(n)\n    }\n}\n",
			})
			cArchive(t, tools, filepath.Join(root, "lib", file), target, []string{helperMember})
			b := mustLib(t, root, target, types.Dev, t.TempDir(), t.TempDir(), false)
			idx := archiveIndex(t, b.paths[0])
			if !slices.Contains(idx, "Helper") || !slices.Contains(idx, "Use") {
				t.Errorf("the static library's index lacks Helper or Use: %q", idx)
			}
		})
	}
}
