// Why: compile speed is one of Mk's claims, and it can be compared across
// languages only on one program written the same way in each.
//
// What: the compile-speed table: genSpec, the generated program; the build
// steps of each language and profile; and the measured kinds clean, edit,
// warm, check-body and check-sig. Its own table, never mixed with run time.
//
// How: a generated program of the same shape in C, Rust, Zig, Go and Mk
// (modules of structs and functions that call across modules) is built clean
// (every cache empty) and after a one-function edit, in each language's
// default dev and release profiles, with 8 jobs. After those builds, and
// without changing them, come a no-change rebuild in every language ("warm")
// and, for Mk's dev profile, two `mk check` rows against the
// edit-to-diagnostic budget (check-body, check-sig). The same table runs
// inside WSL for the ELF edit budget (`-platform linux`, started by
// compile-linux, compile_wsl.go), and compile_real.go builds real programs.
//
//	tools/bench compile [-langs c,rust,zig,go,mk]          check every build at a small size, provisional times
//	tools/bench compile -publish [-date D]                  the table, under the machine lock, 8 CPUs

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// compileJobs is the job count for every language.
const compileJobs = 8

// compileMask is the publish affinity: one logical CPU on each of eight
// cores (logical CPUs 0, 2, ..., 14; Windows numbers SMT siblings next to
// each other), so the 8 jobs do not share a core.
const compileMask = "0x5555"

// genSpec is the generated program's shape. Module i (0 <= i < Modules)
// holds Structs structs and Funcs exported functions F0..F{Funcs-1}; each
// F{j} builds a struct S{j % Structs}, runs a short loop and a branch,
// calls the module's private H, and, for i > 0, F{j} of module (i-1)/2 (a
// binary tree of imports, so independent modules can build in parallel).
// main calls every F{j} of every module once and prints the wrapping sum.
// The edit build changes one constant of one function body: F{Funcs/2} of
// module Modules/2.
type genSpec struct {
	Modules int `json:"modules"`
	Funcs   int `json:"funcs"`
	Structs int `json:"structs"`
}

func (g genSpec) parent(i int) int   { return (i - 1) / 2 }
func (g genSpec) editAt() (int, int) { return g.Modules / 2, g.Funcs / 2 }

// consts are the four constants of F{j} of module i; edit adds to the
// third in the edited function only, so every edit build sees text no
// cache has seen.
func (g genSpec) consts(i, j, edit int) (c1, c2, c3, c4 uint64) {
	n := uint64(i*g.Funcs + j)
	c1 = (n + 1) * 0x9e3779b97f4a7c15
	c2 = n
	c3 = n*7 + 3
	if ei, ej := g.editAt(); i == ei && j == ej {
		c3 += uint64(edit)
	}
	c4 = n*13 + 5
	return
}

// checksum is what every generated program prints after edit number edit.
func (g genSpec) checksum(edit int) uint64 {
	var f func(i, j int, x uint64) uint64
	f = func(i, j int, x uint64) uint64 {
		c1, c2, c3, c4 := g.consts(i, j, edit)
		a, b := x^c1, x+c2
		acc := a*3 + b
		for k := uint64(0); k < 4; k++ {
			acc = (acc ^ (acc >> 7)) + k
		}
		if acc&1 == 1 {
			acc += c3
		} else {
			acc ^= c4
		}
		acc += (x^(x>>13))*0x9e3779b97f4a7c15 + uint64(i)
		if i > 0 {
			acc += f(g.parent(i), j, x>>1)
		}
		return acc
	}
	var total uint64
	for i := 0; i < g.Modules; i++ {
		for j := 0; j < g.Funcs; j++ {
			total += f(i, j, uint64(i*g.Funcs+j))
		}
	}
	return total
}

func (g genSpec) check() error {
	if g.Modules < 2 || g.Funcs < 1 || g.Structs < 1 || g.Structs > g.Funcs {
		return fmt.Errorf("compile: need at least 2 modules, 1 function, and 1 to %d structs (got %+v)", g.Funcs, g)
	}
	return nil
}

// compileLang is one language's generator and build.
type compileLang struct {
	ID       string
	Profiles []string // "dev", "release"; Go has one profile
	// files is every source file at edit 0, by path relative to the source
	// directory; module is module i's file at edit number edit.
	files  func(g genSpec) map[string]string
	module func(g genSpec, i, edit int) (string, string)
}

var compileLangs = []compileLang{
	{ID: "c", Profiles: []string{"dev", "release"}, files: genCFiles, module: genCModule},
	{ID: "rust", Profiles: []string{"dev", "release"}, files: genRustFiles, module: genRustModule},
	{ID: "zig", Profiles: []string{"dev", "release"}, files: genZigFiles, module: genZigModule},
	{ID: "go", Profiles: []string{"dev"}, files: genGoFiles, module: genGoModule},
	{ID: "mk", Profiles: []string{"dev", "release"}, files: genMkFiles, module: genMkModule},
}

// compileProfiles states each language's two profiles, for the README and
// RESULTS.md.
var compileProfiles = map[string]string{
	"c":    "dev: CMake's Debug (`-g`, -O0); release: CMake's Release (`-O3 -DNDEBUG`); both `-march=x86-64-v2`, one translation unit per module plus main, compiled 8 at a time by tools/bench (a make: a TU whose object is older than its source), linked by lld",
	"rust": "`cargo build` (dev: opt-level 0, debug info, incremental) and `cargo build --release` (opt-level 3, 16 codegen units, no LTO, not incremental), `-j 8`, one crate of one module per generated module; `-C target-cpu=x86-64-v2` through RUSTFLAGS. std comes precompiled with the toolchain, so a clean Rust build compiles only the crate",
	"zig":  "`zig build-exe` with `-O Debug` (dev) and `-O ReleaseFast` (release), `-j8`, `-target x86_64-windows-gnu -mcpu=x86_64_v2`, one file per module. Clean empties both caches, so it also rebuilds compiler_rt and the MinGW C runtime that Zig builds from source",
	"go":   "`go build -p 8` (Go has one build profile; the release row says so), GOAMD64=v2, one package per module. Clean is a fresh GOCACHE and `-a`, so it rebuilds the std packages the program imports",
	"mk":   "`mk build -j 8` (dev) and `mk build --release --pgo=off -j 8` (release), `--cpu x86-64-v2`, one module directory per module. Clean is a fresh MK_CACHE",
}

