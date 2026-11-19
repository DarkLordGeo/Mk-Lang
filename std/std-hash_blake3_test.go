// Why: hash.Blake3 hashes four chunks at once in a vector leaf
// (std/hash/blake3_wide.mk), and a mistake there changes digests.
// What: oracle tests of the one-shot against the Go model hmBlake3, a
// control that breaks the leaf in a std copy, and mk check on every target.
// How: every tree shape that reaches the 4,096-byte leaf, at unaligned
// offsets, built natively (dev and release) and run at compile time.

package std

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// h2B3Lengths are the lengths that reach Blake3Subtree4: 4,096 k - 1,
// 4,096 k and 4,096 k + 1 for k = 1 to 9 (the leaf as the whole input,
// with ROOT on its top parent, as a left subtree, beside a ragged right
// side), the chunk counts 5 to 15 and 17, 31, 33 (ragged right sides of
// every size), and long inputs whose leaves are all 4-chunk subtrees.
func h2B3Lengths() []int {
	var lens []int
	for k := 1; k <= 9; k++ {
		lens = append(lens, 4096*k-1, 4096*k, 4096*k+1)
	}
	for _, c := range []int{5, 6, 7, 9, 10, 11, 12, 13, 14, 15, 17, 31, 33} {
		lens = append(lens, 1024*c, 1024*c-511)
	}
	return append(lens, 65536, 65536+4096+1, 262144+1000)
}

// h2B3Program prints, per length n and offset o (0, 1, 3), the one-shot
// digest of buf[o..o + n] in hex.
func h2B3Program(lens []int) string {
	var lit []string
	maxLen := 0
	for _, n := range lens {
		lit = append(lit, fmt.Sprint(n))
		maxLen = max(maxLen, n)
	}
	return h2Prelude + fmt.Sprintf(`
const Lens: [%d]int = [%s]
const Offs: [3]int = [0, 1, 3]

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    var r := Rng(s: 23)
    buf := try sys.arena.Slice[u8](%d)
    Fill(mut r, buf)
    for k in 0..%d {
        n := Lens[k]
        for m in 0..3 {
            o := Offs[m]
            d := hash.Blake3(buf[o..o + n])
            try Print(mut out, "{n} {o} ", (n: n, o: o))
            try Hex(mut out, d[..])
            try Print(mut out, "\n")
        }
    }
}
`, len(lens), strings.Join(lit, ", "), maxLen+3, len(lens))
}

// h2B3Want is h2B3Program's output from the Go model.
func h2B3Want(lens []int) string {
	maxLen := 0
	for _, n := range lens {
		maxLen = max(maxLen, n)
	}
	buf := make([]byte, maxLen+3)
	r := hmSplitmix{s: 23}
	r.fill(buf)
	var b strings.Builder
	for _, n := range lens {
		for _, o := range []int{0, 1, 3} {
			d := hmBlake3(buf[o : o+n])
			fmt.Fprintf(&b, "%d %d %s\n", n, o, hex.EncodeToString(d[:]))
		}
	}
	return b.String()
}

// TestSTDHASH_Blake3WideMatchesModel: the one-shot over every shape in
// h2B3Lengths at offsets 0, 1 and 3 equals the Go model, dev and release.
func TestSTDHASH_Blake3WideMatchesModel(t *testing.T) {
	t.Parallel()
	lens := h2B3Lengths()
	src, want := h2B3Program(lens), h2B3Want(lens)
	for _, flags := range [][]string{nil, {"--release"}} {
		name := "dev"
		if flags != nil {
			name = "release"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diffLines(t, h2Run(t, nil, src, flags...), want)
		})
	}
}

