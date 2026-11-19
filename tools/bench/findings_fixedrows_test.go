// Why: the SIMD rows, the C sources' pragmas, closuresort's two Mk forms and
// the vendored rows each offer a way around the benchmark contract
// (tests/bench/README.md) that a plain load does not catch.
// What: TestNoRunTimeChoiceInFixedRows to TestVendorOnLinuxClangLine;
// the gzip row's trailer check is in findings_simd_test.go.
// How: source scans of the tree's rows, plus release builds and probes that
// run only without -short.

package main

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// testMkExe is the mk a test builds Mk columns with: $MK_BENCH_MK when set
// (a prebuilt mk, such as a publish's frozen one), else ./cmd/mk of the
// tree built into the test's own binary directory <work>/bin/<tag>, which
// the test's cleanup removes.
func testMkExe(t *testing.T, tc *toolchain, root, tag string) (string, error) {
	t.Helper()
	if p := os.Getenv("MK_BENCH_MK"); p != "" {
		return p, nil
	}
	out := filepath.Join(tc.Work, "bin", tag, "mk.exe")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command(tc.Go, "build", "-o", out, "./cmd/mk")
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building ./cmd/mk: %v\n%s", err, firstLines(string(b), 10))
	}
	return out, nil
}

// fixedCells are the width-fixed Mk programs that replace the dispatching
// ones of the SIMD rows: the row, the package directory, and the
// vector width L it fixes (0: one text for every target, the width being
// LLVM's split of Vec[f32, 16]), with the CPU target of the variants that
// may name it ("" for any).
var fixedCells = []struct {
	row, dir string
	lanes    int
	cpu      string
}{
	{"indexbyte", "mkfix", 32, "x86-64-v3"},
	{"indexbyte", "mkfixv2", 16, "x86-64-v2"},
	{"validutf8", "mkfix", 32, "x86-64-v3"},
	{"validutf8", "mkfixv2", 16, "x86-64-v2"},
	{"dot", "mkfix", 0, ""},
}

// mkLaneConst is the line of a fixed cell that sets its width.
var mkLaneConst = regexp.MustCompile(`(?m)^const L: int = (\d+)$`)