func (l compileLang) has(profile string) bool { return slices.Contains(l.Profiles, profile) }

func findCompileLang(id string) (compileLang, bool) {
	for _, l := range compileLangs {
		if l.ID == id {
			return l, true
		}
	}
	return compileLang{}, false
}

// body writes F{j} of module i in the syntax a language's fmt strings give.
type bodyFmt struct {
	open, strct, acc, loop, branch, helper, call, close string
}

func genBody(w *strings.Builder, g genSpec, i, j, edit int, f bodyFmt) {
	c1, c2, c3, c4 := g.consts(i, j, edit)
	fmt.Fprintf(w, f.open, j)
	fmt.Fprintf(w, f.strct, j%g.Structs, c1, c2)
	w.WriteString(f.acc)
	w.WriteString(f.loop)
	fmt.Fprintf(w, f.branch, c3, c4)
	w.WriteString(f.helper)
	if i > 0 {
		fmt.Fprintf(w, f.call, g.parent(i), j)
	}
	w.WriteString(f.close)
}

// C: m<i>.c per module, gen.h with every prototype, main.c.

func genCModule(g genSpec, i, edit int) (string, string) {
	var w strings.Builder
	fmt.Fprintf(&w, "// Generated by tools/bench compile: module %d of %d.\n#include <stdint.h>\n#include \"gen.h\"\n\n", i, g.Modules)
	for k := 0; k < g.Structs; k++ {
		fmt.Fprintf(&w, "struct S%d {\n    uint64_t a;\n    uint64_t b;\n};\n\n", k)
	}
	fmt.Fprintf(&w, "static uint64_t h(uint64_t x) {\n    return (x ^ (x >> 13)) * 0x9e3779b97f4a7c15ULL + %dULL;\n}\n\n", i)
	for j := 0; j < g.Funcs; j++ {
		genBody(&w, g, i, j, edit, bodyFmt{
			open:   fmt.Sprintf("uint64_t m%d_f%%d(uint64_t x) {\n", i),
			strct:  "    struct S%d s = {x ^ 0x%xULL, x + %dULL};\n",
			acc:    "    uint64_t acc = s.a * 3 + s.b;\n",
			loop:   "    for (uint64_t k = 0; k < 4; k++) {\n        acc = (acc ^ (acc >> 7)) + k;\n    }\n",
			branch: "    if ((acc & 1) == 1) {\n        acc = acc + %dULL;\n    } else {\n        acc = acc ^ %dULL;\n    }\n",
			helper: "    acc = acc + h(x);\n",
			call:   "    acc = acc + m%d_f%d(x >> 1);\n",
			close:  "    return acc;\n}\n\n",
		})
	}
	return fmt.Sprintf("m%d.c", i), w.String()
}

func genCFiles(g genSpec) map[string]string {
	files := map[string]string{}
	var h, m strings.Builder
	h.WriteString("// Generated by tools/bench compile.\n#include <stdint.h>\n\n")
	m.WriteString("// Generated by tools/bench compile.\n#include <stdint.h>\n#include <stdio.h>\n#include \"gen.h\"\n\nint main(void) {\n    uint64_t total = 0;\n")
	for i := 0; i < g.Modules; i++ {
		p, s := genCModule(g, i, 0)
		files[p] = s
		for j := 0; j < g.Funcs; j++ {
			fmt.Fprintf(&h, "uint64_t m%d_f%d(uint64_t x);\n", i, j)
			fmt.Fprintf(&m, "    total += m%d_f%d(%d);\n", i, j, i*g.Funcs+j)
		}
	}
	m.WriteString("    printf(\"%llu\\n\", (unsigned long long)total);\n    return 0;\n}\n")
	files["gen.h"], files["main.c"] = h.String(), m.String()
	return files
}

// Rust: a cargo crate, src/m<i>.rs per module.

func genRustModule(g genSpec, i, edit int) (string, string) {
	var w strings.Builder
	fmt.Fprintf(&w, "// Generated by tools/bench compile: module %d of %d.\n\n", i, g.Modules)
	for k := 0; k < g.Structs; k++ {
		fmt.Fprintf(&w, "struct S%d {\n    a: u64,\n    b: u64,\n}\n\n", k)
	}
	fmt.Fprintf(&w, "fn h(x: u64) -> u64 {\n    (x ^ (x >> 13)).wrapping_mul(0x9e3779b97f4a7c15).wrapping_add(%d)\n}\n\n", i)
	for j := 0; j < g.Funcs; j++ {
		genBody(&w, g, i, j, edit, bodyFmt{
			open:   "pub fn f%d(x: u64) -> u64 {\n",
			strct:  "    let s = S%d { a: x ^ 0x%x, b: x.wrapping_add(%d) };\n",
			acc:    "    let mut acc = s.a.wrapping_mul(3).wrapping_add(s.b);\n",
			loop:   "    for k in 0..4u64 {\n        acc = (acc ^ (acc >> 7)).wrapping_add(k);\n    }\n",
			branch: "    if acc & 1 == 1 {\n        acc = acc.wrapping_add(%d);\n    } else {\n        acc ^= %d;\n    }\n",
			helper: "    acc = acc.wrapping_add(h(x));\n",
			call:   "    acc = acc.wrapping_add(crate::m%d::f%d(x >> 1));\n",
			close:  "    acc\n}\n\n",
		})
	}
	return fmt.Sprintf("src/m%d.rs", i), w.String()
}

func genRustFiles(g genSpec) map[string]string {
	files := map[string]string{
		"Cargo.toml": "[package]\nname = \"gen\"\nversion = \"0.1.0\"\nedition = \"2024\"\n\n[workspace]\n",
	}
	var m strings.Builder
	m.WriteString("// Generated by tools/bench compile.\n")
	for i := 0; i < g.Modules; i++ {
		fmt.Fprintf(&m, "mod m%d;\n", i)
		p, s := genRustModule(g, i, 0)
		files[p] = s
	}
	m.WriteString("\nfn main() {\n    let mut total: u64 = 0;\n")
	for i := 0; i < g.Modules; i++ {
		for j := 0; j < g.Funcs; j++ {
			fmt.Fprintf(&m, "    total = total.wrapping_add(m%d::f%d(%d));\n", i, j, i*g.Funcs+j)
		}
	}
	m.WriteString("    println!(\"{}\", total);\n}\n")
	files["src/main.rs"] = m.String()
	return files
}

// Zig: m<i>.zig per module, main.zig.

