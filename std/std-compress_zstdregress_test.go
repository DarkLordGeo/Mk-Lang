// Why: regression tests for zstd decoder bugs once found: an unreported out-of-memory and a wrapping-window copy.
// What: ZstdContent's wrap fails with OutOfMemory when the arena cannot give the decoder its buffers; a second
// fuzz target, FuzzCompress_ZstdWrap, runs the decoder under a 1 KiB window limit.
// How: seeds whose output passes the window's wrap send mutations through ZBody's wrap and ZCopy's old-segment
// copy. Part of the cuttable zstd unit: delete with std/compress/zstd*.mk.
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os/exec"
	"strings"
	"testing"
)

// TestCompress_ZstdContentWrapOOM: in a std copy whose ZInCap is
// 2^62 (the window is given, the input buffer cannot be), ZstdContent's
// wrap fails with OutOfMemory; on the live std the same program reads the
// frame's first bytes.
func TestCompress_ZstdContentWrapOOM(t *testing.T) {
	const line = `c := if sys.args[1] == "gzip" { compress.GzipContent } else { compress.DeflateContent }`
	if !strings.Contains(c2WrapOOMProg, line) {
		t.Fatalf("c2WrapOOMProg has no %q", line)
	}
	src := strings.Replace(c2WrapOOMProg, line, "c := compress.ZstdContent", 1)
	live := c2Exe(t, "zwrapoom", src)
	dir := t.TempDir()
	stdDir := czStdCopy(t, dir)
	c2PatchStd(t, stdDir, "zstd.mk", "const ZInCap = 135168", "const ZInCap = 4611686018427387904")
	patched := czBuildStd(t, dir, stdDir, src)
	// One raw block of "hi".
	frame := c2ZRaw([]byte{0x00, 0x00}, c2ZBlockHdr(true, 0, 2), []byte("hi"))
	if got := runNative(t, live, string(frame), "zstd"); got.stdout != "read 2\n" || got.code != 0 {
		t.Errorf("live std: got %q (exit %d, stderr %s), want %q", got.stdout, got.code, got.stderr, "read 2\n")
	}
	if got := runNative(t, patched, string(frame), "zstd"); got.stdout != "wrap oom\n" || got.code != 0 {
		t.Errorf("no input buffer: got %q (exit %d, stderr %s), want %q", got.stdout, got.code, got.stderr, "wrap oom\n")
	}
}

// c2ZOverLimit reports whether a frame header in data that a decoder can
// reach (walking frames, skippable frames and block headers) declares a
// window over the limit wl (8 MiB for wl <= 0): the Window_Descriptor's
// size, or a single-segment frame's content size, as ZHeader judges it.
func c2ZOverLimit(data []byte, wl int) bool {
	limit := uint64(wl)
	if wl <= 0 {
		limit = 8 << 20
	}
	le := binary.LittleEndian
	i := 0
	for i+4 <= len(data) {
		magic := le.Uint32(data[i:])
		if magic&0xFFFFFFF0 == 0x184D2A50 {
			if i+8 > len(data) {
				return false
			}
			i += 8 + int(le.Uint32(data[i+4:]))
			continue
		}
		if magic != 0xFD2FB528 || i+5 > len(data) {
			return false
		}
		fhd := data[i+4]
		i += 5
		ss := fhd&0x20 != 0
		fcsz := []int{0, 2, 4, 8}[fhd>>6]
		if fhd>>6 == 0 && ss {
			fcsz = 1
		}
		var window uint64
		if !ss {
			if i >= len(data) {
				return false
			}
			wd := data[i]
			base := uint64(1) << (10 + wd>>3)
			window = base + base/8*uint64(wd&7)
			i++
		}
		i += []int{0, 1, 2, 4}[fhd&3]
		if i+fcsz > len(data) {
			return false
		}
		if ss {
			switch fcsz {
			case 1:
				window = uint64(data[i])
			case 2:
				window = 256 + uint64(le.Uint16(data[i:]))
			case 4:
				window = uint64(le.Uint32(data[i:]))
			case 8:
				window = le.Uint64(data[i:])
			}
		}
		if window > limit {
			return true
		}
		i += fcsz
		for {
			if i+3 > len(data) {
				return false
			}
			h := int(data[i]) | int(data[i+1])<<8 | int(data[i+2])<<16
			typ := (h >> 1) & 3
			if typ == 3 {
				return false
			}
			n := h >> 3
			if typ == 1 {
				n = 1
			}
			i += 3 + n
			if h&1 != 0 {
				if fhd&4 != 0 {
					i += 4
				}
				break
			}
		}
	}
	return false
}

