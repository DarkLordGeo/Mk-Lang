// Why: `bench ir` must read the right functions and write nothing into the
// repository, since it runs on a loaded machine during development.
// What: tests of readLLDMap, irRow, trimPadding, sideBySide, the shim's IR
// steps, and irView on real rows.
// How: a cut-down lld map sample for the parser; irView builds binarytrees
// and std-mem2/fixed with the real toolchain and checks the files written.

package main

// Tests of `bench ir` (irview.go, README "Reading a row's code") and
// of the shim options it uses.

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// lldMapSample is the head of a real lld-link /lldmap (binarytrees.c,
// clang 21), cut down, with a second output section.
const lldMapSample = "Address  Size     Align Out     In      Symbol\r\n" +
	"00001000 00016d3d  4096 .text\r\n" +
	"00001000 0000021e    16         V:\\t\\binarytrees.o:(.text)\r\n" +
	"00001000 00000000     0                 main\r\n" +
	"00001000 00000000     0                 main\r\n" +
	"00001170 00000000     0                 build\r\n" +
	"000011a0 00000000     0                 $LN12\r\n" +
	"000011d0 00000000     0                 check\r\n" +
	"00001220 00000054    16         V:\\t\\x.o:(.text$mn)\r\n" +
	"00001220 00000000     0                 mk.main.Check\r\n" +
	"00018000 00001000  4096 .rdata\r\n" +
	"00018000 00000010     8         V:\\t\\x.o:(.rdata)\r\n" +
	"00018000 00000000     0                 mk.pos.mk.main.Check.0\r\n"

func TestReadLLDMap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.map")
	if err := os.WriteFile(p, []byte(lldMapSample), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readLLDMap(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []mapFunc{{"main", 0x1000, 0x1170}, {"build", 0x1170, 0x11d0}, {"check", 0x11d0, 0x121e}, {"mk.main.Check", 0x1220, 0x1274}}
	if !slices.Equal(got, want) {
		t.Errorf("functions %v, want %v (no .rdata symbol, no $LN label, a function ends at the next one or at its section's end)", got, want)
	}
	if ms := matchFuncs(got, "Check"); len(ms) != 1 || ms[0].Name != "mk.main.Check" {
		t.Errorf("Check matches %v, want the last-segment match only", ms)
	}
	if ms := matchFuncs(got, "CHE"); len(ms) != 2 {
		t.Errorf("a substring matches %v, want check and mk.main.Check", ms)
	}
	if ms := matchFuncs(got, "nothing"); ms != nil {
		t.Errorf("no match expected, got %v", ms)
	}
}

// TestIRViewWritesNoData: bench ir on a row that reads a generated data
// file (as jsonscan does) writes nothing under the repository root, since
// it runs no program. The mk here does not exist, so the build fails after
// the row is read; a build that wrote the data file before it read the row
// would leave it behind.
func TestIRViewWritesNoData(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module mk\n\ngo 1.27\n")
	writeFile(t, filepath.Join(root, "tests/bench/same/d/d.c"), "// d\n")
	writeFile(t, filepath.Join(root, "tests/bench/same/d/mk/d.mk"), "")
	writeFile(t, filepath.Join(root, "tests/bench/same/d/bench.json"),
		`{"name":"d","table":"same","overflow":"wraps","args":["{data}","1"],"quick":["{data}","1"],"sources":{"c":"d.c","mk":"mk"},
		"data":{"kind":"json","file":"d.json","bytes":4096,"seed":1}}`)
	list := func() []string {
		var files []string
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil {
				files = append(files, p)
			}
			return nil
		})
		return files
	}
	before := list()
	r, err := irRow(root, "d")
	if err != nil || r.Data == nil {
		t.Fatalf("irRow: %v, data %v", err, r)
	}
	if runtime.GOOS == "windows" {
		work := t.TempDir()
		if _, err := irView(irOptions{Root: root, Work: work, Mk: filepath.Join(work, "no-such-mk.exe"), Row: "d", Log: func(string) {}}); err == nil {
			t.Error("bench ir with a missing mk succeeded")
		}
	}
	if after := list(); !slices.Equal(after, before) {
		t.Errorf("bench ir wrote under the root:\n%s\nbefore:\n%s", strings.Join(after, "\n"), strings.Join(before, "\n"))
	}
	if _, err := irRow(root, "nothing"); err == nil {
		t.Error("irRow of an unknown row succeeded")
	}
}

