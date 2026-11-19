// Why: the codegen table links a C object into an Mk program through a shim
// and trusts a per-round protocol; both must be right before a number counts.
// What: tests of the shim (TestShimTool to TestLinkedObject), the round
// parser, the command lines, the render, and TestTreeKernelsMeetTheContract.
// How: pure functions on fixed inputs, plus a load of the real kernels that
// checks what can be checked without building.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestShimTool(t *testing.T) {
	for argv0, want := range map[string]string{
		`C:\x\llvm\clang.exe`:    "clang",
		`C:\x\llvm\LLD-LINK.EXE`: "lld-link",
		"/x/llvm/ld.lld":         "ld.lld",
		`C:\x\llvm-lib.exe`:      "llvm-lib",
		`C:\x\benchtool.exe`:     "",
		"bench.test":             "",
		`C:\x\clang-21.exe`:      "",
	} {
		if got := shimTool(argv0); got != want {
			t.Errorf("shimTool(%q) = %q, want %q", argv0, got, want)
		}
	}
}

func TestShimArgsAppendObjectsToLinkersOnly(t *testing.T) {
	cfg := shimConfig{RealDir: "R", Objects: []string{"k.obj"}}
	got := shimArgs(cfg, "lld-link", []string{"/out:a.exe", "a.o"}, ".exe")
	want := []string{filepath.Join("R", "lld-link.exe"), "/out:a.exe", "a.o", "k.obj"}
	if !slices.Equal(got, want) {
		t.Errorf("lld-link: %q, want %q", got, want)
	}
	if got := shimArgs(cfg, "ld.lld", []string{"-o", "a"}, ""); got[len(got)-1] != "k.obj" {
		t.Errorf("ld.lld: %q does not end with the object", got)
	}
	for _, tool := range []string{"clang", "llvm-lib"} {
		if got := shimArgs(cfg, tool, []string{"x"}, ".exe"); slices.Contains(got, "k.obj") {
			t.Errorf("%s got the object: %q", tool, got)
		}
	}
}