// c2ZSmallWrapFrame is a frame with a 1 KiB window (descriptor 0x00, no
// content size, no checksum): nrle RLE blocks of 1 KiB, each of its own
// byte, then one last compressed block of lits and a match of ml bytes at
// offset off. Under a 1 KiB limit the window is 1 KiB + 256 KiB, so after
// 257 blocks the next block starts the window again, and a match that
// reaches past it reads the old segment's end. A NewLazy decoder's window
// is the frame's 1 KiB plus two 1 KiB blocks, so it wraps every
// third block, and nrle 256, 257 and 258 put the match block at each
// place in that cycle. want is the decoded output, nil when off is past
// the bound the decoder admits (the window plus the literals), which is
// Corrupt at hdr, the last block's header.
func c2ZSmallWrapFrame(nrle int, lits []byte, ml, off int) (frame, want []byte, hdr int) {
	parts := [][]byte{{0x00, 0x00}}
	var out []byte
	for i := 0; i < nrle; i++ {
		v := byte(i*37 + 11)
		parts = append(parts, c2ZBlockHdr(false, 1, 1024), []byte{v})
		out = append(out, bytes.Repeat([]byte{v}, 1024)...)
	}
	body := c2ZOneSeqBlock(lits, ml, off)
	parts = append(parts, c2ZBlockHdr(true, 2, len(body)), body)
	frame = c2ZRaw(parts...)
	hdr = 6 + 4*nrle
	if off > 1024+len(lits) {
		return frame, nil, hdr
	}
	out = append(out, lits...)
	src := len(out) - off
	for k := 0; k < ml; k++ {
		out = append(out, out[src+k])
	}
	return frame, out, hdr
}

// c2ZSmallWrapCases are c2ZSmallWrapFrame's frames: the match block right
// at the wrap (257 RLE blocks before it) and one and three blocks after,
// literals 0 to 1,000, offsets reaching into the old segment, at the
// largest admitted offset, and one past it.
func c2ZSmallWrapCases() (frames [][]byte, wants []czRes, names []string) {
	for _, nrle := range []int{256, 257, 258, 260} {
		for _, nl := range []int{0, 1, 31, 500, 990} {
			lits := make([]byte, nl)
			for i := range lits {
				lits[i] = byte(i*7 + nrle)
			}
			for _, off := range []int{nl + 1, nl + 5, nl + 600, 1024 + nl - 1, 1024 + nl, 1024 + nl + 1} {
				for _, ml := range []int{3, 34} {
					f, w, hdr := c2ZSmallWrapFrame(nrle, lits, ml, off)
					want := czRes{verdict: "corrupt", offset: hdr}
					if w != nil {
						want = czRes{verdict: "ok", n: len(w), crc: crc32.ChecksumIEEE(w)}
					}
					frames = append(frames, f)
					wants = append(wants, want)
					names = append(names, fmt.Sprintf("rle %d, literals %d, ml %d, offset %d", nrle, nl, ml, off))
				}
			}
		}
	}
	return frames, wants, names
}

// TestCompress_SmallWindowWrap: under a 1 KiB limit, the small-
// window wrap frames decode to their construction through Read and the
// Reader of New and of NewLazy, whole and in pieces, Corrupt at the last block's header one past
// the admitted offset; Go's decoder agrees on each frame. These frames
// seed FuzzCompress_ZstdWrap.
func TestCompress_SmallWindowWrap(t *testing.T) {
	exe := c2ZExe(t)
	frames, wants, names := c2ZSmallWrapCases()
	for i, f := range frames {
		g := c2ZstdGoRes(f)
		if wants[i].verdict == "ok" && !c2ZSame(wants[i], g) || wants[i].verdict != "ok" && g.verdict != "corrupt" {
			t.Errorf("%s: Go's decoder %v, want %v", names[i], g, wants[i])
		}
	}
	for _, c := range []struct {
		form       byte
		maxp, maxo int
	}{{0, 1 << 20, 1 << 20}, {0, 997, 70000}, {1, 4096, 333}, {1, 13, 1 << 20}, {3, 1 << 20, 1 << 20}, {3, 997, 70000}, {4, 4096, 333}, {4, 13, 1 << 20}} {
		got := c2ZAsk(t, exe, frames, c.form, 5, c.maxp, c.maxo, 1024)
		bad := 0
		for i := range frames {
			if !c2Exact(got[i], wants[i]) {
				if bad++; bad <= 8 {
					t.Errorf("%s (form %d, pieces %d/%d): got %v, want %v", names[i], c.form, c.maxp, c.maxo, got[i], wants[i])
				}
			}
		}
	}
	t.Logf("%d frames", len(frames))
}

