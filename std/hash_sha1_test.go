// Why: hash.Sha1 chooses the SHA extensions kernel or the portable loop by
// CPU, and both must give crypto/sha1's digests.
// What: TestHash_Sha1PathsMatchGo, TestHash_Sha1KernelRunsEveryBlock and
// TestHash_Sha1Linux.
// How: a fixture masks the CPU test so the portable loop runs anywhere; a
// control std with the portable loop broken shows which path ran.

package std

import (
	"crypto/sha1"
	"fmt"
	"strings"
	"testing"
)

// h11S1Off masks the SHA-1 kernel's sha test, so Sha1Blocks runs the
// portable loop.
var h11S1Off = []sdForce{{"simd/kernel/sha1.mk", `c.Has("sha")`, "false", 1}}

// h11S1BreakPortable breaks std.hash's portable SHA-1 loop (the first
// chaining word gains 1 per block), so a digest is right only when every
// block went through the kernel.
var h11S1BreakPortable = []sdForce{{"hash/sha1.mk", "h[0] = h[0] +% a\n", "h[0] = h[0] +% a +% 1\n", 1}}

// h11S1Lens are the one-shot and stream lengths: every length to 300 (55
// and 56 are Sum's one- and two-block paddings), then block edges and
// longer messages. Each is hashed at offset n % 16.
var h11S1Lens = append(s8Range(0, 300), 447, 448, 511, 512, 513, 1000, 4095, 4096, 4097, 65543)

// h11S1Edges are the lengths around block edges hashed at every offset 0
// to 15, so the kernel's 16-byte loads start at each alignment.
var h11S1Edges = []int{0, 1, 55, 56, 63, 64, 65, 119, 120, 127, 128, 129, 191, 192, 193, 255, 256, 257, 4096, 65543}

// h11S1Program prints whether simd.Cpu() has sha, then per length of
// h2KernelEval the compile-time Sha1 of h2ConstText's prefix ("E"), then
// per length of h11S1Lens the one-shot Sha1 ("L") and a Sha1State fed in
// seeded random pieces ("S") of splitmix64 bytes at offset n % 16, then
// per length of h11S1Edges and offset 0 to 15 the one-shot Sha1 ("O").
func h11S1Program() string { return h11S1ProgramStop(false) }

// h11S1ProgramStop is h11S1Program; with stop, a CPU without sha stops
// after the compile-time lines (a forced kernel would fault there).
func h11S1ProgramStop(stop bool) string {
	var b strings.Builder
	b.WriteString(strings.Replace(h2Prelude, "import std.hash\n", "import std.hash\nimport std.simd\n", 1))
	fmt.Fprintf(&b, "\nconst Text = %q\n", h2ConstText)
	for i, n := range h2KernelEval {
		fmt.Fprintf(&b, "const E%d: [20]u8 = hash.Sha1(Text.AsBytes()[0..%d])\n", i, n)
	}
	fmt.Fprintf(&b, "const Lens: [%d]int = %s\n", len(h11S1Lens), mkIntArray(h11S1Lens))
	fmt.Fprintf(&b, "const Edges: [%d]int = %s\n", len(h11S1Edges), mkIntArray(h11S1Edges))
	fmt.Fprintf(&b, "const Pieces: [%d]int = %s\n", len(h2Pieces), mkIntArray(h2Pieces))
	b.WriteString(`
export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    has := simd.Cpu().Has("sha")
    try Print(mut out, "cpu {h}\n", (h: has))
`)
	for i := range h2KernelEval {
		fmt.Fprintf(&b, "    try Print(mut out, \"E \")\n    try Hex(mut out, E%d[..])\n    try Print(mut out, \"\\n\")\n", i)
	}
	if stop {
		b.WriteString("    if !has {\n        return\n    }\n")
	}
	b.WriteString(`    var r := Rng(s: 1)
    buf := try sys.arena.Slice[u8](65543 + 16)
    Fill(mut r, buf)
    var sr := Rng(s: 7)
    for k in 0..Lens.Len() {
        n := Lens[k]
        off := n % 16
        m := buf[off..off + n]
        try Print(mut out, "L ")
        try Hex(mut out, hash.Sha1(m)[..])
        try Print(mut out, "\n")
        var s := hash.Sha1State.New()
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
    for k in 0..Edges.Len() {
        n := Edges[k]
        for off in 0..16 {
            try Print(mut out, "O ")
            try Hex(mut out, hash.Sha1(buf[off..off + n])[..])
            try Print(mut out, "\n")
        }
    }
}
`)
	return b.String()
}

// h11S1Want is the oracle's output for h11S1Program after its cpu line.
func h11S1Want() []string {
	var lines []string
	for _, n := range h2KernelEval {
		lines = append(lines, fmt.Sprintf("E %x", sha1.Sum([]byte(h2ConstText[:n]))))
	}
	r := hmSplitmix{1}
	buf := make([]byte, 65543+16)
	r.fill(buf)
	sr := hmSplitmix{7}
	for _, n := range h11S1Lens {
		off := n % 16
		d := sha1.Sum(buf[off : off+n])
		for i := 0; i < n; {
			i += min(h2Pieces[sr.next()%16], n-i)
		}
		lines = append(lines, fmt.Sprintf("L %x", d), fmt.Sprintf("S %x", d))
	}
	for _, n := range h11S1Edges {
		for off := 0; off < 16; off++ {
			lines = append(lines, fmt.Sprintf("O %x", sha1.Sum(buf[off:off+n])))
		}
	}
	return lines
}

