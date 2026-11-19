// Why: hash.Sha256 and Sha256State send whole blocks to the SHA-NI kernel
// when simd.Cpu() has sha, else to the portable loop; both must agree.
// What: tests that both paths give crypto/sha256's digests, at run time and
// compile time, and that the kernel's guard is inlined once.
// How: std-simd's fixture forcing (s8ShaOff, s8ShaOn) picks the path; a
// std copy with the portable loop broken shows which path ran.

package std

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

// h2BreakPortable breaks std.hash's portable SHA-256 loop (the last
// chaining word gains 1 per block), so a digest is right only when every
// block went through the kernel.
var h2BreakPortable = []sdForce{{"hash/sha256.mk", "h[7] = h[7] +% hh\n", "h[7] = h[7] +% hh +% 1\n", 1}}

// h2KernelLens are the one-shot and stream lengths: every length to 300,
// then block edges and longer messages.
var h2KernelLens = append(s8Range(0, 300), 447, 448, 511, 512, 513, 1000, 4096, 4097, 65543)

// h2KernelEval are the lengths of h2ConstText hashed at compile time.
var h2KernelEval = []int{0, 55, 56, 64, 119, 200, len(h2ConstText)}

// h2KernelProgram prints whether simd.Cpu() has sha, then per length of
// h2KernelEval the compile-time Sha256 of h2ConstText's prefix ("E"), then
// per length of h2KernelLens the one-shot Sha256 ("L") and a Sha256State
// fed in seeded random pieces ("S") of splitmix64 bytes. With stop, a CPU
// without sha stops after the compile-time lines (a forced kernel would
// fault there).
func h2KernelProgram(stop bool) string {
	var b strings.Builder
	b.WriteString(strings.Replace(h2Prelude, "import std.hash\n", "import std.hash\nimport std.simd\n", 1))
	fmt.Fprintf(&b, "\nconst Text = %q\n", h2ConstText)
	for i, n := range h2KernelEval {
		fmt.Fprintf(&b, "const E%d: [32]u8 = hash.Sha256(Text.AsBytes()[0..%d])\n", i, n)
	}
	fmt.Fprintf(&b, "const Lens: [%d]int = %s\n", len(h2KernelLens), mkIntArray(h2KernelLens))
	fmt.Fprintf(&b, "const Pieces: [%d]int = %s\n", len(h2Pieces), mkIntArray(h2Pieces))
	b.WriteString(`
export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    sha := simd.Cpu().Has("sha")
    try Print(mut out, "cpu {sha}\n", (sha: sha))
`)
	for i := range h2KernelEval {
		fmt.Fprintf(&b, "    try Print(mut out, \"E \")\n    try Hex(mut out, E%d[..])\n    try Print(mut out, \"\\n\")\n", i)
	}
	if stop {
		b.WriteString("    if !sha {\n        return\n    }\n")
	}
	b.WriteString(`    var r := Rng(s: 1)
    buf := try sys.arena.Slice[u8](65543 + 8)
    Fill(mut r, buf)
    var sr := Rng(s: 7)
    for k in 0..Lens.Len() {
        n := Lens[k]
        off := n % 8
        m := buf[off..off + n]
        try Print(mut out, "L ")
        try Hex(mut out, hash.Sha256(m)[..])
        try Print(mut out, "\n")
        var s := hash.Sha256State.New()
        var i := 0
        for i < n {
            var p := Pieces[Trunc[int](sr.Next() % 16)]
            if p > n - i {
                p = n - i
            }
            s.Bytes(m[i..i + p])
            i += p
        }
        try Print(mut out, "S ")
        try Hex(mut out, s.Sum()[..])
        try Print(mut out, "\n")
    }
}
`)
	return b.String()
}

// h2KernelWant is the oracle's output for h2KernelProgram after its cpu
// line.
func h2KernelWant() []string {
	var lines []string
	for _, n := range h2KernelEval {
		lines = append(lines, fmt.Sprintf("E %x", sha256.Sum256([]byte(h2ConstText[:n]))))
	}
	r := hmSplitmix{1}
	buf := make([]byte, 65543+8)
	r.fill(buf)
	sr := hmSplitmix{7}
	for _, n := range h2KernelLens {
		off := n % 8
		d := sha256.Sum256(buf[off : off+n])
		for i := 0; i < n; {
			i += min(h2Pieces[sr.next()%16], n-i)
		}
		lines = append(lines, fmt.Sprintf("L %x", d), fmt.Sprintf("S %x", d))
	}
	return lines
}