// TestCompress_TooLargeNeedsHeader: c2ZstdDiffers accepts a
// TooLarge verdict only when a reachable frame header declares a window
// (or a single-segment content size) over the limit.
func TestCompress_TooLargeNeedsHeader(t *testing.T) {
	tl := czRes{verdict: "toolarge"}
	ok := czRes{verdict: "ok"}
	w1k := c2ZRaw([]byte{0x00, 0x00}, c2ZBlockHdr(true, 0, 2), []byte("hi"))
	w2k := c2ZRaw([]byte{0x00, 0x08}, c2ZBlockHdr(true, 0, 2), []byte("hi"))
	ss := c2ZRaw([]byte{0x60, 0x00, 0x08}, c2ZBlockHdr(true, 1, 2304), []byte("x")) // single segment, 2,304 bytes
	skip := []byte{0x50, 0x2A, 0x4D, 0x18, 0, 0, 0, 0}
	for _, c := range []struct {
		name string
		data []byte
		wl   int
		want bool
	}{
		{"1 KiB window, limit 1 KiB", w1k, 1024, false},
		{"1 KiB window, limit 512", w1k, 512, true},
		{"2 KiB window, limit 1 KiB", w2k, 1024, true},
		{"2 KiB window, default limit", w2k, 0, false},
		{"second frame's 2 KiB window", bytes.Join([][]byte{w1k, skip, w2k}, nil), 1024, true},
		{"single segment 2,304 bytes, limit 2 KiB", ss, 2048, true},
		{"single segment 2,304 bytes, limit 4 KiB", ss, 4096, false},
	} {
		if got := c2ZOverLimit(c.data, c.wl); got != c.want {
			t.Errorf("%s: c2ZOverLimit %v, want %v", c.name, got, c.want)
		}
		if why := c2ZstdDiffers(c.data, tl, ok, c.wl); (why != "") != c.want {
			t.Errorf("%s: c2ZstdDiffers %q", c.name, why)
		}
	}
}

var c2ZSrv1k c2Pool

// c2ZServe1k is c2ZServe under a 1 KiB window limit (its own running
// batch program).
func c2ZServe1k(t *testing.T, recs []czRec) []czRes {
	return c2ZSrv1k.ask(t, "zstd program, limit 1024", func() *exec.Cmd {
		return exec.Command(c2ZExe(t), "1000", "4096", "1024")
	}, recs)
}

// FuzzCompress_ZstdWrap: FuzzCompress_Zstd's properties
// under a 1 KiB window limit, where the window is 257 KiB and wraps (and a
// NewLazy decoder's is the frame's window plus two blocks, 3 KiB for a 1
// KiB window, which wraps every third block): no panic, no hang,
// every verdict equal to Read's (c2ZFuzzRecs), and Read's verdict
// Go's but for c2ZstdDiffers' cases (TooLarge only where a frame header
// declares a window over 1 KiB). Seeded with the small-window wrap frames
// and the 1 KiB-window corpus frames of up to 25,000 bytes.
func FuzzCompress_ZstdWrap(f *testing.F) {
	frames, _, _ := c2ZSmallWrapCases()
	for i, fr := range frames {
		if i%5 == 0 {
			f.Add(fr)
		}
	}
	if ents, fs := c2ZManifest(f); len(ents) > 0 {
		for i, e := range ents {
			if strings.HasSuffix(e.name, "-l1w10.zst") && len(fs[i]) <= 25000 && e.n >= 5000 {
				f.Add(fs[i])
			}
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		got := c2ZServe1k(t, c2ZFuzzRecs(data))
		c2ZFuzzSame(t, data, got)
		g := c2ZstdGoRes(data)
		if !c2ZSame(got[0], g) && c2ZstdDiffers(data, got[0], g, 1024) == "" {
			t.Errorf("(% x): got %v, Go %v", data[:min(len(data), 64)], got[0], g)
		}
	})
}