// TestNoRunTimeChoiceInFixedRows: in a SIMD row without a dispatch field,
// an Mk source may not call a std.simd routine that picks its form at run
// time (IndexByte, IndexAny, Count, Equal, Compare, Dot, Sum, ValidUtf8,
// Dispatch) or Cpu() itself, the Mk counterpart of the ban on C's
// __attribute__((target)); the dispatch row may. No tree row has such a
// cell, and the width-fixed replacements are what they claim: no dispatch
// and no std.raw, L fixed per package, the two packages of a pair the same
// text but for L and the header, and named only by variants of their target.
// A cell that reaches such a choice through another std routine is labelled
// by its variant's dispatching (or exempted for a lane row), and the release
// builds prove it (bf1ReleaseBuilds, not run under -short).
func TestNoRunTimeChoiceInFixedRows(t *testing.T) {
	fixed := &row{Name: "f", Table: "simd"}
	disp := &row{Name: "d", Table: "simd", Dispatch: map[string]string{"mk": "std.simd"}}
	idio := &row{Name: "i", Table: "idiomatic"}
	cases := []struct {
		r    *row
		lang string
		src  string
		bad  bool
	}{
		{fixed, "mk", "import std.simd\nF(b: []u8): bool { return simd.ValidUtf8(b) }\n", true},
		{fixed, "mk", "import std.simd\nF(b: []u8): int {\n    i := simd.IndexByte(b, 0xFF) else { -1 }\n    return i\n}\n", true},
		{fixed, "mk", "import std.simd\nF(a: []f32, b: []f32): f32 { return simd.Dot[f32](a, b) }\n", true},
		{fixed, "mk", "import std.simd\nF(): bool { return simd.Cpu().Has(\"avx2\") }\n", true},
		{fixed, "mk-wrap", "import std.simd\nF(b: []u8): int { return simd.Count(b, 1) }\n", true},
		{fixed, "mk", "import std.simd\n// not simd.IndexByte(b, 0xFF), and no Cpu() test\nF(b: []u8): bool { return (simd.Vec[u8, 32].FromSlice(b, 0) == simd.Vec[u8, 32].Splat(1)).Any() }\n", false},
		{fixed, "mk", "import std.simd\nF(m: simd.Mask[u8, 16]): int { return m.CountSet() }\n", false},
		{disp, "mk", "import std.simd\nF(b: []u8): int {\n    i := simd.IndexByte(b, 0xFF) else { -1 }\n    return i\n}\n", false},
	}
	for _, c := range cases {
		vs := rowViolations(c.r, c.lang, c.src)
		if got := slices.Contains(vs, mkDispatchUse); got != c.bad {
			t.Errorf("row %s, %s %q: violations %q, want the run-time choice flagged: %v", c.r.Name, c.lang, c.src, vs, c.bad)
		}
	}
	// Outside the SIMD table the import is already the violation.
	if vs := rowViolations(idio, "mk", "import std.simd\nF(b: []u8): bool { return simd.ValidUtf8(b) }\n"); len(vs) == 0 {
		t.Error("an idiomatic Mk source with std.simd passed")
	}
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range treeRowsAll(t) {
		if r.Lane != "" || r.Table != "simd" || len(r.Dispatch) > 0 {
			continue
		}
		for lang := range r.Sources {
			if !isMkLang(lang) {
				continue
			}
			p, text := rowSourceText(r, lang)
			if slices.Contains(rowViolations(r, lang, text), mkDispatchUse) {
				t.Errorf("%s %s: %s chooses its vector form at run time in a fixed-target row", r.id(), lang, p)
			}
			for _, f := range fixedCells {
				if r.base() == f.row && r.Sources[lang] == f.dir && f.cpu != "" && r.cpu() != f.cpu {
					t.Errorf("%s: names %s/, the %s cell, at %s", r.id(), f.dir, f.cpu, r.cpu())
				}
			}
		}
	}
	norm := func(s string) string {
		s = mkLaneConst.ReplaceAllString(stripComments(s), "const L: int = N")
		return blankless(s)
	}
	texts := map[string]string{}
	for _, f := range fixedCells {
		dir := filepath.Join(root, "tests", "bench", "simd", f.row, f.dir)
		b, err := os.ReadFile(filepath.Join(dir, f.row+".mk"))
		if err != nil {
			t.Errorf("%s/%s: %v", f.row, f.dir, err)
			continue
		}
		pkg, err := os.ReadFile(filepath.Join(dir, "pkg.mk"))
		if err != nil {
			t.Errorf("%s/%s: %v", f.row, f.dir, err)
			continue
		}
		src := string(b)
		code := stripComments(src)
		if !strings.HasPrefix(src, "// "+f.row+",") {
			t.Errorf("%s/%s: no header comment \"// %s, ...\"", f.row, f.dir, f.row)
		}
		if mkDispatch.MatchString(code) {
			t.Errorf("%s/%s: a fixed cell chooses its form at run time", f.row, f.dir)
		}
		if strings.Contains(code, "std.raw") || strings.Contains(code, "raw.") {
			t.Errorf("%s/%s: uses std.raw (banned in every table)", f.row, f.dir)
		}
		if vs := otherTableViolations("simd", "mk", src+"\n"+string(pkg)); len(vs) > 0 {
			t.Errorf("%s/%s: %v", f.row, f.dir, vs)
		}
		m := mkLaneConst.FindStringSubmatch(code)
		switch {
		case f.lanes == 0 && m != nil:
			t.Errorf("%s/%s: one text for every target fixes L = %s", f.row, f.dir, m[1])
		case f.lanes > 0 && (m == nil || m[1] != strconv.Itoa(f.lanes)):
			t.Errorf("%s/%s: L is %v, want %d", f.row, f.dir, m, f.lanes)
		}
		if prev, ok := texts[f.row]; ok && prev != norm(src) {
			t.Errorf("%s: %s/ is not its pair's text with another L", f.row, f.dir)
		}
		texts[f.row] = norm(src)
	}
	// The label a variant gives a cell that reaches a run-time choice
	// through std (variant.Dispatching): only an Mk cell the variant has,
	// in a fixed-target SIMD row, and it reaches the notes.
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "m"), 0o755)
	os.WriteFile(filepath.Join(dir, "a.c"), []byte("x"), 0o644)
	label := func(table, extra, v string) (*row, error) {
		js := `{"name": "x", "table": "` + table + `", ` + extra + `"sources": {"mk": "m", "c": "a.c"}, "variants": [{"name": "v", "args": ["1"], "quick": ["1"]` + v + `}]}`
		os.WriteFile(filepath.Join(dir, "bench.json"), []byte(js), 0o644)
		return loadRow(filepath.Join(dir, "bench.json"), "")
	}
	for _, c := range []struct{ table, extra, v string }{
		{"simd", "", `, "dispatching": {"c": "x"}`},
		{"simd", "", `, "dispatching": {"mk": ""}`},
		{"simd", "", `, "dispatching": {"mk-wrap": "x"}`},
		{"simd", "", `, "skip": {"mk": "y"}, "dispatching": {"mk": "x"}`},
		{"idiomatic", "", `, "dispatching": {"mk": "x"}`},
		{"simd", `"dispatch": {"mk": "d", "c": "d"}, `, `, "dispatching": {"mk": "x"}`},
	} {
		if _, err := label(c.table, c.extra, c.v); err == nil || !strings.Contains(err.Error(), "dispatching") {
			t.Errorf("%s row %s variant %s loaded: %v", c.table, c.extra, c.v, err)
		}
	}
	if r, err := label("simd", "", `, "dispatching": {"mk": "how"}`); err != nil {
		t.Error(err)
	} else if x := expand(r)[0]; x.Dispatching["mk"] != "how" || !slices.Contains(x.Notes, dispatchingNote("mk", "how")) {
		t.Errorf("a labelled variant expands to %v, notes %q", x.Dispatching, x.Notes)
	}
	t.Run("ReleaseBuilds", bf1ReleaseBuilds)
}