func genZigModule(g genSpec, i, edit int) (string, string) {
	var w strings.Builder
	fmt.Fprintf(&w, "// Generated by tools/bench compile: module %d of %d.\n", i, g.Modules)
	if i > 0 {
		fmt.Fprintf(&w, "const m%d = @import(\"m%d.zig\");\n", g.parent(i), g.parent(i))
	}
	w.WriteString("\n")
	for k := 0; k < g.Structs; k++ {
		fmt.Fprintf(&w, "const S%d = struct {\n    a: u64,\n    b: u64,\n};\n\n", k)
	}
	fmt.Fprintf(&w, "fn h(x: u64) u64 {\n    return (x ^ (x >> 13)) *%% 0x9e3779b97f4a7c15 +%% %d;\n}\n\n", i)
	for j := 0; j < g.Funcs; j++ {
		genBody(&w, g, i, j, edit, bodyFmt{
			open:   "pub fn fun%d(x: u64) u64 {\n",
			strct:  "    const s = S%d{ .a = x ^ 0x%x, .b = x +%% %d };\n",
			acc:    "    var acc = s.a *% 3 +% s.b;\n",
			loop:   "    var k: u64 = 0;\n    while (k < 4) : (k += 1) {\n        acc = (acc ^ (acc >> 7)) +% k;\n    }\n",
			branch: "    if (acc & 1 == 1) {\n        acc +%%= %d;\n    } else {\n        acc ^= %d;\n    }\n",
			helper: "    acc +%= h(x);\n",
			call:   "    acc +%%= m%d.fun%d(x >> 1);\n",
			close:  "    return acc;\n}\n\n",
		})
	}
	return fmt.Sprintf("m%d.zig", i), w.String()
}

func genZigFiles(g genSpec) map[string]string {
	files := map[string]string{}
	var m strings.Builder
	m.WriteString("// Generated by tools/bench compile.\nconst std = @import(\"std\");\n")
	for i := 0; i < g.Modules; i++ {
		fmt.Fprintf(&m, "const m%d = @import(\"m%d.zig\");\n", i, i)
		p, s := genZigModule(g, i, 0)
		files[p] = s
	}
	m.WriteString("\npub fn main(init: std.process.Init) !void {\n    var total: u64 = 0;\n")
	for i := 0; i < g.Modules; i++ {
		for j := 0; j < g.Funcs; j++ {
			fmt.Fprintf(&m, "    total +%%= m%d.fun%d(%d);\n", i, j, i*g.Funcs+j)
		}
	}
	m.WriteString("    var out: [64]u8 = undefined;\n    var w = std.Io.File.stdout().writer(init.io, &out);\n    try w.interface.print(\"{d}\\n\", .{total});\n    try w.interface.flush();\n}\n")
	files["main.zig"] = m.String()
	return files
}

// Go: a module "gen", package m<i> per module.

func genGoModule(g genSpec, i, edit int) (string, string) {
	var w strings.Builder
	fmt.Fprintf(&w, "// Generated by tools/bench compile: module %d of %d.\n\npackage m%d\n\n", i, g.Modules, i)
	if i > 0 {
		fmt.Fprintf(&w, "import \"gen/m%d\"\n\n", g.parent(i))
	}
	for k := 0; k < g.Structs; k++ {
		fmt.Fprintf(&w, "type s%d struct {\n\ta uint64\n\tb uint64\n}\n\n", k)
	}
	fmt.Fprintf(&w, "func h(x uint64) uint64 {\n\treturn (x^(x>>13))*0x9e3779b97f4a7c15 + %d\n}\n\n", i)
	for j := 0; j < g.Funcs; j++ {
		genBody(&w, g, i, j, edit, bodyFmt{
			open:   "func F%d(x uint64) uint64 {\n",
			strct:  "\ts := s%d{a: x ^ 0x%x, b: x + %d}\n",
			acc:    "\tacc := s.a*3 + s.b\n",
			loop:   "\tfor k := uint64(0); k < 4; k++ {\n\t\tacc = (acc ^ (acc >> 7)) + k\n\t}\n",
			branch: "\tif acc&1 == 1 {\n\t\tacc = acc + %d\n\t} else {\n\t\tacc = acc ^ %d\n\t}\n",
			helper: "\tacc = acc + h(x)\n",
			call:   "\tacc = acc + m%d.F%d(x>>1)\n",
			close:  "\treturn acc\n}\n\n",
		})
	}
	return fmt.Sprintf("m%d/m%d.go", i, i), w.String()
}

func genGoFiles(g genSpec) map[string]string {
	files := map[string]string{"go.mod": "module gen\n\ngo 1.27\n"}
	var m strings.Builder
	m.WriteString("// Generated by tools/bench compile.\n\npackage main\n\nimport (\n\t\"fmt\"\n")
	for i := 0; i < g.Modules; i++ {
		fmt.Fprintf(&m, "\t\"gen/m%d\"\n", i)
		p, s := genGoModule(g, i, 0)
		files[p] = s
	}
	m.WriteString(")\n\nfunc main() {\n\tvar total uint64\n")
	for i := 0; i < g.Modules; i++ {
		for j := 0; j < g.Funcs; j++ {
			fmt.Fprintf(&m, "\ttotal += m%d.F%d(%d)\n", i, j, i*g.Funcs+j)
		}
	}
	m.WriteString("\tfmt.Println(total)\n}\n")
	files["main.go"] = m.String()
	return files
}

// Mk: a package, module directory m<i> per module.

func genMkModule(g genSpec, i, edit int) (string, string) {
	return genMkModuleAt(g, i, edit, "")
}

// The variants of the edited Mk module that the `mk check` rows write
// Each names a token that the edit number makes new, so a check
// that did not read the edit gives the wrong verdict:
//   - mkBadBody: the edited function's constant replaced by the unknown
//     name mkbenchUndefined<c3>, a body edit with one diagnostic;
//   - mkSig (in module sigAt): a new exported function G<edit> after the others, a
//     signature edit (the module's interface changes; no caller breaks,
//     and the checksum is edit's);
//   - mkBadSig: G<edit> with a parameter of the unknown type
//     mkbenchUndefined<edit>, a signature with one diagnostic.
const (
	mkBadBody = "bad-body"
	mkSig     = "sig"
	mkBadSig  = "bad-sig"
)