func TestWriteShimDir(t *testing.T) {
	dir := t.TempDir()
	cfg := shimConfig{RealDir: `C:\Program Files\LLVM\bin`, Objects: []string{`V:\w\k.obj`}, Log: `V:\w\shim.log`}
	if err := writeShimDir(dir, cfg); err != nil {
		t.Fatal(err)
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	for _, tool := range shimTools {
		if _, err := os.Stat(filepath.Join(dir, tool+exe)); err != nil {
			t.Errorf("no %s copy: %v", tool, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, shimConfigName))
	if err != nil {
		t.Fatal(err)
	}
	var back shimConfig
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.RealDir != cfg.RealDir || !slices.Equal(back.Objects, cfg.Objects) || back.Log != cfg.Log {
		t.Errorf("config round trip: %+v, want %+v", back, cfg)
	}
}

func TestLinkedObject(t *testing.T) {
	obj := `V:\w\codegen\k\k-c.obj`
	log := `C:\LLVM\bin\clang.exe -x ir -c a.ll -o a.o
C:\LLVM\bin\llvm-lib.exe /def:x.def /out:kernel32.lib
C:\LLVM\bin\lld-link.exe /nologo /out:k.exe a.o kernel32.lib ` + obj + "\n"
	if !linkedObject(log, obj) {
		t.Error("the link line names the object, but linkedObject says no")
	}
	if linkedObject(strings.ReplaceAll(log, obj, ""), obj) {
		t.Error("no link line names the object, but linkedObject says yes")
	}
	if linkedObject(`C:\LLVM\bin\clang.exe -c `+obj, obj) {
		t.Error("a clang line was taken for the link")
	}
}

func TestParseAndCheckRounds(t *testing.T) {
	rs, err := parseRounds("round 0 mk 100 42 c 90 42\nround 1 mk 110 42 c 95 42\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[1].MkNs != 110 || rs[1].CNs != 95 || rs[0].CSum != "42" {
		t.Fatalf("parsed %+v", rs)
	}
	if err := checkRounds(rs, "42"); err != nil {
		t.Error(err)
	}
	if err := checkRounds(rs, "43"); err == nil {
		t.Error("a wrong expected checksum passed")
	}
	rs[1].CSum = "41"
	if err := checkRounds(rs, "42"); err == nil {
		t.Error("sides that disagree passed")
	}
	for _, bad := range []string{"", "round 0 mk 1 2 c 3", "n out of range", "round 0 mk x 2 c 3 2"} {
		if _, err := parseRounds(bad); err == nil {
			t.Errorf("parseRounds(%q) passed", bad)
		}
	}
}

func TestCodegenCommandsFollowTheContract(t *testing.T) {
	cc := strings.Join(codegenCCommand("clang", "k.c", "k.obj"), " ")
	for _, w := range []string{"--target=" + cTriple, "-O3", "-march=x86-64-v2", "-ffp-contract=off", "-ffreestanding", "-fno-math-errno", "-c"} {
		if !strings.Contains(cc, w) {
			t.Errorf("C command %q lacks %q", cc, w)
		}
	}
	for _, banned := range []string{"-flto", "-ffast-math", "-march=native"} {
		if strings.Contains(cc, banned) {
			t.Errorf("C command %q has %q", cc, banned)
		}
	}
	mc := strings.Join(codegenMkCommand("mk", "S", "k.exe", "pkg"), " ")
	for _, w := range []string{"build --release --pgo=off", "--cpu x86-64-v2", "--no-cache", "--llvm S"} {
		if !strings.Contains(mc, w) {
			t.Errorf("Mk command %q lacks %q", mc, w)
		}
	}
}

func TestRenderAndMergeCodegen(t *testing.T) {
	mk := []float64{0.02, 0.021, 0.019}
	c := []float64{0.01, 0.0101, 0.0099}
	cell := codegenCell{Kernel: "k", Measures: "the check", Args: []string{"9"}, Mk: mk, C: c,
		MkSum: summarize(mk), CSum: summarize(c), Ratio: ratioEntry{Ratio: 2, CI: ratioCI(mk, c)}}
	res := &codegenResults{Run: facts{CPU: "cpu", Versions: map[string]string{"c": "clang version 21.1.8"}}, Procs: 3, Rounds: "3 processes",
		Cells: mergeCodegen([]codegenCell{{Kernel: "old", Excluded: "build failed"}, {Kernel: "k"}}, []codegenCell{cell})}
	if len(res.Cells) != 2 || res.Cells[0].Kernel != "k" || res.Cells[0].Mk == nil {
		t.Fatalf("merge: %+v", res.Cells)
	}
	md := renderCodegen(res, "2026-11-07")
	for _, w := range []string{"## Table \"codegen\"", "(clang version 21.1.8)", "| k | 9 | 20.0 ms", "| 2.00 [", "| over |", "excluded: build failed", "never feeds a cross-language ratio"} {
		if !strings.Contains(md, w) {
			t.Errorf("codegen section lacks %q:\n%s", w, md)
		}
	}
}

// TestTreeKernelsMeetTheContract loads the real codegen kernels and checks
// what can be checked without building (README "The codegen table").
func TestTreeKernelsMeetTheContract(t *testing.T) {
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	ks, err := loadKernels(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, k := range ks {
		names = append(names, k.Name)
		if k.Checksum == "" || k.QuickSum == "" || k.Measures == "" || k.Algorithm == "" {
			t.Errorf("%s: checksums, algorithm or measures not recorded", k.Name)
		}
		cb, err := os.ReadFile(filepath.Join(k.Dir, k.C))
		if err != nil {
			t.Fatal(err)
		}
		src := string(cb)
		if !strings.HasPrefix(src, "// Codegen kernel "+k.Name+", C side") {
			t.Errorf("%s: the C file does not open with its header", k.Name)
		}
		for _, l := range strings.Split(src, "\n") {
			if strings.HasPrefix(l, "#include") && l != "#include <stdint.h>" {
				t.Errorf("%s: %q: the C side is freestanding (stdint.h only)", k.Name, l)
			}
		}
		for _, banned := range []string{"immintrin", "__restrict", "restrict ", "#pragma", "__builtin_assume", "__builtin_expect"} {
			if strings.Contains(src, banned) {
				t.Errorf("%s: the C side uses %q", k.Name, banned)
			}
		}
		mb, err := os.ReadFile(filepath.Join(k.Dir, k.Name+".mk"))
		if err != nil {
			t.Fatal(err)
		}
		ms := string(mb)
		for _, w := range []string{"(side + round) % 2 == 0", `"round {round} mk {mk_ns} {mk_sum} c {c_ns} {c_sum}\n"`, "time.Now(sys.io)"} {
			if !strings.Contains(ms, w) {
				t.Errorf("%s: the Mk harness lacks %q", k.Name, w)
			}
		}
	}
	for _, want := range []string{"fmtints", "intsum", "matmul", "nbody", "sieve"} {
		if !slices.Contains(names, want) {
			t.Errorf("no codegen kernel %s", want)
		}
	}
}