// cpuidInsn is a cpuid instruction in llvm-objdump's disassembly.
var cpuidInsn = regexp.MustCompile(`(?m)^\s*[0-9a-f]+:\s+cpuid\s*$`)

// bf1ReleaseBuilds is TestNoRunTimeChoiceInFixedRows's build half: a fixed-target SIMD row's Mk
// cell may not reach std.simd's run-time choice through a std routine it
// calls unless its variant labels it (variant.Dispatching), or, for a folded
// lane row, the row is exempted. The text rule above sees only the cell's
// own calls; str.From reaches std.simd.ValidUtf8 because std.core's
// ValidUtf8 calls it.
// The witness is the release build with the row's own mk line: Cpu() is
// std's one source of a cpuid instruction (codegen's cold mk.cpu.detect,
// internal/codegen/intrinsic_simd_cpu.go; inline assembly is the only
// other, and reads the features just the same), so a cell whose
// executable holds one reads the CPU's features at run time. The mksimd/ program of
// validutf8, which calls std.simd.ValidUtf8 itself, must hold one, so a
// blind check cannot pass.
func bf1ReleaseBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the tree's mk and every fixed-target SIMD row's Mk cell")
	}
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	lr, err := loadLaneReview(root)
	if err != nil {
		t.Fatal(err)
	}
	tc, err := findToolchain(defaultWork(), "")
	if err != nil {
		t.Fatal(err)
	}
	if tc.Go == "" || tc.Clang == "" {
		t.Fatal("go or clang not found")
	}
	objdump := filepath.Join(filepath.Dir(tc.Clang), "llvm-objdump"+filepath.Ext(tc.Clang))
	if _, err := os.Stat(objdump); err != nil {
		t.Fatalf("llvm-objdump beside clang: %v", err)
	}
	cpuids := func(exe string) (int, error) {
		b, err := exec.Command(objdump, "-d", "--no-show-raw-insn", exe).Output()
		if err != nil {
			return 0, fmt.Errorf("llvm-objdump %s: %v", exe, err)
		}
		return len(cpuidInsn.FindAll(b, -1)), nil
	}
	type cell struct{ dir, lang, pkg, cpu string }
	groups := map[cell][]*row{}
	var order []cell
	var control *row
	for _, r := range treeRowsAll(t) {
		if r.Lane == "" && r.id() == "validutf8-l1v2" {
			c := *r
			c.Name, c.Sources = "validutf8-bf1control", map[string]string{"mk": "mksimd"}
			control = &c
		}
		if r.Broken != "" || r.Table != "simd" || len(r.Dispatch) > 0 {
			continue
		}
		for lang, pkg := range r.Sources {
			if !isMkLang(lang) {
				continue
			}
			k := cell{r.Dir, lang, pkg, r.cpu()}
			if _, ok := groups[k]; !ok {
				order = append(order, k)
			}
			groups[k] = append(groups[k], r)
		}
	}
	if control == nil {
		t.Fatal("no validutf8-l1v2 row for the control")
	}
	wantControl := cell{control.Dir, "mk", "mksimd", control.cpu()}
	order = append(order, wantControl)
	groups[wantControl] = []*row{control}
	tag := "test-bf1-" + strconv.Itoa(os.Getpid())
	t.Cleanup(func() { os.RemoveAll(filepath.Join(tc.Work, "bin", tag)) })
	if tc.Mk, err = testMkExe(t, tc, root, tag); err != nil {
		t.Fatal(err)
	}
	for _, k := range order {
		rs := groups[k]
		bs := buildAll(tc, tag, rs[:1], func(c column) bool { return c.Lang == k.lang && !c.Budget }, func(string) {})
		if len(bs) != 1 {
			t.Errorf("%s %s: built %d columns, want 1", rs[0].id(), k.lang, len(bs))
			continue
		}
		if bs[0].Err != nil {
			t.Errorf("%s %s (%s at %s): %v", rs[0].id(), k.lang, k.pkg, k.cpu, bs[0].Err)
			continue
		}
		n, err := cpuids(bs[0].Exe)
		if err != nil {
			t.Error(err)
			continue
		}
		t.Logf("%s %s (%s/ at %s): %d cpuid", rs[0].id(), k.lang, k.pkg, k.cpu, n)
		if k == wantControl {
			if n == 0 {
				t.Errorf("the control %s/%s, which calls std.simd.ValidUtf8, holds no cpuid: the check is blind", filepath.Base(k.dir), k.pkg)
			}
			continue
		}
		for _, r := range rs {
			if n == 0 || r.Dispatching[k.lang] != "" {
				continue
			}
			msg := fmt.Sprintf("%s %s: the release build of %s/ at %s reads the CPU's features (cpuid, std.simd's Cpu()), so it chooses its vector form at run time through std, and the variant's dispatching does not label it", r.id(), k.lang, k.pkg, k.cpu)
			if v := lr.verdictFor(r); r.Lane != "" && v != nil && slices.ContainsFunc(v.Exempt, func(e string) bool { return strings.Contains(msg, e) }) {
				continue
			}
			t.Error(msg)
		}
	}
}