// h2KernelRun builds and runs h2KernelProgram(false) over std with
// forces, and returns whether the CPU has sha and the lines after the cpu
// line.
func h2KernelRun(t *testing.T, forces []sdForce, flags ...string) (bool, []string) {
	t.Helper()
	return h2KernelRunProgram(t, h2KernelProgram(false), forces, flags...)
}

// h2KernelRunProgram is h2KernelRun of src.
func h2KernelRunProgram(t *testing.T, src string, forces []sdForce, flags ...string) (bool, []string) {
	t.Helper()
	got := h2Run(t, sdFixtures(t, forces), src, flags...)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "cpu ") {
		t.Fatalf("no cpu line:\n%.2000s", got)
	}
	return lines[0] == "cpu true", lines[1:]
}

// h2Mismatches counts the lines of got that differ from want, by kind
// (E, L, S), and fails if the line counts differ.
func h2Mismatches(t *testing.T, got, want []string) map[string]int {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%d lines, want %d", len(got), len(want))
	}
	bad := map[string]int{}
	for i := range got {
		if got[i] != want[i] {
			bad[want[i][:1]]++
		}
	}
	return bad
}

// h2Total counts the lines of want by kind.
func h2Total(want []string) map[string]int {
	n := map[string]int{}
	for _, w := range want {
		n[w[:1]]++
	}
	return n
}

// TestSTDHASH_Sha256PathsMatchGo: hash.Sha256 and Sha256State, as built
// (the kernel where the CPU has sha) and with the CPU test masked (the
// portable loop on any machine), at dev and release, equal crypto/sha256;
// the compile-time digests (the evaluator, where Cpu() is the baseline, so
// the portable loop) too.
func TestSTDHASH_Sha256PathsMatchGo(t *testing.T) {
	t.Parallel()
	want := h2KernelWant()
	for _, c := range []struct {
		name   string
		forces []sdForce
		flags  []string
	}{
		{"dispatch-dev", nil, nil},
		{"dispatch-release", nil, []string{"--release"}},
		{"masked-dev", s8ShaOff, nil},
		{"masked-release", s8ShaOff, []string{"--release"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			sha, got := h2KernelRun(t, c.forces, c.flags...)
			t.Logf("simd.Cpu().Has(\"sha\") = %v", sha)
			diffLines(t, strings.Join(got, "\n"), strings.Join(want, "\n"))
		})
	}
}

// TestSTDHASH_Sha256KernelRunsEveryBlock: with the portable loop broken,
// a build on a CPU with sha still gives crypto/sha256's digests at run
// time (every block of Bytes and Sum went through the kernel) and wrong
// ones at compile time (the evaluator's Cpu() is the baseline); masked,
// every digest is wrong (the portable loop ran). So the dispatch takes
// the kernel exactly where it should and the mask reaches it.
func TestSTDHASH_Sha256KernelRunsEveryBlock(t *testing.T) {
	t.Parallel()
	want := h2KernelWant()
	total := h2Total(want)
	for _, flags := range [][]string{nil, {"--release"}} {
		name := "dev"
		if flags != nil {
			name = "release"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, masked := h2KernelRun(t, append(append([]sdForce{}, s8ShaOff...), h2BreakPortable...), flags...)
			if bad := h2Mismatches(t, masked, want); bad["E"] != total["E"] || bad["L"] != total["L"] || bad["S"] != total["S"] {
				t.Fatalf("masked with the portable loop broken: %v wrong of %v; want all (the portable loop ran nowhere?)", bad, total)
			}
			sha, got := h2KernelRun(t, h2BreakPortable, flags...)
			bad := h2Mismatches(t, got, want)
			if bad["E"] != total["E"] {
				t.Fatalf("compile time with the portable loop broken: %d of %d wrong; want all (the evaluator's Cpu() is the baseline)", bad["E"], total["E"])
			}
			if !sha {
				t.Skip("this CPU lacks sha: the kernel cannot run; the masked and compile-time halves passed")
			}
			if bad["L"] != 0 || bad["S"] != 0 {
				t.Fatalf("CPU has sha, portable loop broken: %d one-shot and %d stream digests wrong; want none (a block missed the kernel)", bad["L"], bad["S"])
			}
		})
	}
}

// TestSTDHASH_Sha256EvaluatorKernel: with the CPU test forced true, the
// evaluator runs hash.Sha256 through the kernel's intrinsics (their
// evaluator semantics), and the compile-time digests equal crypto/sha256
// even with the portable loop broken. The run-time half needs a CPU with
// sha (forced true it would fault elsewhere): the program is
// h2KernelProgram(true), which stops after the compile-time lines on a CPU
// without sha, and the test checks those lines before it skips the rest.
func TestSTDHASH_Sha256EvaluatorKernel(t *testing.T) {
	t.Parallel()
	h2EvaluatorKernel(t, h2KernelProgram(true))
}

