// Why: hash.Xx64State feeds XXH64 in pieces and keeps a partial 32-byte stripe,
// where a split bug would hide.
// What: TestStdDefects_Xx64StateMatchesOneShot.
// How: after every piece, compares with the one-shot hash.Xx64 and the Go model
// hmXxh64 (checked against Zig by TestSTDHASHMOD_Xx64SeededMatchesZig), for
// every split shape around the stripe, under seeds that wrap the lanes' arithmetic.

package std

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// sd8XxCase is one stream: seed, then buf[off..off + sum(pieces)] fed in
// pieces, Sum printed after every piece.
type sd8XxCase struct {
	seed   uint64
	off    int
	pieces []int
}

// sd8XxCases are the shapes: every total from 0 to 100 bytes in one piece
// and as one byte at a time; pieces of 1, 3, 31, 32, 33 and 63 bytes, so a
// partial stripe is completed, left partial, and completed with whole
// stripes after it; random splits of up to 2,000 bytes; one 1 MiB stream
// in 64 KiB + 7 pieces. The seeds are 0, 1, all ones (v4 = seed - P1 wraps
// the other way) and P1's negation (v1 wraps).
func sd8XxCases() []sd8XxCase {
	seeds := []uint64{0, 1, ^uint64(0), ^uint64(hmXxP64_1) + 1}
	var cs []sd8XxCase
	for n := 0; n <= 100; n++ {
		cs = append(cs, sd8XxCase{seed: seeds[n%4], off: n % 5, pieces: []int{n}})
	}
	for _, n := range []int{31, 32, 33, 64, 65} {
		ones := make([]int, n)
		for i := range ones {
			ones[i] = 1
		}
		cs = append(cs, sd8XxCase{seed: seeds[n%4], off: 1, pieces: ones})
	}
	for k, p := range []int{1, 3, 31, 32, 33, 63} {
		ps := make([]int, 7)
		for i := range ps {
			ps[i] = p
		}
		cs = append(cs, sd8XxCase{seed: seeds[k%4], off: k, pieces: ps})
	}
	cs = append(cs, sd8XxCase{seed: 7, off: 2, pieces: []int{5, 27, 64, 0, 31, 33, 1}})
	src := rand.New(rand.NewPCG(80, 64))
	for range 40 {
		var ps []int
		for range 1 + src.IntN(12) {
			ps = append(ps, src.IntN(170))
		}
		cs = append(cs, sd8XxCase{seed: src.Uint64(), off: src.IntN(8), pieces: ps})
	}
	big := make([]int, 16)
	for i := range big {
		big[i] = 65536 + 7
	}
	cs = append(cs, sd8XxCase{seed: 0x9E3779B97F4A7C15, off: 3, pieces: big})
	return cs
}

func sd8XxMax(cs []sd8XxCase) int {
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

// sd8XxProgram feeds each case into a fresh Xx64State and prints, after
// every piece, Sum and hash.Xx64 of the bytes so far.
func sd8XxProgram(cs []sd8XxCase) string {
	var seeds, offs, counts, pieces []string
	for _, c := range cs {
		seeds = append(seeds, fmt.Sprintf("0x%x", c.seed))
		offs = append(offs, fmt.Sprint(c.off))
		counts = append(counts, fmt.Sprint(len(c.pieces)))
		for _, p := range c.pieces {
			pieces = append(pieces, fmt.Sprint(p))
		}
	}
	return h2Prelude + fmt.Sprintf(`
const Seeds: [%d]u64 = [%s]
const Offs: [%d]int = [%s]
const Counts: [%d]int = [%s]
const Pieces: [%d]int = [%s]

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    var r := Rng(s: 41)
    buf := try sys.arena.Slice[u8](%d)
    Fill(mut r, buf)
    var q := 0
    for k in 0..%d {
        var s := hash.Xx64State.New(Seeds[k])
        start := Offs[k]
        var at := start
        for _ in 0..Counts[k] {
            p := Pieces[q]
            q += 1
            s.Bytes(buf[at..at + p])
            at += p
            x := s.Sum()
            y := hash.Xx64(buf[start..at], Seeds[k])
            try Print(mut out, "{x:x} {y:x}\n", (x: x, y: y))
        }
    }
}
`, len(seeds), strings.Join(seeds, ", "), len(offs), strings.Join(offs, ", "), len(counts), strings.Join(counts, ", "), len(pieces), strings.Join(pieces, ", "), sd8XxMax(cs), len(cs))
}

// sd8XxWant is sd8XxProgram's output from the Go model.
func sd8XxWant(cs []sd8XxCase) string {
	buf := make([]byte, sd8XxMax(cs))
	r := hmSplitmix{s: 41}
	r.fill(buf)
	var b strings.Builder
	for _, c := range cs {
		at := c.off
		for _, p := range c.pieces {
			at += p
			h := hmXxh64(buf[c.off:at], c.seed)
			fmt.Fprintf(&b, "%x %x\n", h, h)
		}
	}
	return b.String()
}

// TestStdDefects_Xx64StateMatchesOneShot: Xx64State's Sum after every
// piece equals hash.Xx64 of the bytes so far and the Go model, dev and
// release.
func TestStdDefects_Xx64StateMatchesOneShot(t *testing.T) {
	t.Parallel()
	cs := sd8XxCases()
	src, want := sd8XxProgram(cs), sd8XxWant(cs)
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