// blankless drops the blank and trailing white space of s's lines, so two
// texts whose comments differ in length compare by their code.
func blankless(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, " \t\r"); strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// TestPragmaCommentLibOnly: the one pragma a C source may
// hold is #pragma comment(lib, "..."), which names an import library; a
// linker-option pragma such as comment(linker, "/STACK:...") tunes one
// column's link and fails in every table, as does any other pragma, and
// every pragma of a tree C source is the allowed one.
func TestPragmaCommentLibOnly(t *testing.T) {
	bad := []string{
		"#pragma comment(linker, \"/STACK:268435456\")\n",
		"#pragma comment(linker, \"/OPT:ICF\")\n",
		"#  pragma comment ( linker , \"/STACK:0x10000000\" )\n",
		"#pragma comment(user, \"x\")\n",
		"#pragma common\n",
		"#pragma comment(lib, \"ws2_32\")\n#pragma pack(1)\n",
		"#pragma comment(lib, \"ws2_32\") /* and */ x\n",
	}
	good := []string{
		"#pragma comment(lib, \"ws2_32\")\nint main(void) { return 0; }\n",
		"  #  pragma comment ( lib , \"synchronization.lib\" )\r\nint x;\r\n",
	}
	for _, src := range bad {
		for _, table := range []string{"same", "idiomatic", "simd"} {
			var vs []string
			if table == "same" {
				vs = contractViolations("c", src)
			} else {
				vs = otherTableViolations(table, "c", src)
			}
			if !slices.Contains(vs, "#pragma") {
				t.Errorf("%s: %q passed (%v)", table, src, vs)
			}
		}
	}
	for _, src := range good {
		if vs := contractViolations("c", src); len(vs) > 0 {
			t.Errorf("%q flagged %v", src, vs)
		}
	}
	for _, r := range treeRowsAll(t) {
		if r.Broken != "" {
			continue
		}
		if _, ok := r.Sources["c"]; !ok {
			continue
		}
		p, text := rowSourceText(r, "c")
		code := cPragmaLib.ReplaceAllString(stripComments(text), "")
		if regexp.MustCompile(`#\s*pragma`).MatchString(code) {
			t.Errorf("%s: %s has a pragma other than comment(lib, ...)", r.id(), p)
		}
	}
}

