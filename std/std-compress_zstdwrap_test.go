// Why: right after the window wraps, a window of the limit plus one block would hand out a byte the block
// had already overwritten.
// What: hand-built frames with a match at the largest offset the decoder admits (the window plus the block's output so far).
// How: the oracle is each frame's construction, checked by Go's zstd decoder (and libzstd 1.5.7 when MK_ZDEC
// names it). Part of the cuttable zstd unit: delete with std/compress/zstd*.mk.

package std

import (
	"fmt"
	"hash/crc32"
	"math/rand/v2"
	"testing"
)

// c2ZLLCode is the literal-length code, its extra bit count and extra
// value for v (RFC 8878 section 3.1.1.3.2.1.1).
func c2ZLLCode(v int) (code, nb, extra int) {
	if v < 16 {
		return v, 0, 0
	}
	base := []int{16, 18, 20, 22, 24, 28, 32, 40, 48, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536}
	bits := []int{1, 1, 1, 1, 2, 2, 3, 3, 4, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	for i := len(base) - 1; i >= 0; i-- {
		if v >= base[i] {
			return 16 + i, bits[i], v - base[i]
		}
	}
	panic("unreachable")
}

// c2ZOneSeqBlock is a compressed block's body: raw literals lits, then one
// sequence (literal length len(lits), match length ml, offset off) with
// RLE tables for all three codes, so the bitstream holds only the extra
// bits: written LL, ML, OF and then the end marker, read back OF, ML, LL.
func c2ZOneSeqBlock(lits []byte, ml, off int) []byte {
	var b []byte
	n := len(lits)
	switch {
	case n < 32:
		b = append(b, byte(n<<3))
	case n < 4096:
		b = append(b, byte(1<<2|(n&15)<<4), byte(n>>4))
	default:
		b = append(b, byte(3<<2|(n&15)<<4), byte(n>>4), byte(n>>12))
	}
	b = append(b, lits...)
	llc, llb, lle := c2ZLLCode(n)
	if ml < 3 || ml > 34 {
		panic("ml outside the direct codes")
	}
	mlc := ml - 3
	ov := off + 3
	ofc := 0
	for 1<<(ofc+1) <= ov {
		ofc++
	}
	ofe := ov - 1<<ofc
	b = append(b, 1, 0x54, byte(llc), byte(ofc), byte(mlc))
	var acc uint64
	pos := 0
	put := func(v, nb int) {
		acc |= uint64(v) << pos
		pos += nb
	}
	put(lle, llb)
	put(0, 0)
	put(ofe, ofc)
	put(1, 1)
	for i := 0; i < (pos+7)/8; i++ {
		b = append(b, byte(acc>>(8*i)))
	}
	return b
}

// c2ZWrapFrame is a frame with a 128 KiB window (descriptor 0x38, no
// content size): raw blocks of the random prefix pre, then one last
// compressed block of lits and a match of ml bytes at offset off. want is
// the decoded output (nil when off reaches before the stream's start).
func c2ZWrapFrame(pre, lits []byte, ml, off int) (frame, want []byte) {
	var parts [][]byte
	parts = append(parts, []byte{0x00, 0x38})
	for i := 0; i < len(pre); i += 1 << 17 {
		j := min(i+1<<17, len(pre))
		parts = append(parts, c2ZBlockHdr(false, 0, j-i), pre[i:j])
	}
	body := c2ZOneSeqBlock(lits, ml, off)
	parts = append(parts, c2ZBlockHdr(true, 2, len(body)), body)
	frame = c2ZRaw(parts...)
	out := append(append([]byte{}, pre...), lits...)
	src := len(out) - off
	if src < 0 {
		return frame, nil
	}
	for k := 0; k < ml; k++ {
		out = append(out, out[src+k])
	}
	return frame, out
}

// TestCompress_ZstdWrapEdge: after prefixes that make the window wrap
// just past the frame's window (128 KiB + 1, the edge of a window of the
// limit plus one block) and just past the limit plus one block (256 KiB +
// 1, this decoder's edge), a block of 1 to 60,000 literals and a match at
// the largest admitted offset (the window plus the literals), one less,
// and one more (Corrupt at the block's header): Read and the Reader, in
// pieces and whole, under a limit equal to the window, equal the frame's
// construction; Go's decoder agrees on each frame.
func TestCompress_ZstdWrapEdge(t *testing.T) {
	exe := c2ZExe(t)
	const ws = 1 << 17
	r := rand.New(rand.NewPCG(8, 14))
	var frames [][]byte
	var wants []czRes
	var names []string
	refPast := map[string]int{}
	for _, p := range []int{ws + 1, ws + 2, 2*ws - 1, 2*ws + 1, 2*ws + 7, 3*ws - 3} {
		pre := make([]byte, p)
		for i := range pre {
			pre[i] = byte(r.Uint32())
		}
		for _, nl := range []int{1, 31, 100, 4095, 4096, 60000} {
			lits := make([]byte, nl)
			for i := range lits {
				lits[i] = byte(r.Uint32())
			}
			for _, d := range []int{-1, 0, 1} {
				off := ws + nl + d
				f, w := c2ZWrapFrame(pre, lits, 10, off)
				var want czRes
				hdr := 6 + (p+(1<<17)-1)/(1<<17)*3 + p
				if d > 0 {
					want = czRes{verdict: "corrupt", offset: hdr}
				} else {
					want = czRes{verdict: "ok", n: len(w), crc: crc32.ChecksumIEEE(w)}
				}
				frames = append(frames, f)
				wants = append(wants, want)
				names = append(names, fmt.Sprintf("prefix %d, literals %d, offset window+%d%+d", p, nl, nl, d))
				g := c2ZstdGoRes(f)
				if d <= 0 && !c2ZSame(want, g) {
					t.Errorf("%s: Go's decoder %v, want %v", names[len(names)-1], g, want)
				}
				if d > 0 && g.verdict != "corrupt" {
					t.Errorf("%s: Go's decoder %v, want corrupt", names[len(names)-1], g)
				}
				if ref := c2ZRef(f); ref != "" && d <= 0 && ref != "ok" {
					t.Errorf("%s: libzstd says %s", names[len(names)-1], ref)
				} else if ref != "" && d > 0 {
					refPast[ref]++
				}
			}
		}
	}
	for _, c := range []struct {
		form       byte
		maxp, maxo int
	}{{0, 1 << 20, 1 << 20}, {0, 997, 70000}, {1, 4096, 333}, {1, 1 << 20, 1 << 20}} {
		got := c2ZAsk(t, exe, frames, c.form, 3, c.maxp, c.maxo, ws)
		bad := 0
		for i := range frames {
			if !c2Exact(got[i], wants[i]) {
				bad++
				if bad <= 8 {
					t.Errorf("%s (form %d, pieces %d/%d): got %v, want %v", names[i], c.form, c.maxp, c.maxo, got[i], wants[i])
				}
			}
		}
	}
	t.Logf("%d frames; libzstd on the offsets past the window: %v", len(frames), refPast)
}