// TestIRRowMkOnly: a row with an mk source and no c
// source, as most std rows are, is read on Mk's side alone; a row with no
// mk source is still refused.
func TestIRRowMkOnly(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module mk\n\ngo 1.27\n")
	writeFile(t, filepath.Join(root, "tests/bench/idiomatic/m/mk/m.mk"), "")
	writeFile(t, filepath.Join(root, "tests/bench/idiomatic/m/m.zig"), "")
	writeFile(t, filepath.Join(root, "tests/bench/idiomatic/m/bench.json"),
		`{"name":"m","table":"idiomatic","overflow":"wraps","args":["1"],"quick":["1"],"sources":{"mk":"mk","zig":"m.zig"}}`)
	writeFile(t, filepath.Join(root, "tests/bench/idiomatic/z/z.zig"), "")
	writeFile(t, filepath.Join(root, "tests/bench/idiomatic/z/bench.json"),
		`{"name":"z","table":"idiomatic","overflow":"wraps","args":["1"],"quick":["1"],"sources":{"zig":"z.zig"}}`)
	r, err := irRow(root, "m")
	if err != nil {
		t.Fatalf("irRow of an mk-only row: %v", err)
	}
	if _, ok := r.Sources["c"]; ok {
		t.Errorf("row m has a c source: %v", r.Sources)
	}
	if _, err := irRow(root, "z"); err == nil || !strings.Contains(err.Error(), "needs an mk source") {
		t.Errorf("irRow of a row without mk: %v, want the mk refusal", err)
	}
}

func TestTrimPadding(t *testing.T) {
	in := []string{"1400011bb: popq %rbx", "1400011c0: retq", "1400011c1: nopw %cs:(%rax,%rax)", "1400011cb: int3"}
	if got := trimPadding(in); !slices.Equal(got, in[:2]) {
		t.Errorf("trimPadding = %q", got)
	}
}

// TestShimIRSteps: the shim adds BackendArgs to ThinLTO backends only,
// IndexArgs to the index step only, FinalArgs to every other lld-link, and
// nothing to any other run; Objects still go to every link.
func TestShimIRSteps(t *testing.T) {
	cfg := shimConfig{RealDir: "R", Objects: []string{"k.obj"}, BackendArgs: []string{"-Rpass=x"}, IndexArgs: []string{"/mllvm:-print-import-failures"}, FinalArgs: []string{"/lldmap:m.map"}}
	backend := []string{"--target=x86_64-pc-windows-msvc", "-O3", "-x", "ir", "m0003.o", "-fthinlto-index=m0003.o.thinlto.bc", "-c", "-o", "m0003.o.native.o"}
	if step, mod := shimStep("clang", backend); step != "backend" || mod != "m0003.o" {
		t.Errorf("backend step %q module %q", step, mod)
	}
	if got := shimArgs(cfg, "clang", backend, ""); got[len(got)-1] != "-Rpass=x" {
		t.Errorf("backend: %q", got)
	}
	compile := []string{"--target=x86_64-pc-windows-msvc", "-x", "ir", "-c", "p0000.ll", "-o", "p0000.o", "-O3", "-flto=thin"}
	if got := shimArgs(cfg, "clang", compile, ""); len(got) != len(compile)+1 {
		t.Errorf("a pack compile got extra arguments: %q", got)
	}
	index := []string{"/out:index.exe", "m0000.o", "/thinlto-index-only", "/thinlto-emit-imports-files"}
	got := shimArgs(cfg, "lld-link", index, "")
	if !slices.Contains(got, "/mllvm:-print-import-failures") || slices.Contains(got, "/lldmap:m.map") || !slices.Contains(got, "k.obj") {
		t.Errorf("index step: %q", got)
	}
	got = shimArgs(cfg, "lld-link", []string{"/out:a.exe", "m0000.o.native.o"}, "")
	if !slices.Contains(got, "/lldmap:m.map") || slices.Contains(got, "/mllvm:-print-import-failures") {
		t.Errorf("final link: %q", got)
	}
	if got := shimArgs(cfg, "llvm-lib", []string{"/out:k.lib"}, ""); len(got) != 2 {
		t.Errorf("llvm-lib: %q", got)
	}
	ir := backendIRArgs(append([]string{"clang"}, backend...)[1:], "out.ll")
	if slices.Contains(ir, "-c") || slices.Contains(ir, "m0003.o.native.o") || !slices.Equal(ir[len(ir)-4:], []string{"-S", "-emit-llvm", "-o", "out.ll"}) {
		t.Errorf("backend IR line: %q", ir)
	}
}

func TestSideBySide(t *testing.T) {
	got := sideBySide([]string{"a", strings.Repeat("x", 20)}, []string{"b"}, 8)
	want := "a       | b\nxxxxxx~ | \n"
	if got != want {
		t.Errorf("sideBySide:\n%q\nwant\n%q", got, want)
	}
}