// crossShare is how many of the W-byte loads from a buffer at off mod 64,
// stepping by W, cross a 64-byte cache line, in the words the rows' notes
// use: "none", "half" or "all" (anything else is "some").
func crossShare(off, w int) string {
	per := 64 / w
	n := 0
	for k := range per {
		if (off+k*w)%64+w > 64 {
			n++
		}
	}
	switch {
	case n == 0:
		return "none"
	case 2*n == per:
		return "half"
	case n == per:
		return "all"
	}
	return "some"
}

// bf5Probe is an Mk program that allocates arena slices as the SIMD rows'
// Mk cells do and prints each one's address mod 64, through a module that
// reads it with std.raw (allowed here: the probe is no cell). Usage:
// bf5probe <kind> <n>: kind 4 is dot's two f32 slices of n, 2 validutf8's
// block of min(n, 65536) bytes then its buffer of n, 1 indexbyte's buffer.
const bf5Probe = `// bf5probe: the address mod 64 of the SIMD cells' arena slices (BF-5).
import ref

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    kind := Cast[int](try sys.args[1].ParseInt())
    n := Cast[int](try sys.args[2].ParseInt())
    var out := sys.Stdout()
    if kind == 4 {
        x := try sys.arena.Slice[f32](n)
        y := try sys.arena.Slice[f32](n)
        try Print(mut out, "{a} {b}\n", (a: ref.AddrF32(x[..]) % 64, b: ref.AddrF32(y[..]) % 64))
    } else if kind == 2 {
        var blen := n
        if blen > 65536 {
            blen = 65536
        }
        blk := try sys.arena.Slice[u8](blen)
        buf := try sys.arena.Slice[u8](n)
        try Print(mut out, "{a} {b}\n", (a: ref.AddrU8(blk[..]) % 64, b: ref.AddrU8(buf[..]) % 64))
    } else {
        buf := try sys.arena.Slice[u8](n)
        m := ref.AddrU8(buf[..]) % 64
        try Print(mut out, "{m}\n", (m))
    }
}
`

