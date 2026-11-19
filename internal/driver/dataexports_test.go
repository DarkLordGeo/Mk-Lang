// Why: `export extern var` must produce real data symbols in executables
// and libraries (GDB's JIT interface reads one by its C name), and Linux
// data exports must not be preempted by a host's copy relocation.
// What: TestExeDefinesExportExternVar, TestLibraryDataExports,
// TestElfDataExportsProtected and TestDefMarkDataOwnLine.
// How: builds mkh147Lib as an executable, a DLL and a .so, reads the symbol
// and export tables with debug/elf and the tools, and links C hosts.

package driver

import (
	"bytes"
	"context"
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"mk/internal/types"
)

// mkh147Lib is a library whose own package defines two data exports:
// GDB's JIT descriptor, by its exact C name, which Bump writes,
// and Ticks, which no Mk code reads.
const mkh147Lib = `export struct JitDesc {
    export version: u32
    export action: u32
    export relevant: u64
    export first: u64
}

/// The descriptor GDB reads.
export extern var __jit_debug_descriptor: JitDesc

export extern var Ticks: u64

/// Bump sets the descriptor's version to v and answers what it reads back.
export extern Bump(v: u32): u32 {
    unsafe "invariant: the descriptor is this library's own data; established by: its definition above" {
        p := &__jit_debug_descriptor
        (*p).version = v
        return __jit_debug_descriptor.version
    }
}
`

