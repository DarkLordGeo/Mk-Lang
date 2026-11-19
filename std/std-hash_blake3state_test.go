// Why: Blake3State.Bytes feeds whole 4-chunk runs to the vector leaf
// (Blake3Chunks4), so streaming must give the one-shot's digests.
// What: TestSTDHASH_Blake3StateLeafMatchesModel over h2B3sCases.
// How: every digest, after every piece, against the Go model hmBlake3
// (itself checked by TestSTDHASHMOD_ModelsMatchPublishedVectors).

package std

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// h2B3sCase is one stream: the input buf[off..off + sum(pieces)] fed in
// pieces, with Sum printed after every piece.
type h2B3sCase struct {
	off    int
	pieces []int
}

// h2B3sCases are the shapes around the leaf: pieces of 4,096 k - 1,
// 4,096 k and 4,096 k + 1 (the leaf's run ends exactly at, before and after
// a piece), pieces that are not chunk multiples, ragged prefixes (1, 64,
// 1,024 and 1,025 bytes, so the leaf starts at a chunk boundary that is
// not a multiple of four chunks, or not at all until the chunk closes),
// totals of exactly 4,096 k (the last chunk must stay pending for ROOT),
// and a long input in 64 KiB pieces.
func h2B3sCases() []h2B3sCase {
	var cs []h2B3sCase
	for _, p := range []int{4095, 4096, 4097, 8191, 8192, 8193, 12288, 16383, 16384, 16385, 5000, 1500} {
		n := 3
		if p < 4096 {
			n = 7
		}
		ps := make([]int, n)
		for i := range ps {
			ps[i] = p
		}
		cs = append(cs, h2B3sCase{off: 0, pieces: ps}, h2B3sCase{off: 3, pieces: ps})
	}
	for _, first := range []int{1, 64, 1024, 1025, 2048, 3072, 4095} {
		cs = append(cs, h2B3sCase{off: 1, pieces: []int{first, 20480, 4097, 1}})
	}
	for _, total := range []int{4096, 8192, 16384, 32768} {
		cs = append(cs, h2B3sCase{off: 0, pieces: []int{total}}, h2B3sCase{off: 0, pieces: []int{total - 1, 1}})
	}
	cs = append(cs, h2B3sCase{off: 5, pieces: []int{65536, 65536, 65536, 65536, 7}})
	return cs
}

// h2B3sMax is the buffer the cases need.
func h2B3sMax(cs []h2B3sCase) int {
	m := 0
	for _, c := range cs {
		n := c.off
		for _, p := range c.pieces {
			n += p
		}
		m = max(m, n)
	}
	return m
}

// h2B3sProgram feeds each case into a fresh Blake3State and prints the
// digest after every piece.
func h2B3sProgram(cs []h2B3sCase) string {
	var offs, counts, pieces []string
	for _, c := range cs {
		offs = append(offs, fmt.Sprint(c.off))
		counts = append(counts, fmt.Sprint(len(c.pieces)))
		for _, p := range c.pieces {
			pieces = append(pieces, fmt.Sprint(p))
		}
	}
	return h2Prelude + fmt.Sprintf(`
const Offs: [%d]int = [%s]
const Counts: [%d]int = [%s]
const Pieces: [%d]int = [%s]

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    var r := Rng(s: 29)
    buf := try sys.arena.Slice[u8](%d)
    Fill(mut r, buf)
    var q := 0
    for k in 0..%d {
        var s := hash.Blake3State.New()
        var at := Offs[k]
        for _ in 0..Counts[k] {
            p := Pieces[q]
            q += 1
            s.Bytes(buf[at..at + p])
            at += p
            d := s.Sum()
            try Hex(mut out, d[..])
            try Print(mut out, "\n")
        }
    }
}
`, len(offs), strings.Join(offs, ", "), len(counts), strings.Join(counts, ", "), len(pieces), strings.Join(pieces, ", "), h2B3sMax(cs), len(cs))
}

// h2B3sWant is h2B3sProgram's output from the model.
func h2B3sWant(cs []h2B3sCase) string {
	buf := make([]byte, h2B3sMax(cs))
	r := hmSplitmix{s: 29}
	r.fill(buf)
	var b strings.Builder
	for _, c := range cs {
		at := c.off
		for _, p := range c.pieces {
			at += p
			d := hmBlake3(buf[c.off:at])
			b.WriteString(hex.EncodeToString(d[:]) + "\n")
		}
	}
	return b.String()
}