const bf5Ref = `// bf5probe ref: the data address of a slice.
import std.raw

export AddrU8(s: []u8): uint {
    unsafe "reads only the address" {
        return raw.Addr(raw.Data(s))
    }
}

export AddrF32(s: []f32): uint {
    unsafe "reads only the address" {
        return raw.Addr(raw.Data(s))
    }
}
`

const bf5Pkg = `const Package = pkg.Package(
    name: "bf5probe",
    bins: [pkg.Bin(name: "bf5probe", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["ref"],
    links: [],
)
`

// TestArenaOffsetsAsLabelled: a SIMD row whose notes give its Mk cell
// the labelled exception to aligned buffers states the arena offset it was
// measured at and what that offset costs, and both are true. The cost is
// arithmetic from the stated offset: the share of 32-byte (v3) and 16-byte
// (v2) loads, stepping from the slice's start, that cross a cache line,
// which the notes must state in crossShare's words. The offset is measured:
// the tree's mk builds a probe that allocates as each row's Mk cell does,
// at every variant's size, and every slice must sit at the stated offset.
// std.simd.Dot's avx2 form aligns its own loads, so a note that counts its
// loads as crossing lines is wrong; and a cell that aligns its own loads
// calls a dispatching routine, which TestNoRunTimeChoiceInFixedRows bans.
func TestArenaOffsetsAsLabelled(t *testing.T) {
	offRe := regexp.MustCompile(`at (\d+) mod 64`)
	kinds := map[string]string{"dot": "4", "validutf8": "2", "indexbyte": "1"}
	type probe struct{ kind, n string }
	want := map[probe]int{}
	for _, r := range treeRowsAll(t) {
		if r.Lane != "" || r.Table != "simd" || r.Bytes == 0 {
			continue
		}
		notes := strings.Join(r.Notes, " ")
		if !strings.Contains(notes, "labelled alignment exception") {
			continue
		}
		m := offRe.FindStringSubmatch(notes)
		if m == nil {
			t.Errorf("%s: labelled alignment exception without the measured offset (\"at <k> mod 64\")", r.id())
			continue
		}
		off, _ := strconv.Atoi(m[1])
		for _, w := range []int{16, 32} {
			share := crossShare(off, w)
			re := regexp.MustCompile(share + ` of (the|its|their) ` + strconv.Itoa(w) + `-byte loads`)
			if !re.MatchString(notes) {
				t.Errorf("%s: the notes put the Mk slices at %d mod 64, so %s of the %d-byte loads cross a cache line, and do not say so (want %q)", r.id(), off, share, w, re)
			}
		}
		kind := kinds[r.base()]
		if kind == "" || len(r.Args) == 0 {
			t.Errorf("%s: no probe for this row's allocations", r.id())
			continue
		}
		want[probe{kind, r.Args[0]}] = off
	}
	if len(want) == 0 {
		t.Skip("no SIMD row states labelled alignment exception")
	}
	if testing.Short() {
		t.Skip("builds the tree's mk and an arena probe")
	}
	tc, err := findToolchain(defaultWork(), "")
	if err != nil {
		t.Fatal(err)
	}
	if tc.Go == "" || tc.Clang == "" {
		t.Skip("go or clang not found")
	}
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	tag := "test-bf5-" + strconv.Itoa(os.Getpid())
	t.Cleanup(func() { os.RemoveAll(filepath.Join(tc.Work, "bin", tag)) })
	mk, err := testMkExe(t, tc, root, tag)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "bf5probe")
	for name, text := range map[string]string{"bf5probe.mk": bf5Probe, "pkg.mk": bf5Pkg, filepath.Join("ref", "ref.mk"): bf5Ref} {
		p := filepath.Join(pkgDir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(dir, "bf5probe.exe")
	cmd := exec.Command(mk, "build", "--release", "--pgo=off", "--cpu", "x86-64-v2", "-o", exe, pkgDir)
	cmd.Env = append(os.Environ(), "MK_CACHE="+filepath.Join(dir, "cache"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the probe: %v\n%s", err, b)
	}
	probes := slices.SortedFunc(maps.Keys(want), func(a, b probe) int { return strings.Compare(a.kind+" "+a.n, b.kind+" "+b.n) })
	for _, p := range probes {
		out, _, err := runOnceEnv(exe, []string{p.kind, p.n}, nil)
		if err != nil {
			t.Errorf("probe %s %s: %v", p.kind, p.n, err)
			continue
		}
		fields := strings.Fields(out)
		if p.kind == "2" && len(fields) == 2 {
			fields = fields[1:] // the cell's buffer is the second slice
		}
		for _, f := range fields {
			if got, err := strconv.Atoi(f); err != nil || got != want[p] {
				t.Errorf("probe %s %s: a slice at %q mod 64, the notes say %d (labelled alignment exception)", p.kind, p.n, f, want[p])
			}
		}
	}
}

// TestClosuresortSameSort: the two Mk forms of closuresort run the same
// sort, so their ratio is the cost of capturing literals and nothing else.
// mkcap's sortw is mkctx's with the context argument taken out ("ctx: &C, ",
// "&C, ", "ctx, ", ", C]" deleted) and no other change; mkcap's program is
// mk's with std's Sort call replaced by sortw.SortWith; and the row may
// publish an mk-ctx column only beside mkcap as its mk column.
func TestClosuresortSameSort(t *testing.T) {
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "tests", "bench", "idiomatic", "closuresort")
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	ctx := stripComments(read(filepath.Join("mkctx", "sortw", "sortw.mk")))
	for _, d := range []string{"ctx: &C, ", "&C, ", "ctx, "} {
		ctx = strings.ReplaceAll(ctx, d, "")
	}
	ctx = blankless(strings.ReplaceAll(ctx, ", C]", "]"))
	capt := blankless(stripComments(read(filepath.Join("mkcap", "sortw", "sortw.mk"))))
	if diff := firstDiff(ctx, capt); diff != "" {
		t.Errorf("mkcap/sortw/sortw.mk is not mkctx's sort with the context taken out: %s", diff)
	}
	if strings.Contains(capt, "ctx") || strings.Contains(capt, "raw.") {
		t.Error("mkcap's sort still names a context, or uses std.raw")
	}
	mk := blankless(strings.Replace(stripComments(read(filepath.Join("mk", "closuresort.mk"))), "xs.Sort(", "sortw.SortWith(mut xs[..], ", 1))
	mkcap := blankless(strings.Replace(stripComments(read(filepath.Join("mkcap", "closuresort.mk"))), "import sortw\n", "", 1))
	if diff := firstDiff(mk, mkcap); diff != "" {
		t.Errorf("mkcap/closuresort.mk is not mk's program with the sort call changed: %s", diff)
	}
	for _, r := range treeRowsAll(t) {
		if r.Lane == "" && r.base() == "closuresort" {
			if _, ok := r.Sources["mk-ctx"]; ok && r.Sources["mk"] != "mkcap" {
				t.Errorf("%s: mk-ctx is published beside mk = %q, which runs another sort; the like-for-like pair is mkcap against mkctx", r.id(), r.Sources["mk"])
			}
		}
	}
}

// firstDiff names the first line where a and b differ, "" when equal.
func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range max(len(al), len(bl)) {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return fmt.Sprintf("code line %d: %q against %q", i+1, x, y)
		}
	}
	return ""
}