// TestSTDHASH_Blake3WideRuns is the control: with the 4-chunk leaf's
// top parent flag changed in a std copy, every length that reaches the
// leaf hashes wrong and every length below 4,096 still hashes right, so
// the leaf is on the one-shot's path.
func TestSTDHASH_Blake3WideRuns(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join("hash", "blake3_wide.mk"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	const old = "return Blake3Cv(&Blake3IV, &p, 0, 64, Blake3Parent | root)"
	if strings.Count(text, old) != 1 {
		t.Fatalf("hash/blake3_wide.mk no longer has %q once", old)
	}
	broken := map[string]string{"hash/blake3_wide.mk": strings.Replace(text, old, "return Blake3Cv(&Blake3IV, &p, 1, 64, Blake3Parent | root)", 1)}
	lens := []int{1000, 2048, 4095, 4096, 4097, 8192, 20480}
	got := strings.Split(strings.TrimSpace(h2Run(t, broken, h2B3Program(lens), "--release")), "\n")
	want := strings.Split(strings.TrimSpace(h2B3Want(lens)), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	for i := range want {
		var n int
		fmt.Sscan(want[i], &n)
		if n >= 4096 && got[i] == want[i] {
			t.Errorf("leaf broken, length %d still hashes right: the 4-chunk leaf did not run (%s)", n, got[i])
		}
		if n < 4096 && got[i] != want[i] {
			t.Errorf("leaf broken, length %d below the leaf hashes wrong: got %s, want %s", n, got[i], want[i])
		}
	}
}

// TestSTDHASH_Blake3WideCompileTime: the evaluator runs the leaf (its
// std.simd intrinsics' semantics) for consts of 4,096 bytes (the leaf is
// the whole input) and 16,385 bytes (four leaves and a lone byte), equal
// to the run-time digests and the Go model.
func TestSTDHASH_Blake3WideCompileTime(t *testing.T) {
	t.Parallel()
	src := h2Prelude + `
Make(): [16385]u8 {
    var b: [16385]u8 = [0; 16385]
    for i in 0..16385 {
        b[i] = Trunc[u8](i * 7 + 3)
    }
    return b
}

const Big: [16385]u8 = Make()
const A: [32]u8 = hash.Blake3(Big[0..4096])
const B: [32]u8 = hash.Blake3(Big[..])

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try Hex(mut out, A[..])
    try Print(mut out, " ")
    try Hex(mut out, hash.Blake3(Big[0..4096])[..])
    try Print(mut out, "\n")
    try Hex(mut out, B[..])
    try Print(mut out, " ")
    try Hex(mut out, hash.Blake3(Big[..])[..])
    try Print(mut out, "\n")
}
`
	big := make([]byte, 16385)
	for i := range big {
		big[i] = byte(i*7 + 3)
	}
	a, b := hmBlake3(big[:4096]), hmBlake3(big)
	ah, bh := hex.EncodeToString(a[:]), hex.EncodeToString(b[:])
	diffLines(t, h2Run(t, nil, src), fmt.Sprintf("%s %s\n%s %s\n", ah, ah, bh, bh))
}

// TestSTDHASH_Blake3WideCheckEveryTarget: a program whose Blake3 reaches
// the leaf checks clean on both hosted targets, aarch64-linux and
// x86_64-freestanding (std.simd's baseline width exists on each).
func TestSTDHASH_Blake3WideCheckEveryTarget(t *testing.T) {
	body := "    var b: [4096]u8 = [7; 4096]\n    _ = hash.Blake3(b[..])\n"
	hosted := "import std.hash\n\nexport Main(sys: &System): WriteError!void {\n" + body + "    var out := sys.Stdout()\n    try Print(mut out, \"done\\n\")\n}\n"
	free := "import std.hash\n\nexport extern MkEntry() {\n" + body + "}\n"
	for _, c := range []struct{ target, src string }{
		{"x86_64-windows", hosted}, {"x86_64-linux", hosted}, {"aarch64-linux", hosted}, {"x86_64-freestanding", free},
	} {
		t.Run(c.target, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			stdDir := copyStd(t, root, nil)
			if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, status := mkCmd(t, root, stdDir, "check", "--target", c.target, "main.mk"); status != 0 {
				t.Errorf("mk check --target %s: exit %d, want clean\n%s", c.target, status, out)
			}
		})
	}
}

// TestSTDHASH_Blake3ScalarPathMatchesModel: with the leaf's branch turned
// off in a std copy, the scalar recursion (the reference, and what
// x86_64-freestanding runs) gives the model's digests on the same shapes,
// so both paths stay checked on a hosted machine.
func TestSTDHASH_Blake3ScalarPathMatchesModel(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join("hash", "blake3.mk"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	const old = "if Target.os != Os.Freestanding && n == 4096 {"
	if strings.Count(text, old) != 1 {
		t.Fatalf("hash/blake3.mk no longer has %q once", old)
	}
	off := map[string]string{"hash/blake3.mk": strings.Replace(text, old, "if Target.os != Os.Freestanding && n == -1 {", 1)}
	lens := h2B3Lengths()
	diffLines(t, h2Run(t, off, h2B3Program(lens), "--release"), h2B3Want(lens))
}