func mkh147Package(t *testing.T, kind string) string {
	return writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"jd"`, "unsafe_modules": `["."]`,
			"bins": `[pkg.Bin(name: "jd", root: ".", link_script: "", cpu: "", subsystem: "", kind: "` + kind + `", abi: "")]`}),
		"lib.mk": mkh147Lib,
	})
}

// TestExeDefinesExportExternVar: an executable that defines
// `export extern var __jit_debug_descriptor` (and an unread one) builds for
// x86_64-linux, dev and release with symbols, and both are defined data
// objects of its symbol table under their C names; on the host it builds
// and runs, writing and reading the var. Without data exports this fails with
// E0001, "undefined symbol __jit_debug_descriptor".
func TestExeDefinesExportExternVar(t *testing.T) {
	tools := needTools(t)
	src := strings.Replace(mkh147Lib, "export extern Bump", "Bump", 1) + `
export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try Print(mut out, "bump {v}\n", (v: Bump(7)))
}
`
	root := writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"jx"`, "unsafe_modules": `["."]`,
			"bins": `[pkg.Bin(name: "jx", root: ".", link_script: "", cpu: "", subsystem: "console", kind: "", abi: "")]`}),
		"main.mk": src,
	})
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run("linux-"+prof.String(), func(t *testing.T) {
			s, err := NewSession(Config{Root: root, Target: types.X86_64Linux, Profile: prof, Symbols: true, Tools: tools, CacheDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			r, paths, err := s.Build(filepath.Join(t.TempDir(), "jx"))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Report.Diags) > 0 || len(paths) != 1 {
				t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
			}
			f, err := elf.Open(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			syms, err := f.Symbols()
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []struct {
				name string
				size uint64
			}{{"__jit_debug_descriptor", 24}, {"Ticks", 8}} {
				i := slices.IndexFunc(syms, func(s elf.Symbol) bool { return s.Name == want.name })
				if i < 0 {
					t.Errorf("%s is not in the symbol table", want.name)
					continue
				}
				s := syms[i]
				if s.Section == elf.SHN_UNDEF || elf.ST_TYPE(s.Info) != elf.STT_OBJECT || elf.ST_BIND(s.Info) != elf.STB_GLOBAL || s.Size != want.size {
					t.Errorf("%s: section %v type %v bind %v size %d, want a defined global object of %d bytes",
						want.name, s.Section, elf.ST_TYPE(s.Info), elf.ST_BIND(s.Info), s.Size, want.size)
				}
			}
		})
	}
	if runtime.GOOS != "windows" {
		return
	}
	s, err := NewSession(Config{Root: root, Target: types.X86_64Windows, Profile: types.Dev, Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "jx.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("windows build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
	}
	if out, _, code := runExe(t, types.X86_64Windows, paths[0], ""); code != 0 || out != "bump 7\n" {
		t.Errorf("run: exit %d, stdout %q", code, out)
	}
}

// TestLibraryDataExports: a dynamic library exports its package's
// export extern vars as data: the DLL's export table and the import
// library mark them DATA, the .so's dynamic symbols hold them as global
// objects at the version node, and the header declares them (dllimport for
// the DLL only). A C host reads and writes the DLL's descriptor through
// the header. Without data exports the link fails (undefined symbol).
func TestLibraryDataExports(t *testing.T) {
	tools := needTools(t)
	so := buildLib(t, mkh147Package(t, "dynamic"), types.X86_64Linux, types.Dev, t.TempDir())
	f, err := elf.Open(so)
	if err != nil {
		t.Fatal(err)
	}
	syms, err := f.DynamicSymbols()
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	var data []string
	for _, s := range syms {
		if s.Section != elf.SHN_UNDEF && elf.ST_TYPE(s.Info) == elf.STT_OBJECT && elf.ST_BIND(s.Info) == elf.STB_GLOBAL {
			data = append(data, s.Name+"@"+s.Version)
		}
	}
	slices.Sort(data)
	if want := []string{"Ticks@JD_1", "__jit_debug_descriptor@JD_1"}; !slices.Equal(data, want) {
		t.Errorf("exported data %q, want %q", data, want)
	}
	h := readFile(t, filepath.Join(filepath.Dir(so), "jd.h"))
	for _, want := range []string{"/* The descriptor GDB reads. */\nextern JitDesc __jit_debug_descriptor;", "extern uint64_t Ticks;", "struct JitDesc {"} {
		if !strings.Contains(h, want) {
			t.Errorf(".so header lacks %q:\n%s", want, h)
		}
	}
	if runtime.GOOS != "windows" {
		t.Skip("SKIP: the DLL leg needs a Windows host")
	}
	dyn := t.TempDir()
	dll := buildLib(t, mkh147Package(t, "dynamic"), types.X86_64Windows, types.Dev, dyn)
	pe := dllExports(t, dll)
	for _, n := range []string{"__jit_debug_descriptor", "Ticks", "Bump"} {
		if !slices.Contains(pe, n) {
			t.Errorf("DLL exports %q, lack %s", pe, n)
		}
	}
	// DATA in the .def: the import library has the data's __imp_ pointer
	// and no code thunk under its name, so a host that declares the var
	// without dllimport fails to link instead of reading a thunk's bytes.
	if idx := archiveIndex(t, filepath.Join(dyn, "jd.lib")); !slices.Contains(idx, "__imp___jit_debug_descriptor") ||
		slices.Contains(idx, "__jit_debug_descriptor") || !slices.Contains(idx, "Bump") {
		t.Errorf("import library index %q", idx)
	}
	h = readFile(t, filepath.Join(dyn, "jd.h"))
	if !strings.Contains(h, "__declspec(dllimport) extern JitDesc __jit_debug_descriptor;") || strings.Contains(h, "protected symbols") {
		t.Errorf("DLL header:\n%s", h)
	}
	for _, std := range [][]string{{"-x", "c", "-std=c11"}, {"-x", "c++", "-std=c++17"}} {
		runTool(t, dyn, tools.Clang, append(append([]string{"--target=x86_64-pc-windows-msvc", "-ffreestanding", "-Wall", "-Werror", "-fsyntax-only"}, std...), filepath.Join(dyn, "jd.h"))...)
	}
	k32 := filepath.Join(t.TempDir(), "k32.lib")
	if err := tools.ImportLib(context.Background(), "kernel32.dll", []string{"ExitProcess"}, "x64", k32); err != nil {
		t.Fatal(err)
	}
	// 5 from Bump, then 5 read through the data import, plus 3 written by C
	// and read back by Mk's next Bump argument: 5 + 5 + 3 = 13.
	host := `#include "jd.h"
__declspec(dllimport) void __stdcall ExitProcess(unsigned code);
void start(void) {
    uint32_t a = Bump(5);
    uint32_t b = __jit_debug_descriptor.version;
    Ticks = 3;
    ExitProcess(a + b + (unsigned)Ticks);
}
`
	exe := clangHost(t, tools, dyn, "host", host, filepath.Join(dyn, "jd.lib"), k32)
	if _, _, code := runExe(t, types.X86_64Windows, exe, ""); code != 13 {
		t.Errorf("C host through the import library: exit %d, want 13", code)
	}
	st := t.TempDir()
	lib := buildLib(t, mkh147Package(t, "static"), types.X86_64Windows, types.Dev, st)
	if h := readFile(t, filepath.Join(st, "jd.h")); strings.Contains(h, "dllimport") {
		t.Errorf("static library header has dllimport:\n%s", h)
	}
	exe = clangHost(t, tools, st, "host", host, lib)
	if _, _, code := runExe(t, types.X86_64Windows, exe, ""); code != 13 {
		t.Errorf("C host linked statically: exit %d, want 13", code)
	}
}

// TestElfDataExportsProtected: a Linux .so's data exports are
// protected (STV_PROTECTED in .dynsym), dev and release, so a C host that
// would take a copy relocation of one is refused at link instead of
// reading a copy the library never sees (the library binds its own
// references to its own definition, -Bsymbolic). A host built -fPIE (clang's
// default) reaches the data through the GOT and links with no copy
// relocation; a -fno-pic -no-pie host and a -fdirect-access-external-data
// PIE host fail with ld.lld's "cannot preempt symbol". With default
// visibility both hosts would link with R_X86_64_COPY.
func TestElfDataExportsProtected(t *testing.T) {
	tools := needTools(t)
	ld, ldPre := tools.LDLLD, []string(nil)
	if ld == "" {
		ld, ldPre = tools.LLD, []string{"-flavor", "gnu"}
	}
	host := `#include "jd.h"
uint32_t a, b;
void _start(void) {
    a = Bump(5);
    b = __jit_debug_descriptor.version;
    Ticks = 3;
    for (;;) {
    }
}
`
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			so := buildLib(t, mkh147Package(t, "dynamic"), types.X86_64Linux, prof, t.TempDir())
			dir := filepath.Dir(so)
			f, err := elf.Open(so)
			if err != nil {
				t.Fatal(err)
			}
			syms, err := f.DynamicSymbols()
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range []string{"__jit_debug_descriptor", "Ticks"} {
				i := slices.IndexFunc(syms, func(s elf.Symbol) bool { return s.Name == n })
				if i < 0 {
					t.Errorf("%s is not a dynamic symbol", n)
				} else if v := elf.ST_VISIBILITY(syms[i].Other); v != elf.STV_PROTECTED {
					t.Errorf("%s: visibility %v, want STV_PROTECTED", n, v)
				}
			}
			if h := readFile(t, filepath.Join(dir, "jd.h")); !strings.Contains(h, "/* The data exports are protected symbols of a shared library: a host reaches them") ||
				!strings.Contains(h, "export extern functions and vars") {
				t.Errorf(".so header lacks the GOT note:\n%s", h)
			}
			if err := os.WriteFile(filepath.Join(dir, "host.c"), []byte(host), 0o644); err != nil {
				t.Fatal(err)
			}
			link := func(name string, cflags, ldflags []string) (string, error) {
				obj := filepath.Join(dir, name+".o")
				runTool(t, dir, tools.Clang, append(append([]string{"--target=x86_64-unknown-linux-gnu", "-ffreestanding",
					"-fno-stack-protector", "-Wall", "-Werror", "-std=c11", "-c", "-I" + dir}, cflags...), "host.c", "-o", obj)...)
				out := filepath.Join(dir, name)
				args := append(append(slices.Clone(ldPre), ldflags...), obj, so, "-o", out)
				var e bytes.Buffer
				cmd := exec.Command(ld, args...)
				cmd.Dir, cmd.Stdout, cmd.Stderr = dir, &e, &e
				err := cmd.Run()
				return out, errorsJoin(err, e.String())
			}
			exe, err := link("host-pie", []string{"-fPIE"}, []string{"-pie"})
			if err != nil {
				t.Fatalf("a -fPIE host does not link: %v", err)
			}
			if n := copyRelocs(t, exe); n != 0 {
				t.Errorf("the -fPIE host has %d copy relocations", n)
			}
			for _, h := range []struct {
				name            string
				cflags, ldflags []string
			}{
				{"host-nopie", []string{"-fno-pic"}, []string{"-no-pie"}},
				{"host-direct", []string{"-fPIE", "-fdirect-access-external-data"}, []string{"-pie"}},
			} {
				exe, err := link(h.name, h.cflags, h.ldflags)
				if err == nil {
					t.Errorf("%s links (%d copy relocations); want ld.lld to refuse it", h.name, copyRelocs(t, exe))
				} else if !strings.Contains(err.Error(), "cannot preempt symbol") {
					t.Errorf("%s: %v; want \"cannot preempt symbol\"", h.name, err)
				}
			}
		})
	}
}

// errorsJoin is err with the tool's output, or nil.
func errorsJoin(err error, out string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%v: %s", err, out)
}

// copyRelocs counts the R_X86_64_COPY entries of exe's .rela.dyn.
func copyRelocs(t *testing.T, exe string) int {
	t.Helper()
	f, err := elf.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sec := f.Section(".rela.dyn")
	if sec == nil {
		return 0
	}
	b, err := sec.Data()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for i := 0; i+24 <= len(b); i += 24 {
		if elf.R_X86_64(f.ByteOrder.Uint64(b[i+8:])&0xffffffff) == elf.R_X86_64_COPY {
			n++
		}
	}
	return n
}

// TestDefMarkDataOwnLine: a data export named EXPORTS is marked
// DATA on its own line and the .def's EXPORTS header is left alone (a
// plain string replace would hit the header's line first).
func TestDefMarkDataOwnLine(t *testing.T) {
	text := "LIBRARY x.dll\nEXPORTS\nBump\nEXPORTS\nTicks\n"
	want := "LIBRARY x.dll\nEXPORTS\nBump\nEXPORTS DATA\nTicks DATA\n"
	if got := defMarkData(text, []string{"EXPORTS", "Ticks"}); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
