// Why: Mk never contracts a * b + c (or c - a * b) into a fused
// multiply-add, so results are the same bits on every CPU and in the
// evaluator.
// What: TestNoContraction, in any cpu context, scalar or vector, at
// dev or release, and in the evaluator (twice-rounded bits).
// How: the inputs are TestFloatNoFusion's detector: a fused result is
// -2^-54 (f64) or -2^-26 (f32), the unfused one 0.

package codegen

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// simd8NoFmaSrc computes the detector in functions compiled for fma,
// x86-64-v3 and the baseline, as scalars and as Vec lanes, at run time
// (the inputs depend on an environment variable the optimiser cannot see)
// and in the evaluator (the const CE); every result's bits are folded into
// one word, which is 0 exactly when nothing fused.
const simd8NoFmaSrc = `import std.simd

const Eps64: f64 = 1.0 / 134217728.0
const Eps32: f32 = 1.0 / 8192.0

MulAdd64(a: f64, b: f64, c: f64): f64 cpu "fma" {
    return a * b + c
}

MulAdd32(a: f32, b: f32, c: f32): f32 cpu "x86-64-v3" {
    return a * b + c
}

MulSub64(a: f64, b: f64, c: f64): f64 cpu "fma" {
    return c - a * b
}

MulAddBase64(a: f64, b: f64, c: f64): f64 {
    return a * b + c
}

Lanes64(a: [4]f64, b: [4]f64, c: [4]f64): [4]f64 cpu "fma" {
    x := simd.Vec[f64, 4].FromArray(a) * simd.Vec[f64, 4].FromArray(b) + simd.Vec[f64, 4].FromArray(c)
    return x.ToArray()
}

Lanes32(a: [8]f32, b: [8]f32, c: [8]f32): [8]f32 cpu "x86-64-v3" {
    x := simd.Vec[f32, 8].FromArray(a) * simd.Vec[f32, 8].FromArray(b) + simd.Vec[f32, 8].FromArray(c)
    return x.ToArray()
}

Run(z: f64): u64 {
    a := 1.0 + Eps64 + z
    b := 1.0 - Eps64 + z
    a32 := 1.0 + Eps32 + Cast[f32](z)
    b32 := 1.0 - Eps32 + Cast[f32](z)
    var h: u64 = 0
    unsafe "Run is called only where Cpu() has x86-64-v3, which implies fma, or by the evaluator" {
        h = h | MulAdd64(a, b, -1.0).Bits()
        h = h | Cast[u64](MulAdd32(a32, b32, -1.0).Bits())
        h = h | MulSub64(a, b, 1.0).Bits()
        v := Lanes64([a; 4], [b; 4], [-1.0; 4])
        w := Lanes32([a32; 8], [b32; 8], [-1.0; 8])
        for i in 0..4 {
            h = h | v[i].Bits()
        }
        for i in 0..8 {
            h = h | Cast[u64](w[i].Bits())
        }
    }
    return h | MulAddBase64(a, b, -1.0).Bits()
}

const CE: u64 = Run(0.0)

export Main(sys: &System): WriteError!void {
    z := Cast[f64]((sys.Env("MK_SIMD8_ZERO") else { "" }).Len())
    if !simd.Cpu().Has("x86-64-v3") {
        try sys.Print("skip {c}\n", (c: CE))
        return
    }
    try sys.Print("{c} {r}\n", (c: CE, r: Run(z)))
}
`

// TestNoContraction: the detector gives 0 at compile time and at run
// time, dev and release, and the release binary holds no FMA instruction
// (the program calls no Fma, so any vfmadd would be a contraction).
func TestNoContraction(t *testing.T) {
	bin := needLLVM(t)
	if runtime.GOARCH != "amd64" {
		t.Skip("x86_64 cpu contexts; this host is " + runtime.GOARCH)
	}
	// The detector detects: a fused evaluation differs from the unfused.
	a, b := 1+math.Ldexp(1, -27), 1-math.Ldexp(1, -27)
	if math.FMA(a, b, -1) == 0 || float64(a*b)+(-1) != 0 {
		t.Fatalf("detector: fused %g, unfused %g", math.FMA(a, b, -1), float64(a*b)+(-1))
	}
	exe := simdMkExe(t)
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			pkg := strings.ReplaceAll(simd8PkgMk, "NAME", "nofma")
			for n, s := range map[string]string{"main.mk": simd8NoFmaSrc, "pkg.mk": pkg} {
				if err := os.WriteFile(filepath.Join(dir, n), []byte(s), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out := filepath.Join(dir, "nofma")
			if runtime.GOOS == "windows" {
				out += ".exe"
			}
			args := []string{"build", "-o", out, "."}
			if prof == "release" {
				args = []string{"build", "--release", "-o", out, "."}
			}
			cmd := exec.Command(exe, args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "MK_CACHE="+filepath.Join(dir, ".mk-cache"))
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("mk %v: %v\n%s", args, err, b)
			}
			got := strings.TrimSpace(runTool(t, out))
			if strings.HasPrefix(got, "skip") {
				if got != "skip 0" {
					t.Fatalf("evaluator: %q, want skip 0", got)
				}
				t.Skip("this CPU lacks x86-64-v3; the evaluator half passed")
			}
			if got != "0 0" {
				t.Fatalf("evaluator and run: %q, want \"0 0\" (a nonzero word is a fused result's bits)", got)
			}
			dis := runTool(t, tool(bin, "llvm-objdump"), "-d", "--no-show-raw-insn", out)
			vex := 0
			for _, ln := range strings.Split(dis, "\n") {
				f := strings.Fields(ln)
				if len(f) < 2 {
					continue
				}
				op := f[1]
				if strings.HasPrefix(op, "vfmadd") || strings.HasPrefix(op, "vfmsub") || strings.HasPrefix(op, "vfnmadd") || strings.HasPrefix(op, "vfnmsub") {
					t.Errorf("fused instruction in the %s build: %s", prof, strings.TrimSpace(ln))
				}
				if strings.HasPrefix(op, "vmul") {
					vex++
				}
			}
			if vex == 0 {
				t.Errorf("no VEX multiply in the %s build: the cpu functions were not compiled with their features", prof)
			}
		})
	}
}

// simd8PkgMk is a package manifest whose one module may hold unsafe;
// NAME is the bin's name.
const simd8PkgMk = `const Package = pkg.Package(
    name: "NAME",
    bins: [pkg.Bin(name: "NAME", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["."],
    links: [],
)
`