// TestHash_Sha1PathsMatchGo: hash.Sha1 and Sha1State, as built (the
// kernel where the CPU has sha) and with the CPU test masked (the
// portable loop on any machine), at dev and release, equal crypto/sha1 at
// every length to 300, at block edges at each offset 0 to 15, and in
// streams; the compile-time digests (the evaluator, where Cpu() is the
// baseline, so the portable loop) too. "abc" is FIPS 180-4's example.
func TestHash_Sha1PathsMatchGo(t *testing.T) {
	t.Parallel()
	if got := fmt.Sprintf("%x", sha1.Sum([]byte("abc"))); got != "a9993e364706816aba3e25717850c26c9cd0d89d" {
		t.Fatalf("oracle SHA-1(abc) = %s", got)
	}
	want := h11S1Want()
	for _, c := range []struct {
		name   string
		forces []sdForce
		flags  []string
	}{
		{"dispatch-dev", nil, nil},
		{"dispatch-release", nil, []string{"--release"}},
		{"masked-dev", h11S1Off, nil},
		{"masked-release", h11S1Off, []string{"--release"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			has, got := h2KernelRunProgram(t, h11S1Program(), c.forces, c.flags...)
			t.Logf("simd.Cpu() has sha = %v", has)
			diffLines(t, strings.Join(got, "\n"), strings.Join(want, "\n"))
		})
	}
}

// TestHash_Sha1KernelRunsEveryBlock: with the portable loop broken, a
// build on a CPU with sha still gives crypto/sha1's digests at run time
// (every block of Bytes and Sum went through the kernel) and wrong ones at
// compile time (the evaluator's Cpu() is the baseline); masked, every
// digest is wrong (the portable loop ran).
func TestHash_Sha1KernelRunsEveryBlock(t *testing.T) {
	t.Parallel()
	want := h11S1Want()
	total := h2Total(want)
	for _, flags := range [][]string{nil, {"--release"}} {
		name := "dev"
		if flags != nil {
			name = "release"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, masked := h2KernelRunProgram(t, h11S1Program(), append(append([]sdForce{}, h11S1Off...), h11S1BreakPortable...), flags...)
			if bad := h2Mismatches(t, masked, want); bad["E"] != total["E"] || bad["L"] != total["L"] || bad["S"] != total["S"] || bad["O"] != total["O"] {
				t.Fatalf("masked with the portable loop broken: %v wrong of %v; want all (the portable loop ran nowhere?)", bad, total)
			}
			has, got := h2KernelRunProgram(t, h11S1Program(), h11S1BreakPortable, flags...)
			bad := h2Mismatches(t, got, want)
			if bad["E"] != total["E"] {
				t.Fatalf("compile time with the portable loop broken: %d of %d wrong; want all (the evaluator's Cpu() is the baseline)", bad["E"], total["E"])
			}
			if !has {
				t.Skip("this CPU lacks sha: the kernel cannot run; the masked and compile-time halves passed")
			}
			if bad["L"] != 0 || bad["S"] != 0 || bad["O"] != 0 {
				t.Fatalf("CPU has sha, portable loop broken: %v wrong; want none (a block missed the kernel)", bad)
			}
		})
	}
}

// TestHash_Sha1Linux: the SHA-1 program on x86_64-linux, as built and
// masked, at dev and release, equals its oracle; with the portable loop
// broken the run-time digests stay right where the CPU has sha (the
// kernel ran there too). Linux leaves run only with MK_LINUX=1, under
// tools/wsl-lock.ps1, with go test -p 1.
func TestHash_Sha1Linux(t *testing.T) {
	needWSL(t)
	want := h11S1Want()
	for _, flags := range [][]string{nil, {"--release"}} {
		name := "dev"
		if flags != nil {
			name = "release"
		}
		t.Run(name, func(t *testing.T) {
			for _, c := range []struct {
				name   string
				forces []sdForce
			}{
				{"dispatch", nil},
				{"masked", h11S1Off},
			} {
				has, got := h11LinuxLines(t, h11S1Program(), c.forces, flags...)
				t.Logf("%s: cpu %v", c.name, has)
				diffLines(t, strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			has, got := h11LinuxLines(t, h11S1Program(), h11S1BreakPortable, flags...)
			if !has {
				t.Log("this CPU lacks sha under WSL; the kernel-runs half was not run")
				return
			}
			if bad := h2Mismatches(t, got, want); bad["L"] != 0 || bad["S"] != 0 || bad["O"] != 0 {
				t.Errorf("portable loop broken: %v wrong; want none (a block missed the kernel)", bad)
			}
		})
	}
}