// mkCheckToken is the unknown name a bad variant of edit writes, which the
// diagnostic must quote.
func (g genSpec) mkCheckToken(variant string, edit int) string {
	if variant == mkBadBody {
		ei, ej := g.editAt()
		_, _, c3, _ := g.consts(ei, ej, edit)
		return fmt.Sprintf("mkbenchUndefined%d", c3)
	}
	return fmt.Sprintf("mkbenchUndefined%d", edit)
}

// sigAt is the module the signature variants are written in: m0, the root
// of the import tree, which every other module imports through its parents
// and main imports directly, so a signature edit there has the most
// importers the generated program has. The body
// variant stays in editAt's module.
func (g genSpec) sigAt() int { return 0 }

// genMkModuleAt is module i at edit number edit, in one of the check
// variants above when i is the variant's module (editAt for mkBadBody,
// sigAt for mkSig and mkBadSig; "" is the program itself).
func genMkModuleAt(g genSpec, i, edit int, variant string) (string, string) {
	return genMkModuleSig(g, i, edit, edit, variant)
}

// genMkModuleSig is genMkModuleAt with the bodies at edit body and the
// signature variants' function named G<n>, so a run of signature edits
// changes G alone.
func genMkModuleSig(g genSpec, i, body, n int, variant string) (string, string) {
	edit := body
	ei, ej := g.editAt()
	if (variant == mkBadBody && i != ei) || ((variant == mkSig || variant == mkBadSig) && i != g.sigAt()) {
		variant = ""
	}
	var w strings.Builder
	fmt.Fprintf(&w, "// Generated by tools/bench compile: module %d of %d.\n\n", i, g.Modules)
	if i > 0 {
		fmt.Fprintf(&w, "import m%d\n\n", g.parent(i))
	}
	for k := 0; k < g.Structs; k++ {
		fmt.Fprintf(&w, "struct S%d {\n    a: u64\n    b: u64\n}\n\n", k)
	}
	fmt.Fprintf(&w, "H(x: u64): u64 {\n    return (x ^ (x >> 13)) *%% 0x9e3779b97f4a7c15 +%% %d\n}\n\n", i)
	for j := 0; j < g.Funcs; j++ {
		branch := "    if acc & 1 == 1 {\n        acc = acc +%% %d\n    } else {\n        acc = acc ^ %d\n    }\n"
		if variant == mkBadBody && j == ej {
			branch = "    if acc & 1 == 1 {\n        acc = acc +%% mkbenchUndefined%d\n    } else {\n        acc = acc ^ %d\n    }\n"
		}
		genBody(&w, g, i, j, edit, bodyFmt{
			open:   "export F%d(x: u64): u64 {\n",
			strct:  "    s := S%d(a: x ^ 0x%x, b: x +%% %d)\n",
			acc:    "    var acc := s.a *% 3 +% s.b\n",
			loop:   "    for k in 0..4 {\n        acc = (acc ^ (acc >> 7)) +% Cast[u64](k)\n    }\n",
			branch: branch,
			helper: "    acc = acc +% H(x)\n",
			call:   "    acc = acc +%% m%d.F%d(x >> 1)\n",
			close:  "    return acc\n}\n\n",
		})
	}
	switch variant {
	case mkSig:
		fmt.Fprintf(&w, "export G%d(x: u64): u64 {\n    return x +%% %d\n}\n\n", n, n)
	case mkBadSig:
		fmt.Fprintf(&w, "export G%d(x: %s): u64 {\n    return 0\n}\n\n", n, g.mkCheckToken(mkBadSig, n))
	}
	return fmt.Sprintf("m%d/m%d.mk", i, i), w.String()
}

func genMkFiles(g genSpec) map[string]string {
	files := map[string]string{
		"pkg.mk": "const Package = pkg.Package(\n    name: \"gen\",\n    bins: [pkg.Bin(name: \"gen\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n",
	}
	var m strings.Builder
	m.WriteString("// Generated by tools/bench compile.\n\n")
	for i := 0; i < g.Modules; i++ {
		fmt.Fprintf(&m, "import m%d\n", i)
		p, s := genMkModule(g, i, 0)
		files[p] = s
	}
	m.WriteString("\nexport Main(sys: &System): WriteError!void {\n    var total: u64 = 0\n")
	for i := 0; i < g.Modules; i++ {
		for j := 0; j < g.Funcs; j++ {
			fmt.Fprintf(&m, "    total = total +%% m%d.F%d(%d)\n", i, j, i*g.Funcs+j)
		}
	}
	m.WriteString("    var out := sys.Stdout()\n    try Print(mut out, \"{total}\\n\", (total))\n}\n")
	files["main.mk"] = m.String()
	return files
}

// compileSetup is one language and profile's directories.
type compileSetup struct {
	Lang    compileLang
	Profile string
	Src     string // generated sources
	Out     string // the executable
	State   string // this profile's caches and objects, deleted for a clean build
}

func newCompileSetup(work string, l compileLang, profile string) compileSetup {
	base := filepath.Join(work, "compile", l.ID)
	exe := ".exe"
	if runtime.GOOS != "windows" {
		exe = "" // the Linux run (compile -platform linux)
	}
	s := compileSetup{Lang: l, Profile: profile, Src: filepath.Join(base, "src"),
		Out: filepath.Join(base, "out-"+profile+exe), State: filepath.Join(base, "state-"+profile)}
	if l.ID == "rust" {
		// cargo writes the executable into its target directory.
		dir := "debug"
		if profile == "release" {
			dir = "release"
		}
		s.Out = filepath.Join(s.State, "target", dir, "gen.exe")
	}
	return s
}

// writeSources writes every file of the generated program at edit 0 into
// a fresh source directory.
func (s compileSetup) writeSources(g genSpec) (lines int, err error) {
	if err := os.RemoveAll(s.Src); err != nil {
		return 0, err
	}
	for p, text := range s.Lang.files(g) {
		path := filepath.Join(s.Src, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return 0, err
		}
		lines += strings.Count(text, "\n")
	}
	return lines, nil
}

// edit rewrites the edited module's file at edit number n.
func (s compileSetup) edit(g genSpec, n int) error {
	ei, _ := g.editAt()
	p, text := s.Lang.module(g, ei, n)
	return os.WriteFile(filepath.Join(s.Src, filepath.FromSlash(p)), []byte(text), 0o644)
}