// TestReadmeNamesTheExceptions: tests/bench/README.md states the
// contract every row is reviewed against, so its exceptions are the rows'
// exceptions. Every loaded SIMD row (tree or folded lane row) whose Mk
// vector cell reads unaligned buffers is named in the "Aligned buffers"
// paragraph, and every one whose Mk cell chooses its form at run time
// without being the dispatch row is named in the "Fixed targets, no
// dispatch" paragraph, as is every variant whose dispatching labels its Mk
// cell (the variant's own name); the "Aligned buffers" paragraph does not
// claim a single exception.
func TestReadmeNamesTheExceptions(t *testing.T) {
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "tests", "bench", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)
	para := func(marker string) string {
		i := strings.Index(readme, marker)
		if i < 0 {
			t.Fatalf("README.md has no paragraph %q", marker)
		}
		j := strings.Index(readme[i:], "\n")
		if j < 0 {
			j = len(readme) - i
		}
		return readme[i : i+j]
	}
	bs4, b16 := para("**Aligned buffers.**"), para("**Fixed targets, no dispatch.**")
	if strings.Contains(bs4, "is the one exception") {
		t.Error("the aligned-buffers paragraph still claims one exception")
	}
	named := 0
	for _, r := range treeRowsAll(t) {
		if r.Broken != "" || r.Table != "simd" {
			continue
		}
		name := r.base()
		if r.Lane != "" {
			name = r.Lane + "/" + r.base()
		}
		for lang := range r.Sources {
			if !isMkLang(lang) {
				continue
			}
			p, text := rowSourceText(r, lang)
			code := stripComments(text)
			if r.Bytes > 0 && strings.Contains(code, "simd") && !strings.Contains(text, "buffer not 64-byte aligned") {
				named++
				if !strings.Contains(bs4, "`"+name+"`") {
					t.Errorf("%s %s: %s is an Mk vector cell over unaligned arena buffers, and the aligned-buffers paragraph does not name `%s`", r.id(), lang, p, name)
				}
			}
			if len(r.Dispatch) == 0 && mkDispatch.MatchString(code) {
				named++
				if !strings.Contains(b16, "`"+name+"`") {
					t.Errorf("%s %s: %s chooses its form at run time, and the fixed-targets paragraph does not name `%s`", r.id(), lang, p, name)
				}
			}
			if r.Dispatching[lang] != "" {
				named++
				if !strings.Contains(b16, "`"+r.id()+"`") {
					t.Errorf("%s %s: its variant labels %s as choosing its form at run time through std, and the fixed-targets paragraph does not name `%s`", r.id(), lang, p, r.id())
				}
			}
		}
	}
	t.Logf("%d exception cells, each named", named)
}

