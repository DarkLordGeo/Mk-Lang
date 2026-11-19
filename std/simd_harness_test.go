// Why: the kernel and intrinsic tests need two things a user program cannot
// do: call std-only std.simd.kernel, and import std.raw outside unsafe_modules.
// What: the shared harness (s8Try, s8Run, s8Fixtures, s8CheckTargets,
// s8GroWantNeeds) and TestSimd_BenchRowsOneInstructionSet.
// How: a fixture shim in the std copy exports kernel wrappers through
// std.hash; a std.raw program builds as a package listed in unsafe_modules.

package std

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// What the copy of std gives a test program:
//
//   - std.simd.kernel is std-only (E0202), so a fixture file adds exported
//     wrappers of its kernels to std.hash, a std module (s8Shim);
//   - std.raw may be imported only by a module pkg.mk lists in
//     unsafe_modules, so an intrinsic program is built as a package whose
//     one module is listed (s8Try with unsafe set).
//
// The CPU-feature mask is the fixture forcing of std-simd_width_test.go
// (sdFixtures): a kernel's `Cpu().Has("...")` test is replaced by false, so
// the portable answer (false) runs on any machine.

// s8ShimPath is the fixture file that exports the kernel wrappers.
const s8ShimPath = "hash/simd8_shim.mk"

// s8PkgMk is a manifest whose one module may hold unsafe and import
// std.raw.
const s8PkgMk = `const Package = pkg.Package(
    name: "s8",
    bins: [pkg.Bin(name: "s8", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["."],
    links: [],
)
`

// s8Try builds src as main.mk over a copy of std with fixtures (and, when
// unsafe, beside s8PkgMk) with flags; it returns the executable, mk's
// output and its status.
func s8Try(t *testing.T, fixtures map[string]string, src string, unsafe bool, flags ...string) (string, string, int) {
	t.Helper()
	root := t.TempDir()
	stdDir := copyStd(t, root, fixtures)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	target := "main.mk"
	if unsafe {
		if err := os.WriteFile(filepath.Join(root, "pkg.mk"), []byte(s8PkgMk), 0o644); err != nil {
			t.Fatal(err)
		}
		target = "."
	}
	exe := filepath.Join(root, "s8")
	if runtime.GOOS == "windows" && !strings.Contains(strings.Join(flags, " "), "x86_64-linux") {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, target)
	out, status := mkCmd(t, root, stdDir, args...)
	return exe, out, status
}

// s8Run builds src with s8Try and runs it; a failed build or run fails the
// test, a missing toolchain skips it. It returns standard output with LF
// line ends.
func s8Run(t *testing.T, fixtures map[string]string, src string, unsafe bool, flags ...string) string {
	t.Helper()
	exe, out, status := s8Try(t, fixtures, src, unsafe, flags...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build %v: exit %d\n%s", flags, status, out)
	}
	r := runNative(t, exe, "")
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout:\n%.4000s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	return strings.ReplaceAll(r.stdout, "\r\n", "\n")
}

// s8Fixtures is the shim file plus each forcing applied to its std file.
func s8Fixtures(t *testing.T, shim string, forces []sdForce) map[string]string {
	t.Helper()
	fx := sdFixtures(t, forces)
	fx[s8ShimPath] = shim
	return fx
}

// s8CheckTargets runs mk check over a program whose Main runs body (which
// calls the shim's wrappers through std.hash) for each hosted target and
// aarch64-linux (checked only, not built), and over a freestanding MkEntry
// running the same body: each must be clean. A control copy of std with
// the kernels' freestanding test forced false must draw E1108 in
// std/simd/kernel, which shows the clean freestanding check is the test's
// doing (the vector forms are never instantiated there).
func s8CheckTargets(t *testing.T, shim, body string) {
	t.Helper()
	main := "import std.hash\n\nexport Main(sys: &System): WriteError!void {\n" + body + "    try sys.Print(\"done\", ())\n}\n"
	free := "import std.hash\n\nexport extern MkEntry() {\n" + body + "}\n"
	check := func(t *testing.T, fx map[string]string, src string, target string) (string, int) {
		root := t.TempDir()
		stdDir := copyStd(t, root, fx)
		if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return mkCmd(t, root, stdDir, "check", "--target", target, "main.mk")
	}
	fx := map[string]string{s8ShimPath: shim}
	for _, target := range []string{"x86_64-windows", "x86_64-linux", "aarch64-linux"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			if out, status := check(t, fx, main, target); status != 0 {
				t.Errorf("mk check --target %s: exit %d, want clean\n%s", target, status, out)
			}
		})
	}
	t.Run("x86_64-freestanding", func(t *testing.T) {
		t.Parallel()
		if out, status := check(t, fx, free, "x86_64-freestanding"); status != 0 {
			t.Errorf("mk check --target x86_64-freestanding: exit %d, want clean\n%s", status, out)
		}
	})
	t.Run("freestanding-control", func(t *testing.T) {
		t.Parallel()
		ctl := map[string]string{s8ShimPath: shim}
		ms, err := filepath.Glob(filepath.Join("simd", "kernel", "*.mk"))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ms {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			ctl[filepath.ToSlash(p)] = strings.ReplaceAll(string(b), "Target.os == Os.Freestanding", "false")
		}
		out, status := check(t, ctl, free, "x86_64-freestanding")
		if status == 0 || !strings.Contains(out, "E1108") || !strings.Contains(out, "std/simd/kernel/") {
			t.Errorf("control: exit %d, want E1108 in std/simd/kernel\n%s", status, out)
		}
	})
}

