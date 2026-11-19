// Why: ZstdDecoder.NewLazy sizes each frame's window at the frame header, where New takes window_limit + 256 KiB
// up front; both must decode the same.
// What: tests of NewLazy and ReaderLazy (Reader panics on a lazy decoder), and of windows that grow and are reused.
// How: stored frames of each setting. Part of the cuttable zstd unit: delete with std/compress/zstd*.mk and tests_zstd.mk.

package std

import (
	"bytes"
	"hash/crc32"
	"strings"
	"testing"
)

// c2ZWinMax is zstd.mk's ZWinMax, the largest window limit: a window of
// that size is more than any arena gives.
const c2ZWinMax = 4398046511104

// TestCompress_TinyFrameUnderHugeLimit: under the largest window
// limit, a frame of two bytes decodes through a NewLazy decoder's Read and
// ReaderLazy, since its window is taken at its own size, while New asks
// its arena for the limit's window at construction, so every read of it
// is OutOfMemory.
func TestCompress_TinyFrameUnderHugeLimit(t *testing.T) {
	exe := c2ZExe(t)
	frames := [][]byte{
		c2ZRaw([]byte{0x00, 0x00}, c2ZBlockHdr(true, 0, 2), []byte("hi")), // a 1 KiB window
		c2ZRaw([]byte{0x20, 0x02}, c2ZBlockHdr(true, 0, 2), []byte("hi")), // single segment, 2 bytes
		c2ZRaw([]byte{0x20, 0x00}),                                       // single segment, no block: truncated
		c2ZRaw([]byte{0x20, 0x00}, c2ZBlockHdr(true, 0, 0)),              // single segment, 0 bytes: no window
		c2ZRaw([]byte{0x00, 0xF8}, c2ZBlockHdr(true, 1, 5), []byte("z")), // a 2 TiB window, under the limit: the arena cannot give it
	}
	wants := []czRes{
		{verdict: "ok", n: 2, crc: crc32.ChecksumIEEE([]byte("hi"))},
		{verdict: "ok", n: 2, crc: crc32.ChecksumIEEE([]byte("hi"))},
		{verdict: "truncated"},
		{verdict: "ok", n: 0, crc: 0},
		{verdict: "oom"},
	}
	for _, form := range []byte{3, 4} {
		got := c2ZAsk(t, exe, frames, form, 3, 1<<20, 1<<20, c2ZWinMax)
		for i := range frames {
			if !c2Exact(got[i], wants[i]) {
				t.Errorf("frame %d, form %d: got %v, want %v", i, form, got[i], wants[i])
			}
		}
	}
	for _, form := range []byte{0, 1} {
		got := c2ZAsk(t, exe, frames[:2], form, 3, 1<<20, 1<<20, c2ZWinMax)
		for i := range got {
			if got[i].verdict != "oom" {
				t.Errorf("New, frame %d, form %d: got %v, want oom", i, form, got[i])
			}
		}
	}
}

// TestCompress_GrowingWindows: streams of the stored frames back to
// back, their windows growing and shrinking (1 KiB, 128 KiB, 1 MiB, the
// level's 8 MiB, then 1 KiB again), decode to the concatenation of Go's
// decoding of each frame, through New's Read and Reader and NewLazy's
// Read and ReaderLazy, whole and in pieces, under the default limit and
// the largest (New only under the default: the largest is more than an
// arena gives). A frame whose window does not fit the one held takes a
// larger one, and a later smaller frame reuses it.
func TestCompress_GrowingWindows(t *testing.T) {
	ents, stored := c2ZManifest(t)
	// The longest stored frame of each setting, so its blocks reach back
	// across its window.
	pick := func(sub string) []byte {
		best := -1
		for i, e := range ents {
			if strings.HasSuffix(e.name, "-"+sub+".zst") && (best < 0 || e.n > ents[best].n) {
				best = i
			}
		}
		if best < 0 {
			t.Fatalf("no stored frame of setting %q", sub)
		}
		t.Logf("%s: %s, %d bytes decoded", sub, ents[best].name, ents[best].n)
		return stored[best]
	}
	order := []string{"l1w10", "l9w17b", "l12w20h", "l19", "l1w10", "l3"}
	var parts [][]byte
	var want []byte
	for _, s := range order {
		f := pick(s)
		out, err := c2ZstdGo(f)
		if err != nil {
			t.Fatalf("Go's decoder on %s: %v", s, err)
		}
		parts = append(parts, f)
		want = append(want, out...)
	}
	stream := bytes.Join(parts, nil)
	rev := make([][]byte, len(parts))
	var wantRev []byte
	for i := range parts {
		rev[i] = parts[len(parts)-1-i]
	}
	for i := range order {
		out, _ := c2ZstdGo(rev[i])
		wantRev = append(wantRev, out...)
	}
	streams := [][]byte{stream, bytes.Join(rev, nil)}
	wants := []czRes{
		{verdict: "ok", n: len(want), crc: crc32.ChecksumIEEE(want)},
		{verdict: "ok", n: len(wantRev), crc: crc32.ChecksumIEEE(wantRev)},
	}
	exe := c2ZExe(t)
	for _, c := range []struct {
		form       byte
		maxp, maxo int
		wl         int
	}{
		{0, 1 << 20, 1 << 20, 0}, {1, 4093, 777, 0},
		{3, 1 << 20, 1 << 20, 0}, {4, 4093, 777, 0}, {3, 1 << 20, 1 << 20, c2ZWinMax}, {4, 997, 70000, c2ZWinMax},
	} {
		got := c2ZAsk(t, exe, streams, c.form, 11, c.maxp, c.maxo, c.wl)
		for i := range streams {
			if !c2Exact(got[i], wants[i]) {
				t.Errorf("stream %d (form %d, pieces %d/%d, limit %d): got %v, want %v", i, c.form, c.maxp, c.maxo, c.wl, got[i], wants[i])
			}
		}
	}
}

// c2ZLazyReaderProg calls ZstdDecoder.Reader on a NewLazy decoder.
const c2ZLazyReaderProg = `import std.compress

export Main(sys: &System): WriteError|OutOfMemory!void {
    var a := try Arena.New(sys.heap)
    defer a.Free()
    var z := compress.ZstdDecoder.NewLazy(&a, 0)
    var src := sys.Stdin()
    var rb: [16]u8 = [0; 16]
    var r := z.Reader(mut src, mut rb[..])
    try sys.Print("made a Reader\n")
    var d: [4]u8 = [0; 4]
    _ = r.Read(mut d[..]) else {
        return
    }
}
`

// TestCompress_ReaderOfLazyPanics: Reader, whose reads cannot take
// a window, panics with its message on a NewLazy decoder before it makes
// a Reader.
func TestCompress_ReaderOfLazyPanics(t *testing.T) {
	exe := c2Exe(t, "zlazyreader", c2ZLazyReaderProg)
	got := runNative(t, exe, "")
	const msg = "ZstdDecoder.Reader: a NewLazy decoder takes its windows in its reads, so its Reader is ReaderLazy"
	if got.code == 0 || strings.Contains(got.stdout, "made a Reader") || !strings.Contains(got.stderr, msg) {
		t.Errorf("got exit %d, stdout %q, stderr %q; want a panic with %q", got.code, got.stdout, got.stderr, msg)
	}
}
