// Why: Adler-32's vector form (32-byte std.simd blocks, a periodic reduction, a SWAR and byte tail)
// must equal the scalar checksum from any starting state and any alignment.
// What: AdlerUpdate and compress.Adler32State against Go's hash/adler32, with data cut at every offset.
// How: the second call starts from a state that is not 1 and from every alignment of the 32-byte blocks,
// the 8-byte words and the reduction block.

package std

import (
	"bytes"
	"fmt"
	"hash/adler32"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// c2AdlerShim is a test-only function in a std copy's std.compress:
// AdlerUpdate over data cut in two at c.
const c2AdlerShim = `// Test shim (std-compress2, CMP2-12): AdlerUpdate in two pieces.
export AdlerSplit(data: []u8, c: int): u32 {
    return AdlerUpdate(AdlerUpdate(1, data[0..c]), data[c..data.Len()])
}
`

// c2AdlerProg prints AdlerSplit of the file args[1] at every cut from 0
// to its length in steps of args[2], one hex value a line.
const c2AdlerProg = `import std.compress
import std.fs

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError!void {
    data := try fs.ReadBytes(sys.arena, sys.io, sys.args[1])
    step := Cast[int](try sys.args[2].ParseInt())
    var out := sys.Stdout()
    var c := 0
    for c <= data.Len() {
        s := compress.AdlerSplit(data, c)
        try Print(mut out, "{s:x}\n", (s: s))
        c += step
    }
    try out.Flush()
}
`

// TestCompress_AdlerSplitMatchesGo: AdlerUpdate in two pieces,
// cut at every offset of 2,000 random bytes and of 600 0xFF bytes, and at
// every 97th offset of 0xFF and random inputs that cross the reduction
// block (32 KiB) three times, equals hash/adler32 over the whole.
func TestCompress_AdlerSplitMatchesGo(t *testing.T) {
	exe := c2PieceExe(t, "adler", c2AdlerProg, map[string]string{"testadler.mk": c2AdlerShim})
	dir := t.TempDir()
	for _, c := range []struct {
		name string
		data []byte
		step int
	}{
		{"random 2000", czInput(0, 2000, 41), 1},
		{"ff 600", bytes.Repeat([]byte{0xFF}, 600), 1},
		{"ff 3 blocks", bytes.Repeat([]byte{0xFF}, 3*32768+77), 97},
		{"random 3 blocks", czInput(0, 3*32768+45, 42), 97},
	} {
		p := filepath.Join(dir, "in.bin")
		if err := os.WriteFile(p, c.data, 0o644); err != nil {
			t.Fatal(err)
		}
		got := runNative(t, exe, "", p, fmt.Sprint(c.step))
		if got.code != 0 {
			t.Fatalf("%s: exit %d: %s", c.name, got.code, got.stderr)
		}
		want := adler32.Checksum(c.data)
		lines := strings.Fields(got.stdout)
		if n := len(c.data)/c.step + 1; len(lines) != n {
			t.Fatalf("%s: %d answers, want %d", c.name, len(lines), n)
		}
		bad := 0
		for i, l := range lines {
			v, err := strconv.ParseUint(l, 16, 32)
			if err != nil || uint32(v) != want {
				if bad++; bad <= 5 {
					t.Errorf("%s, cut at %d: got %s, want %x", c.name, i*c.step, l, want)
				}
			}
		}
	}
}

// c2AdlerStateProg prints, for the file args[1], one line each:
// "whole" and compress.Adler32 of the data; "zero" and a zero-filled
// Adler32State's Sum before and after it absorbs the data; "cut", the
// offset, and a state's Sum after data[0..c] and then after the rest, at
// every cut from 0 to the length in steps of args[2]; and "piece", k, a
// state's Sum after the data in pieces of k bytes, each piece followed by
// an empty Bytes and a Sum the state keeps absorbing after, and the xor
// of those Sums.
const c2AdlerStateProg = `import std.compress
import std.fs

const Ks: [12]int = [1, 7, 8, 9, 31, 32, 33, 1000, 5552, 32768, 32769, 100000]

Pieces(data: []u8, k: int): (sum: u32, mids: u32) {
    var s := compress.Adler32State.New()
    var sums: u32 = 0
    var i := 0
    for i < data.Len() {
        e := if i + k > data.Len() { data.Len() } else { i + k }
        s.Bytes(data[i..e])
        s.Bytes(data[e..e])
        sums = sums ^ s.Sum()
        i = e
    }
    return (sum: s.Sum(), mids: sums)
}

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError!void {
    data := try fs.ReadBytes(sys.arena, sys.io, sys.args[1])
    step := Cast[int](try sys.args[2].ParseInt())
    var out := sys.Stdout()
    try Print(mut out, "whole {v:x}\n", (v: compress.Adler32(data)))
    zs := try sys.arena.Slice[compress.Adler32State](1)
    z0 := zs[0].Sum()
    zs[0].Bytes(data)
    try Print(mut out, "zero {a:x} {b:x}\n", (a: z0, b: zs[0].Sum()))
    var c := 0
    for c <= data.Len() {
        var s := compress.Adler32State.New()
        s.Bytes(data[0..c])
        p := s.Sum()
        s.Bytes(data[c..data.Len()])
        try Print(mut out, "cut {c} {p:x} {w:x}\n", (c: c, p: p, w: s.Sum()))
        c += step
    }
    for j in 0..12 {
        r := Pieces(data, Ks[j])
        try Print(mut out, "piece {k} {v:x} {m:x}\n", (k: Ks[j], v: r.sum, m: r.mids))
    }
    try out.Flush()
}
`

// TestCompress_Adler32StateMatchesGo: compress.Adler32State
// equals hash/adler32 and the one-shot compress.Adler32 over
// the same bytes, whatever the split: cut in two at every offset (or every
// 97th for inputs past the 32 KiB reduction block), the Sum between the
// pieces equal to the prefix's checksum; in pieces of 1 to 100,000 bytes,
// across the 8-byte word, the 32-byte block and the reduction block, with
// an empty Bytes and a Sum after each piece. A zero-filled state is the
// empty message's (Sum 1) and absorbs like a new one. The inputs include
// one whose checksum is 0 (63,473 zero bytes, 256 of 0xFF and one of 240)
// and the empty one. Built over a std copy, so MK_C2_PATCH can plant a
// fault in zlib.mk.
func TestCompress_Adler32StateMatchesGo(t *testing.T) {
	exe := c2PieceExe(t, "adlerstate", c2AdlerStateProg, nil)
	zero := append(make([]byte, 63473), bytes.Repeat([]byte{0xFF}, 256)...)
	zero = append(zero, 240)
	if adler32.Checksum(zero) != 0 {
		t.Fatalf("the zero input's checksum is %x, want 0", adler32.Checksum(zero))
	}
	dir := t.TempDir()
	for _, c := range []struct {
		name string
		data []byte
		step int
	}{
		{"empty", nil, 1},
		{"abc", []byte("abc"), 1},
		{"random 2000", czInput(0, 2000, 43), 1},
		{"ff 600", bytes.Repeat([]byte{0xFF}, 600), 1},
		{"checksum 0", zero, 97},
		{"ff 3 blocks", bytes.Repeat([]byte{0xFF}, 3*32768+77), 97},
		{"random 3 blocks", czInput(0, 3*32768+45, 44), 97},
	} {
		p := filepath.Join(dir, "in.bin")
		if err := os.WriteFile(p, c.data, 0o644); err != nil {
			t.Fatal(err)
		}
		got := runNative(t, exe, "", p, fmt.Sprint(c.step))
		if got.code != 0 {
			t.Fatalf("%s: exit %d: %s", c.name, got.code, got.stderr)
		}
		want := adler32.Checksum(c.data)
		hex := func(s string) uint32 {
			v, err := strconv.ParseUint(s, 16, 32)
			if err != nil {
				t.Fatalf("%s: %q is not hex", c.name, s)
			}
			return uint32(v)
		}
		bad, cuts, pieces := 0, 0, 0
		fail := func(format string, args ...any) {
			if bad++; bad <= 5 {
				t.Errorf(c.name+": "+format, args...)
			}
		}
		for _, line := range strings.Split(strings.TrimSpace(got.stdout), "\n") {
			f := strings.Fields(line)
			switch {
			case len(f) == 2 && f[0] == "whole":
				if hex(f[1]) != want {
					fail("compress.Adler32 is %s, want %x", f[1], want)
				}
			case len(f) == 3 && f[0] == "zero":
				if hex(f[1]) != 1 || hex(f[2]) != want {
					fail("a zero-filled state sums to %s, then %s; want 1, then %x", f[1], f[2], want)
				}
			case len(f) == 4 && f[0] == "cut":
				cut, err := strconv.Atoi(f[1])
				if err != nil || cut < 0 || cut > len(c.data) {
					t.Fatalf("%s: bad line %q", c.name, line)
				}
				cuts++
				if pw := adler32.Checksum(c.data[:cut]); hex(f[2]) != pw {
					fail("cut at %d: Sum of the prefix is %s, want %x", cut, f[2], pw)
				}
				if hex(f[3]) != want {
					fail("cut at %d: Sum is %s, want %x", cut, f[3], want)
				}
			case len(f) == 4 && f[0] == "piece":
				k, err := strconv.Atoi(f[1])
				if err != nil || k <= 0 {
					t.Fatalf("%s: bad line %q", c.name, line)
				}
				pieces++
				if hex(f[2]) != want {
					fail("pieces of %d bytes: Sum is %s, want %x", k, f[2], want)
				}
				// The xor of the Sums after each piece: each is the
				// checksum of the prefix so far.
				var mids uint32
				h := adler32.New()
				for i := 0; i < len(c.data); i += k {
					h.Write(c.data[i:min(i+k, len(c.data))])
					mids ^= h.Sum32()
				}
				if hex(f[3]) != mids {
					fail("pieces of %d bytes: the Sums between pieces xor to %s, want %x", k, f[3], mids)
				}
			default:
				t.Fatalf("%s: bad line %q", c.name, line)
			}
		}
		if n := len(c.data)/c.step + 1; cuts != n || pieces != 12 {
			t.Fatalf("%s: %d cuts and %d piece sizes, want %d and 12", c.name, cuts, pieces, n)
		}
	}
}