// s8WantCpuCall builds src (an unsafe package's main.mk, which calls a
// crypto intrinsic outside unsafe from a function lacking its feature) and
// wants E1101 with operation cpu-call and the missing feature.
func s8WantCpuCall(t *testing.T, src, feature string) {
	t.Helper()
	_, out, status := s8Try(t, nil, src, true)
	if status == 0 || !strings.Contains(out, "E1101") || !strings.Contains(out, "cpu-call") || !strings.Contains(out, feature) {
		t.Fatalf("mk build: exit %d, want E1101 cpu-call naming %s\n%s", status, feature, out)
	}
}

// s8Bytes is n bytes of the Mk programs' generator (their Fill) from seed:
// a 64-bit LCG (Knuth's MMIX constants), the top byte of each state.
func s8Bytes(seed uint64, n int) []byte {
	out := make([]byte, n)
	x := seed
	for i := range out {
		x = x*6364136223846793005 + 1442695040888963407
		out[i] = byte(x >> 56)
	}
	return out
}

// s8GroWantNeeds builds src (an unsafe package's main.mk with an
// MkEntry) for x86_64-freestanding, whose bins are general-regs-only, and
// wants exactly n E1106 diagnostics naming sse2 (internal/check
// raw_sse.go) and no internal error: such a call once crashed LLVM's backend
// (E0000, E0001) or put SSE code in the bin unreported.
func s8GroWantNeeds(t *testing.T, src string, n int) {
	t.Helper()
	_, out, status := s8Try(t, nil, src, true, "--target", "x86_64-freestanding")
	if strings.Contains(out, "no usable LLVM") {
		t.Skipf("needs the LLVM toolchain: %s", out)
	}
	got := strings.Count(out, "E1106 cpu-feature")
	if status == 0 || got != n || strings.Count(out, "needs: sse2") != n || strings.Contains(out, "E0000") || strings.Contains(out, "E0001") {
		t.Fatalf("mk build --target x86_64-freestanding: exit %d, %d E1106, want %d naming sse2 and no internal error\n%s", status, got, n, out)
	}
}

// s8GroXmm builds src for x86_64-freestanding with flags, which must
// succeed, and returns, per function of the ELF that uses an xmm, ymm or
// zmm register, its instruction mnemonics.
func s8GroXmm(t *testing.T, src string, flags ...string) map[string][]string {
	t.Helper()
	objdump := cryptoObjdump()
	if objdump == "" {
		t.Skip("needs llvm-objdump (LLVM 21)")
	}
	exe, out, status := s8Try(t, nil, src, true, append([]string{"--target", "x86_64-freestanding"}, flags...)...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build --target x86_64-freestanding %v: exit %d\n%s", flags, status, out)
	}
	dis, err := exec.Command(objdump, "-d", "--no-show-raw-insn", exe).Output()
	if err != nil {
		t.Fatalf("%s -d: %v", objdump, err)
	}
	ins := ctParse(string(dis))
	if len(ins) == 0 {
		t.Fatal("llvm-objdump printed no instruction")
	}
	fns := map[string][]string{}
	for _, x := range ins {
		if strings.Contains(x.args, "%xmm") || strings.Contains(x.args, "%ymm") || strings.Contains(x.args, "%zmm") {
			fns[x.fn] = append(fns[x.fn], x.op)
		}
	}
	return fns
}

// s8GroOnlyIn fails unless every function of fns is named by one of the
// suffixes allowed (functions whose cpu clause holds sse2, and the private
// helpers mk.intr.*), and unless op is among their instructions.
func s8GroOnlyIn(t *testing.T, fns map[string][]string, op string, allowed ...string) {
	t.Helper()
	seen := false
	for fn, ops := range fns {
		ok := strings.HasPrefix(fn, "mk.intr.")
		for _, a := range allowed {
			ok = ok || strings.HasSuffix(fn, a)
		}
		if !ok {
			t.Errorf("%s uses SSE registers in a general-regs-only bin: %v", fn, ops)
		}
		for _, o := range ops {
			seen = seen || o == op
		}
	}
	if !seen {
		t.Errorf("no %s among the SSE functions %v", op, fns)
	}
}

// TestSimd_BenchRowsOneInstructionSet: no row of
// tests/bench-lanes/simd8 sets a column that may choose the SHA, AES or
// PCLMUL instructions at run time (Mk's std through std.simd.kernel, Go's
// crypto packages) against one held to the row's x86-64-v2 target (Zig, C,
// Rust): the benchmarks use one CPU target, no per-language tuning unless
// every side has the same (sha256-4k once timed Go's SHA-NI, and Mk's once
// wired, against a scalar Zig). Such a row skips the
// fixed-target languages with a reason and says so in its notes.
func TestSimd_BenchRowsOneInstructionSet(t *testing.T) {
	rows, err := filepath.Glob(filepath.Join("..", "tests", "bench-lanes", "simd8", "*", "bench.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Skip("no tests/bench-lanes/simd8 beside this std (a snapshot run)")
	}
	dispatching := map[string]bool{"mk": true, "go": true}
	for _, p := range rows {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var r struct {
			Sources map[string]string `json:"sources"`
			Skip    map[string]string `json:"skip"`
			Notes   []string          `json:"notes"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		notes := strings.Join(r.Notes, " ")
		if !strings.Contains(notes, "run time") && !strings.Contains(notes, "run-time") {
			continue
		}
		for lang := range r.Sources {
			if !dispatching[lang] {
				t.Errorf("%s: %s is a column held to x86-64-v2 in a row whose columns choose their instructions at run time; skip it with the reason", p, lang)
			}
		}
		for _, lang := range []string{"c", "rust", "zig"} {
			if r.Skip[lang] == "" {
				t.Errorf("%s: no skip reason for %s", p, lang)
			}
		}
		if !strings.Contains(notes, "Run-time dispatch, labelled") {
			t.Errorf("%s: the notes do not label the row's run-time dispatch", p)
		}
	}
}