// TestVendorOnLinuxClangLine: a vendored row's files, include
// directory and flags reach the C column's clang line on Linux as on
// Windows, with -lm still last on Linux.
func TestVendorOnLinuxClangLine(t *testing.T) {
	ev := t.TempDir()
	t.Setenv("MK_EVIDENCE", ev)
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(ev, "vendor", "x"), 0o755)
	os.WriteFile(filepath.Join(ev, "vendor", "x", "x.c"), []byte("int x;"), 0o644)
	os.WriteFile(filepath.Join(ev, "vendor", "x", "x.h"), []byte(""), 0o644)
	v := &vendorSpec{Dir: "vendor/x", Files: []string{"x.c", "x.h"}, CFlags: []string{"-DA=1"}}
	if err := resolveVendor(v, filepath.Join(root, "tests", "bench")); err != nil {
		t.Fatal(err)
	}
	r := &row{Name: "v", Dir: t.TempDir(), Sources: map[string]string{"c": "main.c"}, Vendor: v}
	for _, goos := range []string{"linux", "windows"} {
		tc := &toolchain{Clang: "clang", Work: t.TempDir(), GOOS: goos}
		cmd, err := tc.command(r, column{ID: "c", Lang: "c"}, "o.exe")
		if err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		for _, a := range []string{"-I" + v.Path, filepath.Join(v.Path, "x.c"), "-DA=1"} {
			if !slices.Contains(cmd.Args, a) {
				t.Errorf("%s: the C line %q lacks %q", goos, cmd.Args, a)
			}
		}
		if slices.Contains(cmd.Args, filepath.Join(v.Path, "x.h")) {
			t.Errorf("%s: a header is on the C line %q", goos, cmd.Args)
		}
		if last := cmd.Args[len(cmd.Args)-1]; (goos == "linux") != (last == "-lm") {
			t.Errorf("%s: the C line ends in %q", goos, last)
		}
	}
}
