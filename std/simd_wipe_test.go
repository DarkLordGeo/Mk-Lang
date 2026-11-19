// Why: a key wipe that the optimiser removes, or that the evaluator skips,
// leaves secrets in memory; std.simd.kernel's Wipe must do neither.
// What: TestSimd_WipeZeroesExactly, WipeKeptByOptimiser,
// WipeRefusedAtCompileTime and WipeCheckEveryTarget.
// How: every length and alignment against a Go model; zero stores counted in
// the release disassembly against a plain loop; the evaluator refuses (E0901).

package std

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const s8WipeShim = `import std.simd.kernel

export Simd8Wipe(buf: mut []u8) {
    kernel.Wipe(buf)
}
`

// s8WipeProgram wipes buf[o..o + n] of a 96-byte buffer of 0xA5 for every
// n from 0 to 80 and o from 0 to 15, and prints a hash of the buffers.
const s8WipeProgram = `import std.hash

Mix(h: u64, b: u8): u64 {
    return (h ^ Cast[u64](b)) *% 0x100000001b3
}

export Main(sys: &System): WriteError!void {
    var h: u64 = 0xcbf29ce484222325
    for n in 0..81 {
        for o in 0..16 {
            var buf: [96]u8 = [0xA5; 96]
            hash.Simd8Wipe(mut buf[o..o + n])
            for i in 0..96 {
                h = Mix(h, buf[i])
            }
        }
    }
    try sys.Print("{h}\n", (h: h))
}
`

func s8WipeModel() string {
	h := uint64(0xcbf29ce484222325)
	for n := 0; n <= 80; n++ {
		for o := 0; o < 16; o++ {
			for i := 0; i < 96; i++ {
				b := byte(0xA5)
				if i >= o && i < o+n {
					b = 0
				}
				h = (h ^ uint64(b)) * 0x100000001b3
			}
		}
	}
	return fmt.Sprintf("%d\n", h)
}

// TestSimd_WipeZeroesExactly: every length and offset, dev and release.
func TestSimd_WipeZeroesExactly(t *testing.T) {
	t.Parallel()
	fx := s8Fixtures(t, s8WipeShim, nil)
	want := s8WipeModel()
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			diffLines(t, s8Run(t, fx, s8WipeProgram, false, flags...), want)
		})
	}
}

// s8WipeKeepProgram holds a key in a local that dies right after it is
// wiped, by the kernel (WIPE is hash.Simd8Wipe(mut key[..])) or by a plain
// store loop (WIPE is that loop).
const s8WipeKeepProgram = `IMPORT

Secret(k: u8): u8 {
    var key: [64]u8 = [k; 64]
    x := key[5] ^ key[63]
    WIPE
    return x
}

export Main(sys: &System): WriteError!void {
    k := Cast[u8]((sys.Env("MK_SIMD8_K") else { "" }).Len())
    try sys.Print("{a}\n", (a: Secret(k)))
}
`

// s8MainZeroStores builds the keep program with wipe in place of WIPE, at
// release for x86_64-linux with --debug (the symbols kept; built, never
// run), and counts the stores of an immediate 0 to memory in the main
// module's functions.
func s8MainZeroStores(t *testing.T, objdump, wipe string) int {
	t.Helper()
	imp := ""
	if strings.Contains(wipe, "hash.") {
		imp = "import std.hash"
	}
	src := strings.Replace(strings.Replace(s8WipeKeepProgram, "WIPE", wipe, 1), "IMPORT", imp, 1)
	exe, out, status := s8Try(t, s8Fixtures(t, s8WipeShim, nil), src, false, "--release", "--debug", "-target", "x86_64-linux")
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	dis, err := exec.Command(objdump, "-d", "--no-show-raw-insn", exe).Output()
	if err != nil {
		t.Fatalf("%s -d: %v", objdump, err)
	}
	mov := regexp.MustCompile(`^mov[bwlq]$`)
	n, wipeCalls := 0, 0
	for _, x := range ctParse(string(dis)) {
		if !strings.HasPrefix(x.fn, "mk.main.") {
			continue
		}
		if mov.MatchString(x.op) && strings.HasPrefix(x.args, "$0x0,") && strings.Contains(x.args, "(") {
			n++
		}
		if x.op == "callq" && strings.Contains(x.args, "Wipe") {
			wipeCalls++
		}
	}
	if wipeCalls > 0 {
		t.Fatalf("the main module calls Wipe out of line (%d calls): count its stores instead", wipeCalls)
	}
	return n
}

// TestSimd_WipeKeptByOptimiser: with the kernel's wipe the main module
// holds at least the eight 8-byte zero stores of the 64-byte key more than
// with a plain loop, which the optimiser removes (the control: the loop's
// key dies, so its stores are dead).
func TestSimd_WipeKeptByOptimiser(t *testing.T) {
	t.Parallel()
	objdump := cryptoObjdump()
	if objdump == "" {
		t.Skip("needs llvm-objdump (LLVM 21)")
	}
	kernel := s8MainZeroStores(t, objdump, "hash.Simd8Wipe(mut key[..])")
	loop := s8MainZeroStores(t, objdump, "for i in 0..64 {\n        key[i] = 0\n    }")
	if kernel < loop+8 {
		t.Errorf("zero stores in the main module: %d with the kernel's wipe, %d with a plain loop; want at least 8 more", kernel, loop)
	}
	t.Logf("zero stores: kernel %d, plain loop %d", kernel, loop)
}

// TestSimd_WipeRefusedAtCompileTime: a const that reaches Wipe is E0901
// with a refusal (pointer-bytes, from the alignment test), never a skipped wipe.
func TestSimd_WipeRefusedAtCompileTime(t *testing.T) {
	t.Parallel()
	src := `import std.hash

Zeroed(): u8 {
    var b: [4]u8 = [1; 4]
    hash.Simd8Wipe(mut b[..])
    return b[0]
}

const Z: u8 = Zeroed()

export Main(sys: &System): WriteError!void {
    try sys.Print("{z}\n", (z: Z))
}
`
	_, out, status := s8Try(t, s8Fixtures(t, s8WipeShim, nil), src, false)
	if status == 0 || !strings.Contains(out, "E0901") || !strings.Contains(out, "refused:") {
		t.Fatalf("mk build: exit %d, want E0901 with a refusal\n%s", status, out)
	}
}

// TestSimd_WipeCheckEveryTarget: Wipe checks clean on every target,
// x86_64-freestanding included (it holds no vector), and the control
// (the other kernels' freestanding tests forced false) is E1108 only
// through them; with Wipe alone in the shim no kernel file is reached but
// Wipe's, so the control asks for nothing here.
func TestSimd_WipeCheckEveryTarget(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"x86_64-windows", "x86_64-linux", "aarch64-linux", "x86_64-freestanding"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			src := "import std.hash\n\nexport Main(sys: &System): WriteError!void {\n    var b: [64]u8 = [0; 64]\n    hash.Simd8Wipe(mut b[..])\n    try sys.Print(\"done\", ())\n}\n"
			if target == "x86_64-freestanding" {
				src = "import std.hash\n\nexport extern MkEntry() {\n    var b: [64]u8 = [0; 64]\n    hash.Simd8Wipe(mut b[..])\n}\n"
			}
			root := t.TempDir()
			stdDir := copyStd(t, root, map[string]string{s8ShimPath: s8WipeShim})
			if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, status := mkCmd(t, root, stdDir, "check", "--target", target, "main.mk"); status != 0 {
				t.Errorf("mk check --target %s: exit %d, want clean\n%s", target, status, out)
			}
		})
	}
}