// TestIRViewRow: bench ir on binarytrees builds both columns, writes the
// pack IR, the post-LTO IR, both remark files, the import report and the
// disassembly of Build and build, and its mk executable is byte for byte
// the one the row's own mk line builds (the shim and --emit-ll change no
// output). It builds the tree's mk (or uses $MK_BENCH_MK) and times
// nothing.
func TestIRViewRow(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the tree's mk and the binarytrees row")
	}
	if runtime.GOOS != "windows" {
		t.Skip("bench ir is Windows only")
	}
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	tc, _ := findToolchain(work, "")
	if tc.Clang == "" || tc.Go == "" {
		t.Skip("clang or go not found")
	}
	mk, err := testMkExe(t, tc, root, "irtest")
	if err != nil {
		t.Fatal(err)
	}
	idx, err := irView(irOptions{Root: root, Work: work, Mk: mk, Row: "binarytrees", MkFuncs: []string{"Build"}, CFuncs: []string{"build"}, Log: func(string) {}})
	skipWithoutEvidence(t, root, err)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(work, "ir", "binarytrees")
	for _, f := range []string{"mk-ll", "mk-lto-ll/m0000.o.ll", "c-ll/binarytrees.ll", "mk-remarks.txt", "c-remarks.txt", "mk-imports.txt", "mk-disasm.txt", "c-disasm.txt", "side-by-side.txt", "index.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	bins, _ := os.ReadDir(filepath.Join(dir, "mk-ll"))
	if len(bins) != 1 {
		t.Fatalf("mk-ll holds %v, want one bin", bins)
	}
	if _, err := os.Stat(filepath.Join(dir, "mk-ll", bins[0].Name(), "support.ll")); err != nil {
		t.Error(err)
	}
	for _, w := range []string{"mk: mk.main.Build ", "c: build ", "-Rpass-missed=loop-vectorize"} {
		if !strings.Contains(idx, w) {
			t.Errorf("index lacks %q:\n%s", w, idx)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "mk-imports.txt")); !strings.Contains(string(b), "Reason =") {
		t.Error("the import report has no missed import")
	}
	// The row's own mk line (as verify and publish build it).
	r := &row{Name: "binarytrees", Dir: filepath.Join(root, "tests", "bench", "same", "binarytrees"), Sources: map[string]string{"mk": "mk"}}
	tc.Mk = mk
	col, _ := columnByID("mk")
	plain := filepath.Join(work, "plain.exe")
	cmd, err := tc.command(r, col, plain)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := runCmd(cmd, nil); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	a, _ := os.ReadFile(plain)
	b, _ := os.ReadFile(filepath.Join(dir, "mk.exe"))
	if len(a) == 0 || string(a) != string(b) {
		t.Errorf("bench ir's mk.exe (%d bytes) differs from the row's plain build (%d bytes)", len(b), len(a))
	}
}

// TestIRViewMkOnlyRow: bench ir on a row
// with no c source (std-mem2/fixed) reads Mk's side alone. Mk's files are
// written; no C file, directory or side-by-side.txt is, and index.txt
// names none of them.
func TestIRViewMkOnlyRow(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the tree's mk and the std-mem2/fixed row")
	}
	if runtime.GOOS != "windows" {
		t.Skip("bench ir is Windows only")
	}
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	tc, _ := findToolchain(work, "")
	if tc.Clang == "" || tc.Go == "" {
		t.Skip("clang or go not found")
	}
	mk, err := testMkExe(t, tc, root, "irtest")
	if err != nil {
		t.Fatal(err)
	}
	idx, err := irView(irOptions{Root: root, Work: work, Mk: mk, Row: "std-mem2/fixed", MkFuncs: []string{"Round"}, Log: func(string) {}})
	skipWithoutEvidence(t, root, err)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(work, "ir", "std-mem2_fixed")
	for _, f := range []string{"mk-ll", "mk-lto-ll/m0000.o.ll", "mk-remarks.txt", "mk-imports.txt", "mk-disasm.txt", "mk.exe", "mk.map", "index.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	for _, f := range []string{"c-ll", "c-remarks.txt", "c.exe", "c.map", "c-disasm.txt", "side-by-side.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s exists for a row with no c source", f)
		}
	}
	if !strings.Contains(idx, "mk: mk.main.Round ") {
		t.Errorf("index lacks Round's line:\n%s", idx)
	}
	for _, w := range []string{"c-ll", "c-remarks", "c.exe", "c.map", "c-disasm", "side-by-side", "beside the C twin"} {
		if strings.Contains(idx, w) {
			t.Errorf("index names %q:\n%s", w, idx)
		}
	}
}