// editMk rewrites the edited Mk module at edit number n in a check variant
// (genMkModuleAt; "" is edit itself).
func (s compileSetup) editMk(g genSpec, n int, variant string) error {
	ei, _ := g.editAt()
	p, text := genMkModuleAt(g, ei, n, variant)
	return os.WriteFile(filepath.Join(s.Src, filepath.FromSlash(p)), []byte(text), 0o644)
}

// steps are the build's commands: each inner slice runs with up to
// compileJobs at a time, and the slices run in order. Only C has more than
// one command (its translation units, then the link). clean adds the flags
// a clean build needs (Go's -a).
func (s compileSetup) steps(t *toolchain, clean bool) ([][]buildCmd, error) {
	cpu := cpus["x86-64-v2"]
	need := func(path, what string) error {
		if path == "" {
			return fmt.Errorf("%s not found", what)
		}
		return nil
	}
	switch s.Lang.ID {
	case "c":
		if err := need(t.Clang, "clang"); err != nil {
			return nil, err
		}
		flags := []string{"-g"}
		if s.Profile == "release" {
			flags = []string{"-O3", "-DNDEBUG"}
		}
		obj := filepath.Join(s.State, "obj")
		srcs, _ := filepath.Glob(filepath.Join(s.Src, "*.c"))
		slices.Sort(srcs)
		var compile []buildCmd
		var objs []string
		linked := !clean
		for _, src := range srcs {
			o := filepath.Join(obj, strings.TrimSuffix(filepath.Base(src), ".c")+".o")
			objs = append(objs, o)
			if !newer(s.Out, o) {
				linked = false
			}
			if !clean && newer(o, src) && newer(o, filepath.Join(s.Src, "gen.h")) {
				continue // make: the object is up to date
			}
			args := append([]string{t.Clang, "--target=x86_64-pc-windows-msvc", "-std=c17", "-march=" + cpu.C}, flags...)
			compile = append(compile, buildCmd{Dir: s.Src, Env: t.CEnv, Args: append(args, "-c", "-o", o, src)})
		}
		if linked && len(compile) == 0 {
			// make: every object is up to date and the executable is
			// newer than all of them, so a no-change build runs nothing
			// (the "warm" kind). An edit always recompiles a TU, so
			// the edit builds always link.
			return [][]buildCmd{}, nil
		}
		link := append([]string{t.Clang, "--target=x86_64-pc-windows-msvc", "-fuse-ld=lld", "-fms-runtime-lib=static"}, flags...)
		link = append(append(link, "-o", s.Out), objs...)
		return [][]buildCmd{compile, {{Dir: s.Src, Env: t.CEnv, Args: link}}}, nil
	case "rust":
		if err := need(t.Cargo, "cargo"); err != nil {
			return nil, err
		}
		args := []string{t.Cargo, "build", "--offline", "-j", strconv.Itoa(compileJobs)}
		if s.Profile == "release" {
			args = append(args, "--release")
		}
		return [][]buildCmd{{{Dir: s.Src, Env: []string{"CARGO_TARGET_DIR=" + filepath.Join(s.State, "target"),
			"RUSTFLAGS=-C target-cpu=" + cpu.Rust, "CARGO_INCREMENTAL=", "CARGO_BUILD_JOBS="}, Args: args}}}, nil
	case "zig":
		if err := need(t.Zig, "zig"); err != nil {
			return nil, err
		}
		mode := "Debug"
		if s.Profile == "release" {
			mode = "ReleaseFast"
		}
		return [][]buildCmd{{{Dir: s.Src, Args: []string{t.Zig, "build-exe", "-O", mode, "-j" + strconv.Itoa(compileJobs),
			"-target", "x86_64-windows-gnu", "-mcpu=" + cpu.Zig,
			"--cache-dir", filepath.Join(s.State, "zig-cache"), "--global-cache-dir", filepath.Join(s.State, "zig-global"),
			"-femit-bin=" + s.Out, "main.zig"}}}}, nil
	case "go":
		if err := need(t.Go, "go"); err != nil {
			return nil, err
		}
		args := []string{t.Go, "build", "-p", strconv.Itoa(compileJobs), "-pgo=off"}
		if clean {
			args = append(args, "-a")
		}
		return [][]buildCmd{{{Dir: s.Src, Env: []string{"GOFLAGS=", "GOTOOLCHAIN=local", "GOWORK=off", "GOAMD64=" + cpu.GoAmd64,
			"CGO_ENABLED=0", "GOOS=windows", "GOARCH=amd64", "GOCACHE=" + filepath.Join(s.State, "gocache")},
			Args: append(args, "-o", s.Out, ".")}}}, nil
	case "mk":
		if err := need(t.Mk, "mk"); err != nil {
			return nil, err
		}
		args := []string{t.Mk, "build", "-j", strconv.Itoa(compileJobs), "--cpu", cpu.Mk}
		if s.Profile == "release" {
			args = append(args, "--release", "--pgo=off")
		}
		return [][]buildCmd{{{Dir: s.Src, Env: []string{"MK_CACHE=" + filepath.Join(s.State, "mkcache"), "MK_STD=" + t.MkStd},
			Args: append(args, "-o", s.Out, s.Src)}}}, nil
	}
	return nil, fmt.Errorf("compile: no build for %q", s.Lang.ID)
}

// newer reports whether a exists and is strictly newer than b (an equal
// time, possible at the clock's step, rebuilds).
func newer(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	if err != nil {
		return true
	}
	return sa.ModTime().After(sb.ModTime())
}

// runSteps runs a build's steps and returns its wall time, from the first
// command's start to the last one's end.
func runSteps(steps [][]buildCmd) (time.Duration, error) {
	start := time.Now()
	for _, step := range steps {
		var mu sync.Mutex
		var errs []error
		sem := make(chan struct{}, compileJobs)
		var wg sync.WaitGroup
		for _, c := range step {
			wg.Add(1)
			sem <- struct{}{}
			go func(c buildCmd) {
				defer wg.Done()
				defer func() { <-sem }()
				if err := os.MkdirAll(filepath.Dir(outOf(c)), 0o755); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
					return
				}
				cmd := exec.Command(c.Args[0], c.Args[1:]...)
				cmd.Dir = c.Dir
				cmd.Env = compileEnv(os.Environ(), c.Env)
				if out, err := cmd.CombinedOutput(); err != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("%s: %v: %s", filepath.Base(c.Args[0]), err, firstLines(string(out), 12)))
					mu.Unlock()
				}
			}(c)
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			return 0, err
		}
	}
	return time.Since(start), nil
}