// h2EvaluatorKernel runs src (h2KernelProgram(true) or a variant) with the
// kernel forced on and the portable loop broken: the compile-time lines
// must equal crypto/sha256 on any CPU, and the run-time lines too on a CPU
// with sha. On a CPU without sha the program has printed only the
// compile-time lines, and the test skips after checking them.
func h2EvaluatorKernel(t *testing.T, src string) {
	t.Helper()
	want := h2KernelWant()
	sha, got := h2KernelRunProgram(t, src, append(append([]sdForce{}, s8ShaOn...), h2BreakPortable...))
	if !sha {
		diffLines(t, strings.Join(got, "\n"), strings.Join(want[:len(h2KernelEval)], "\n"))
		t.Skip("this CPU lacks sha: the compile-time digests are checked; the forced kernel cannot run natively")
	}
	diffLines(t, strings.Join(got, "\n"), strings.Join(want, "\n"))
}

// TestSTDHASH_NoShaStopsAfterCompileTime: on a
// CPU without sha, TestSTDHASH_Sha256EvaluatorKernel's program ran the
// forced kernel natively and faulted (#UD) before the test could skip.
// Here the program's own CPU probe is forced false, as on such a CPU: it
// must exit 0 having printed exactly the compile-time lines, and they must
// be right, so the test skips cleanly instead of failing.
func TestSTDHASH_NoShaStopsAfterCompileTime(t *testing.T) {
	t.Parallel()
	const probe = `sha := simd.Cpu().Has("sha")`
	src := h2KernelProgram(true)
	if strings.Count(src, probe) != 1 {
		t.Fatalf("h2KernelProgram holds %q %d times, want 1", probe, strings.Count(src, probe))
	}
	src = strings.Replace(src, probe, `sha := simd.Cpu().Has("sha") && false`, 1)
	want := h2KernelWant()[:len(h2KernelEval)]
	sha, got := h2KernelRunProgram(t, src, append(append([]sdForce{}, s8ShaOn...), h2BreakPortable...))
	if sha {
		t.Fatal("the cpu line says true with the probe forced false")
	}
	diffLines(t, strings.Join(got, "\n"), strings.Join(want, "\n"))
}

// TestSTDHASH_OneKernelGuard: the kernel's guard
// (simd.Cpu()'s cache and the sha bit) was inlined into Sha256State's Bytes
// twice and Sum once, which put 11 and 4 unread jumps into std-crypto's
// constant-time check. Now Sha256Compress (the portable loop, once named
// Sha256Blocks) offers the blocks to the kernel itself, so in the release
// build only Sha256Compress reads the CPU cache or calls ShaNi, and Bytes
// and Sum keep their length tests (5 and 1).
func TestSTDHASH_OneKernelGuard(t *testing.T) {
	objdump := cryptoObjdump()
	if objdump == "" {
		t.Skip("needs llvm-objdump (LLVM 21)")
	}
	ins, _ := h2CtBuild(t, objdump, nil, false)
	guards := map[string]int{}
	for _, x := range ins {
		if strings.HasPrefix(x.fn, "mk.cpu.") || strings.HasPrefix(x.fn, "mk.std.simd.") {
			continue
		}
		if strings.Contains(x.args, "<mk.cpu.cache") || strings.Contains(x.args, "<mk.std.simd.kernel.ShaNi") {
			guards[x.fn]++
		}
	}
	for fn, n := range guards {
		// Sha512Blocks and Sha1Blocks hold the SHA-512 and SHA-1 kernels'
		// own guards, once each (the same shape as Sha256Compress's).
		if fn != "mk.std.hash.Sha256Compress" && fn != "mk.std.hash.Sha512Blocks" && fn != "mk.std.hash.Sha1Blocks" {
			t.Errorf("%s reads simd.Cpu()'s cache or calls ShaNi (%d instructions): the kernel's guard is inlined outside Sha256Compress", fn, n)
		}
	}
	if guards["mk.std.hash.Sha256Compress"] == 0 {
		t.Error("Sha256Compress holds no kernel guard: the probe no longer reaches the instruction path")
	}
	other, _ := ctClassify(ins)
	for fn, max := range map[string]int{"mk.std.hash.Sha256State:Bytes": 5, "mk.std.hash.Sha256State:Sum": 1} {
		if n := len(other[fn]); n > max {
			t.Errorf("%s has %d other jumps, want at most %d (its length tests): %q", fn, n, max, other[fn])
		}
	}
}