// TestSTDHASH_Blake3StateLeafMatchesModel: Blake3State fed every shape of h2B3sCases equals the model after every
// piece, dev and release.
func TestSTDHASH_Blake3StateLeafMatchesModel(t *testing.T) {
	t.Parallel()
	cs := h2B3sCases()
	src, want := h2B3sProgram(cs), h2B3sWant(cs)
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

// h2B3sLeafCall is the state's call of the leaf, which the controls break
// or turn off in a std copy.
const h2B3sLeafCall = "cvs := Blake3Chunks4(data[i..i + 4096], self.chunk)"

// h2B3sFixture is std/hash/blake3.mk with the state's leaf call replaced.
func h2B3sFixture(t *testing.T, repl string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("hash", "blake3.mk"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	if strings.Count(text, h2B3sLeafCall) != 1 {
		t.Fatalf("hash/blake3.mk no longer has %q once", h2B3sLeafCall)
	}
	return map[string]string{"hash/blake3.mk": strings.Replace(text, h2B3sLeafCall, repl, 1)}
}

// TestSTDHASH_Blake3StateLeafRuns is the control: with the state's
// leaf given the wrong chunk counter in a std copy, a stream whose pieces
// hold more than four whole chunks past a chunk boundary hashes wrong, and
// a stream of 1,500-byte pieces (never four whole chunks with input after
// them) still hashes right, so the leaf is on the state's path for large
// pieces and only there.
func TestSTDHASH_Blake3StateLeafRuns(t *testing.T) {
	t.Parallel()
	broken := h2B3sFixture(t, "cvs := Blake3Chunks4(data[i..i + 4096], self.chunk +% 1)")
	cs := []h2B3sCase{{off: 0, pieces: []int{8193}}, {off: 0, pieces: []int{1500, 1500, 1500, 1500, 1500, 1500}}}
	got := strings.Split(strings.TrimSpace(h2Run(t, broken, h2B3sProgram(cs), "--release")), "\n")
	want := strings.Split(strings.TrimSpace(h2B3sWant(cs)), "\n")
	if len(got) != 7 || len(want) != 7 {
		t.Fatalf("got %d lines, want 7 (model %d)", len(got), len(want))
	}
	if got[0] == want[0] {
		t.Errorf("leaf broken, an 8,193-byte piece still hashes right: the state did not take the leaf")
	}
	for i := 1; i < 7; i++ {
		if got[i] != want[i] {
			t.Errorf("leaf broken, 1,500-byte pieces hash wrong after piece %d: got %s, want %s", i, got[i], want[i])
		}
	}
}

// TestSTDHASH_Blake3StateScalarPathMatchesModel: with the state's
// leaf turned off in a std copy (what x86_64-freestanding runs), the
// block-at-a-time path gives the model's digests on the same shapes.
func TestSTDHASH_Blake3StateScalarPathMatchesModel(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join("hash", "blake3.mk"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	const old = "if Target.os != Os.Freestanding && self.blen == 0 && self.blocks == 0 {"
	if strings.Count(text, old) != 1 {
		t.Fatalf("hash/blake3.mk no longer has %q once", old)
	}
	off := map[string]string{"hash/blake3.mk": strings.Replace(text, old, "if Target.os != Os.Freestanding && self.blen == -1 {", 1)}
	cs := h2B3sCases()
	diffLines(t, h2Run(t, off, h2B3sProgram(cs), "--release"), h2B3sWant(cs))
}

// TestSTDHASH_Blake3StateCompileTime: a Blake3State fed 8,193 bytes
// in one piece as a const (the evaluator runs the leaf's std.simd
// intrinsics once and keeps the last chunk pending) equals the run-time
// state and the model.
func TestSTDHASH_Blake3StateCompileTime(t *testing.T) {
	t.Parallel()
	src := h2Prelude + `
Make(): [8193]u8 {
    var b: [8193]u8 = [0; 8193]
    for i in 0..8193 {
        b[i] = Trunc[u8](i * 5 + 1)
    }
    return b
}

Stream(b: []u8): [32]u8 {
    var s := hash.Blake3State.New()
    s.Bytes(b)
    return s.Sum()
}

const Big: [8193]u8 = Make()
const C: [32]u8 = Stream(Big[..])

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try Hex(mut out, C[..])
    try Print(mut out, " ")
    try Hex(mut out, Stream(Big[..])[..])
    try Print(mut out, "\n")
}
`
	big := make([]byte, 8193)
	for i := range big {
		big[i] = byte(i*5 + 1)
	}
	d := hmBlake3(big)
	h := hex.EncodeToString(d[:])
	diffLines(t, h2Run(t, nil, src), h+" "+h+"\n")
}

// TestSTDHASH_Blake3StateCheckEveryTarget: a program feeding a
// Blake3State checks clean on both hosted targets, aarch64-linux and
// x86_64-freestanding, where the Target test keeps the leaf out.
func TestSTDHASH_Blake3StateCheckEveryTarget(t *testing.T) {
	body := "    var b: [8193]u8 = [7; 8193]\n    var s := hash.Blake3State.New()\n    s.Bytes(b[..])\n    _ = s.Sum()\n"
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