// compileEnv is the build's environment: the caller's, with every variable
// the command sets replaced (an empty value clears it).
func compileEnv(base, set []string) []string {
	var out []string
	for _, kv := range base {
		keep := true
		for _, s := range set {
			k, _, _ := strings.Cut(s, "=")
			if envKeyIs(kv, k) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	for _, s := range set {
		if k, v, _ := strings.Cut(s, "="); v != "" || k == "MK_STD" {
			out = append(out, s)
		}
	}
	return out
}

// outOf is the file a command writes (its -o or -femit-bin value), so its
// directory can be made first; "" when it names none.
func outOf(c buildCmd) string {
	for i, a := range c.Args {
		if a == "-o" && i+1 < len(c.Args) {
			return c.Args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "-femit-bin="); ok {
			return v
		}
	}
	return filepath.Join(c.Dir, "x")
}

// compileCell is one language, profile and kind of build.
type compileCell struct {
	Lang     string     `json:"lang"`
	Profile  string     `json:"profile"`
	Kind     string     `json:"kind"` // "clean", "edit", "warm" (no change), "check-body" or "check-sig"
	Times    []float64  `json:"times"`
	Sum      summary    `json:"summary"`
	Builds   []buildCmd `json:"builds"` // the commands of one such build
	Lines    int        `json:"lines"`
	Excluded string     `json:"excluded,omitempty"`
	// Nothing marks a no-change cell whose builds ran no command (C's
	// make): its times are tools/bench's own loop, so it is printed as
	// such and never the best of its kind.
	Nothing bool `json:"nothing,omitempty"`
}

type compileResults struct {
	Run   facts         `json:"run"`
	Spec  genSpec       `json:"spec"`
	Jobs  int           `json:"jobs"`
	Runs  string        `json:"runs"`
	Cells []compileCell `json:"cells"`
	// Platform is "linux" for the run inside WSL (results/<date>/compile-linux).
	Platform string `json:"platform,omitempty"`
}

type compileOptions struct {
	Root, Work, Mk     string
	Spec               genSpec
	Langs              []string
	Publish            bool
	CleanRuns, EditRun int
	// WarmRuns and CheckRuns are the warm and check kinds: timed no-change
	// builds after the edit builds (one untimed first), and timed `mk check`
	// runs per check row (one untimed pair first); 0 leaves a kind out.
	WarmRuns, CheckRuns int
	LockPath            string
	Mask                uint64
	Date                string
	Log                 func(string)
	// Platform "linux" is the run inside WSL that compile-linux starts:
	// Mk only, the affinity set from outside by taskset, the machine lock
	// held by the caller (NoLock), results in results/<date>/compile-linux.
	Platform string
	NoLock   bool
	MkLabel  string // the mk's name in the facts when it was copied (Linux)
}

// compileDir is the results subdirectory of a platform's compile table.
func compileDir(platform string) string {
	if platform == "linux" {
		return "compile-linux"
	}
	return "compile"
}

// compileTable builds every language and profile clean CleanRuns times and
// after EditRun one-function edits (two more first, untimed), running the
// program after every build and checking its checksum against the
// generator's, so a stale cache fails the cell instead of timing it.
func compileTable(o compileOptions, w func(string)) error {
	if o.Publish {
		if err := refuseExperiment(o.Mk, ""); err != nil {
			return err
		}
	}
	switch {
	case o.Platform == "" && runtime.GOOS != "windows":
		return errors.New("the compile-speed table runs on Windows (inside WSL: -platform linux, started by compile-linux)")
	case o.Platform == "linux" && runtime.GOOS != "linux":
		return errors.New("-platform linux is the run inside WSL that compile-linux starts")
	case o.Platform != "" && o.Platform != "linux":
		return fmt.Errorf("compile: no platform %q", o.Platform)
	}
	if err := o.Spec.check(); err != nil {
		return err
	}
	var langs []compileLang
	for _, id := range o.Langs {
		l, ok := findCompileLang(id)
		if !ok {
			return fmt.Errorf("compile: no language %q (c, rust, zig, go, mk)", id)
		}
		if o.Platform == "linux" && l.ID != "mk" {
			// The other languages' builds are the Windows table's
			// (their command lines name Windows targets).
			return fmt.Errorf("compile -platform linux builds Mk only, not %s", l.ID)
		}
		langs = append(langs, l)
	}
	if o.Publish && !o.NoLock {
		lock, err := acquireLock(o.LockPath, o.Log)
		if err != nil {
			return err
		}
		defer lock.Release()
		o.Log("holding the machine lock " + lock.Path())
	}
	t, err := findToolchain(o.Work, o.Mk)
	if err != nil {
		return err
	}
	runs := fmt.Sprintf("%d clean builds and %d one-function-edit builds (after 2 untimed) per cell", o.CleanRuns, o.EditRun)
	if o.WarmRuns > 0 {
		runs += fmt.Sprintf("; %d no-change builds (after 1 untimed)", o.WarmRuns)
	}
	if o.CheckRuns > 0 {
		runs += fmt.Sprintf("; %d `mk check` runs per check row (after 1 untimed pair)", o.CheckRuns)
	}
	res := compileResults{Spec: o.Spec, Jobs: compileJobs, Runs: runs, Platform: o.Platform}
	if o.Publish {
		res.Run = gatherFacts(o.Root, t)
		res.Run.Date = time.Now().Format("2006-01-02 15:04")
		res.Run.MkBinary, res.Run.MkSHA256, res.Run.MkStd = o.Mk, fileSHA(o.Mk), t.MkStd
		if o.MkLabel != "" {
			res.Run.MkBinary = o.MkLabel
		}
		if t.Cargo != "" && o.Platform == "" {
			res.Run.Versions["cargo"] = firstLine(output(t.Cargo, "--version"))
		}
		if o.Platform == "linux" {
			res.Run.Affinity = "taskset -c " + linuxCompileCPUs + " (8 virtual CPUs of WSL 2, set by compile-linux on the whole run; a virtual CPU is not a core)"
		} else {
			if err := setAffinity(o.Mask); err != nil {
				return err
			}
			res.Run.Affinity = fmt.Sprintf("%#x (one logical CPU on each of 8 cores, set on tools/bench and inherited by every compiler)", o.Mask)
		}
	}
	tag := "PROVISIONAL (no machine lock)"
	if o.Publish {
		tag = "published"
	}
	failed := 0
	for _, profile := range []string{"dev", "release"} {
		for _, l := range langs {
			if !l.has(profile) {
				continue
			}
			s := newCompileSetup(o.Work, l, profile)
			clean, edit := compileCell{Lang: l.ID, Profile: profile, Kind: "clean"}, compileCell{Lang: l.ID, Profile: profile, Kind: "edit"}
			err := measureCompile(t, s, o, &clean, &edit)
			if err != nil {
				failed++
				msg := firstLines(err.Error(), 8)
				if clean.Sum.N == 0 {
					clean.Excluded = msg
				}
				edit.Excluded = msg
				w(fmt.Sprintf("FAIL compile %s %s: %s", l.ID, profile, msg))
				res.Cells = append(res.Cells, clean, edit)
				continue
			}
			w(fmt.Sprintf("ok compile %s %s (%d lines): clean median %s (min %s), edit median %s (min %s), %s",
				l.ID, profile, clean.Lines, fmtSec(clean.Sum.Median), fmtSec(clean.Sum.Min), fmtSec(edit.Sum.Median), fmtSec(edit.Sum.Min), tag))
			res.Cells = append(res.Cells, clean, edit)
			// The warm and check kinds run after the clean and edit builds,
			// so those are measured as they would be without them.
			last := o.EditRun + 2
			more := func(c compileCell, err error) {
				if err != nil {
					failed++
					c.Excluded = firstLines(err.Error(), 8)
					w(fmt.Sprintf("FAIL compile %s %s %s: %s", l.ID, profile, c.Kind, c.Excluded))
				} else {
					w(fmt.Sprintf("ok compile %s %s %s: median %s (min %s), %s", l.ID, profile, c.Kind, fmtSec(c.Sum.Median), fmtSec(c.Sum.Min), tag))
				}
				res.Cells = append(res.Cells, c)
			}
			if o.WarmRuns > 0 {
				c := compileCell{Lang: l.ID, Profile: profile, Kind: "warm", Lines: clean.Lines}
				err := measureWarm(t, s, o, last, &c)
				more(c, err)
			}
			if o.CheckRuns > 0 && l.ID == "mk" && profile == "dev" {
				// Edit numbers after the last edit build: each check
				// row uses CheckRuns+1 of them.
				body := compileCell{Lang: l.ID, Profile: profile, Kind: "check-body", Lines: clean.Lines}
				err := measureCheck(t, s, o, last, mkBadBody, &body)
				more(body, err)
				sig := compileCell{Lang: l.ID, Profile: profile, Kind: "check-sig", Lines: clean.Lines}
				err = measureCheck(t, s, o, last+o.CheckRuns+1, mkSig, &sig)
				more(sig, err)
			}
		}
	}
	if o.Publish {
		dir := filepath.Join(o.Root, "tests", "bench", "results", o.Date, compileDir(o.Platform))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "results.json"), append(b, '\n'), 0o644); err != nil {
			return err
		}
		if o.Platform == "linux" {
			// compile-linux renders RESULTS.md on Windows after this run.
		} else if _, err := os.Stat(filepath.Join(o.Root, "tests", "bench", "results", o.Date, "results.json")); err == nil {
			if err := writeResultsMD(o.Root, o.Date); err != nil {
				return err
			}
		} else {
			o.Log("results/" + o.Date + "/results.json is not there yet, so RESULTS.md is rendered by the main publish of that date")
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d compile cells failed", failed)
	}
	return nil
}

func measureCompile(t *toolchain, s compileSetup, o compileOptions, clean, edit *compileCell) error {
	lines, err := s.writeSources(o.Spec)
	if err != nil {
		return err
	}
	clean.Lines, edit.Lines = lines, lines
	check := func(n int) error {
		out, _, err := runOnce(s.Out, nil)
		if err != nil {
			return err
		}
		if want := strconv.FormatUint(o.Spec.checksum(n), 10); out != want {
			return fmt.Errorf("after edit %d the program printed %q, want %s (a stale build)", n, out, want)
		}
		return nil
	}
	for r := 0; r < o.CleanRuns; r++ {
		if err := os.RemoveAll(s.State); err != nil {
			return err
		}
		os.Remove(s.Out)
		steps, err := s.steps(t, true)
		if err != nil {
			return err
		}
		d, err := runSteps(steps)
		if err != nil {
			return fmt.Errorf("clean build: %w", err)
		}
		if err := check(0); err != nil {
			return err
		}
		if r == 0 {
			clean.Builds = slices.Concat(steps...)
		}
		clean.Times = append(clean.Times, d.Seconds())
		o.Log(fmt.Sprintf("compile %s %s clean %d: %s", s.Lang.ID, s.Profile, r+1, fmtSec(d.Seconds())))
	}
	clean.Sum = summarize(clean.Times)
	for n := 1; n <= o.EditRun+2; n++ {
		if err := s.edit(o.Spec, n); err != nil {
			return err
		}
		steps, err := s.steps(t, false)
		if err != nil {
			return err
		}
		d, err := runSteps(steps)
		if err != nil {
			return fmt.Errorf("edit build %d: %w", n, err)
		}
		if err := check(n); err != nil {
			return err
		}
		if n <= 2 {
			continue
		}
		if n == 3 {
			edit.Builds = slices.Concat(steps...)
		}
		edit.Times = append(edit.Times, d.Seconds())
	}
	edit.Sum = summarize(edit.Times)
	return nil
}

func loadCompileIfAny(root, date string) (*compileResults, error) {
	b, err := os.ReadFile(filepath.Join(root, "tests", "bench", "results", date, "compile", "results.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r compileResults
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("compile results of %s: %w", date, err)
	}
	return &r, nil
}

// renderCompile is the compile-speed section of RESULTS.md.
func renderCompile(res *compileResults, date string) string {
	var w strings.Builder
	heading, where := "## Table \"compile\"", ""
	if res.Platform == "linux" {
		heading = "## Table \"compile\" on x86_64-linux (WSL 2)"
		where = " Built inside WSL 2 by an mk built for Linux, on the Linux file system, for x86_64-linux (ELF, ld.lld): Mk only, for the ELF edit budget; the other languages' builds are the Windows table's."
	}
	fmt.Fprintf(&w, "%s\n\nFrom `tests/bench/results/%s/%s/results.json`. Compile speed, never mixed with run time: one generated program of the same shape in every language, %d modules of %d structs and %d functions (each function builds a struct, loops, branches, calls its module's helper and the same function of its parent module; main calls all %d functions and prints a checksum that every build is checked against, so a stale cache fails instead of timing). Clean is every cache and object of the language deleted; edit is one function body's constant changed, a new value each build. Wall time from the first compiler start to the executable, %d jobs, %s. Ratios are to the fastest cell of the same profile and kind.%s\n\n",
		heading, date, compileDir(res.Platform), res.Spec.Modules, res.Spec.Structs, res.Spec.Funcs, res.Spec.Modules*res.Spec.Funcs, res.Jobs, res.Runs, where)
	hasKind := func(kind string) bool {
		return slices.ContainsFunc(res.Cells, func(c compileCell) bool { return c.Kind == kind })
	}
	warm := hasKind("warm")
	if warm {
		w.WriteString("No change is a rebuild with nothing changed, right after the edit builds, checked the same way: what each tool does when nothing needs doing (C's make links nothing when the executable is newer than every object).\n\n")
	}
	busy := "none"
	if len(res.Run.Busy) > 0 {
		busy = strings.Join(res.Run.Busy, ", ")
	}
	fmt.Fprintf(&w, "Machine: %s; %s; power scheme %s; affinity %s; measured %s. mk `%s` (%s, sha256 `%s`). Other compilers and test binaries running at the start: %s.\n\n",
		orDash(res.Run.CPU), orDash(res.Run.OS), orDash(res.Run.Power), orDash(res.Run.Affinity), orDash(res.Run.Date),
		orDash(binName(res.Run.MkBinary)), orDash(res.Run.Versions["mk"]), short(res.Run.MkSHA256), busy)
	w.WriteString("| Language | Version | Profiles and build |\n|---|---|---|\n")
	for _, l := range compileLangs {
		if res.Platform == "linux" && l.ID != "mk" {
			continue // the Linux run builds Mk only
		}
		v := res.Run.Versions[l.ID]
		if l.ID == "rust" && res.Run.Versions["cargo"] != "" {
			v = res.Run.Versions["cargo"] + "; " + v
		}
		pipe := func(s string) string { return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "|", `\|`) }
		fmt.Fprintf(&w, "| %s | %s | %s |\n", l.ID, pipe(orDash(v)), pipe(compileProfiles[l.ID]))
	}
	w.WriteString("\n")
	for _, profile := range []string{"dev", "release"} {
		best := map[string]float64{}
		for _, c := range res.Cells {
			if c.Profile == profile && c.Excluded == "" && c.Sum.N > 0 && !c.Nothing && c.Sum.Median > 0 {
				if b, ok := best[c.Kind]; !ok || c.Sum.Median < b {
					best[c.Kind] = c.Sum.Median
				}
			}
		}
		if warm {
			fmt.Fprintf(&w, "**%s profile.**\n\n| Language | Lines | Clean median (min, MAD) | Clean / best | Edit median (min, MAD) | Edit / best | No change median (min, MAD) | No change / best |\n|---|---|---|---|---|---|---|---|\n", profile)
		} else {
			fmt.Fprintf(&w, "**%s profile.**\n\n| Language | Lines | Clean median (min, MAD) | Clean / best | Edit median (min, MAD) | Edit / best |\n|---|---|---|---|---|---|\n", profile)
		}
		for _, l := range compileLangs {
			if !l.has(profile) {
				if l.ID == "go" && res.Platform == "" {
					if warm {
						w.WriteString("| go | | Go has one build profile: see the dev table | | | | | |\n")
					} else {
						w.WriteString("| go | | Go has one build profile: see the dev table | | | |\n")
					}
				}
				continue
			}
			var cl, ed, wa *compileCell
			for i := range res.Cells {
				c := &res.Cells[i]
				if c.Lang == l.ID && c.Profile == profile {
					switch c.Kind {
					case "clean":
						cl = c
					case "edit":
						ed = c
					case "warm":
						wa = c
					}
				}
			}
			if cl == nil || ed == nil {
				continue
			}
			cellOf := func(c *compileCell) (string, string) {
				if c.Excluded != "" {
					return "excluded: " + cellText(c.Excluded), ""
				}
				if c.Nothing {
					return "nothing to build (make runs no command)", ""
				}
				ratio := ""
				if b := best[c.Kind]; b > 0 {
					ratio = fmt.Sprintf("%.2f", c.Sum.Median/b)
				}
				return fmt.Sprintf("%s (%s, %s)", fmtSec(c.Sum.Median), fmtSec(c.Sum.Min), fmtSec(c.Sum.MAD)), ratio
			}
			a, ar := cellOf(cl)
			b, br := cellOf(ed)
			if !warm {
				fmt.Fprintf(&w, "| %s | %d | %s | %s | %s | %s |\n", l.ID, cl.Lines, a, ar, b, br)
				continue
			}
			c, cr := "not run", ""
			if wa != nil {
				c, cr = cellOf(wa)
			}
			fmt.Fprintf(&w, "| %s | %d | %s | %s | %s | %s | %s | %s |\n", l.ID, cl.Lines, a, ar, b, br, c, cr)
		}
		w.WriteString("\n")
	}
	if hasKind("check-body") || hasKind("check-sig") {
		w.WriteString("**Edit to diagnostic (Mk, `mk check -j 8` in the dev build's cache).** Every timed check follows a state with the opposite verdict, so a check that did not read the edit gives the wrong one and fails the cell. Body edit: the edited function's constant replaced by an unknown name, one diagnostic, after a valid body edit. Signature edit: a new exported function in m0, the root of the import tree, which every other module imports through its parents and main imports directly, so its interface changes and every module is an importer; the bodies stay as they were, and it follows the same function with a parameter of an unknown type. Mk only: no other language's command is in this table.\n\n| Row | Lines | Median (min, MAD) |\n|---|---|---|\n")
		for _, c := range res.Cells {
			name := map[string]string{"check-body": "body edit to diagnostic", "check-sig": "signature edit"}[c.Kind]
			if name == "" {
				continue
			}
			text := fmt.Sprintf("%s (%s, %s)", fmtSec(c.Sum.Median), fmtSec(c.Sum.Min), fmtSec(c.Sum.MAD))
			if c.Excluded != "" {
				text = "excluded: " + cellText(c.Excluded)
			}
			fmt.Fprintf(&w, "| %s | %d | %s |\n", name, c.Lines, text)
		}
		w.WriteString("\n")
	}
	if warm || hasKind("check-body") || hasKind("check-sig") {
		renderCompileBudgets(&w, res)
	}
	return w.String()
}
