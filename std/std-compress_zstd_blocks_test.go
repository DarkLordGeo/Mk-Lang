// Why: ZstdDecoder's Read must be repeatable over a repeatable source, and its Reader must stay Failed after an interruption.
// What: c2ZstdSimBlocks, Mk cancel-safety test blocks of the zstd decoder under std.sim.
// How: written into a std copy as std/compress/tests_zstd.mk and run by `mk test --std=std.compress` (dev at 1 and
// 16 carriers, release at 16). Part of the cuttable zstd unit: delete with std/compress/zstd*.mk.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// It is a cancel-safety test; tools/gate/safety.go lists it.
func TestCompress_ZstdMkTestBlocks(t *testing.T) {
	names := regexp.MustCompile(`(?m)^test "([^"]+)"`).FindAllStringSubmatch(c2ZstdSimBlocks, -1)
	if len(names) == 0 {
		t.Fatal("no test block in tests_zstd.mk")
	}
	for _, c := range []struct {
		carriers int
		flags    []string
	}{{1, nil}, {16, nil}, {16, []string{"--release"}}} {
		t.Run(fmt.Sprintf("carriers=%d%s", c.carriers, strings.Join(c.flags, "")), func(t *testing.T) {
			root := t.TempDir()
			stdDir := czStdCopy(t, root)
			if err := os.WriteFile(filepath.Join(stdDir, "compress", "tests_zstd.mk"), []byte(c2ZstdSimBlocks), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
			t.Setenv("MK_CARRIERS", fmt.Sprint(c.carriers))
			out, status := mkCmd(t, root, stdDir, append([]string{"test", "--std=std.compress"}, c.flags...)...)
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			for _, m := range names {
				if !strings.Contains(out, fmt.Sprintf("PASS std.compress %q", m[1])) {
					t.Errorf("%q did not pass (status %d)", m[1], status)
				}
			}
			if t.Failed() || status != 0 {
				t.Logf("mk test output (status %d):\n%s", status, out)
			}
		})
	}
}

// c2ZstdSimBlocks is the Mk test file of the zstd decoder's cancel-safety blocks.
// The stream is a frame libzstd 1.5.7 wrote (level 3, checksum, content
// size) of ZsText 20 times (1,380 bytes): a compressed block whose sequences use the
// predefined tables. Its first p bytes are sent at once and the rest by a
// peer task 2 ms in; a second task cancels the reader's scope at 1 ms,
// while the decoder waits for the rest of the block.
const c2ZstdSimBlocks = `// Tests of ZstdDecoder under std.sim, for reference section 18's
// cancel-safety table (doc/std/core.md#cancel-safety): Read over a repeatable source is
// repeatable, and the Reader over an interrupted source is Failed (it
// fails again; Cause names no compress.Error).

import std.core
import std.net
import std.sim

const ZsText = "the quick brown fox jumps over the lazy dog; the lazy dog sleeps on. "

const ZsFrame: [81]u8 = [0x28, 0xB5, 0x2F, 0xFD, 0x64, 0x64, 0x04, 0x1D, 0x02, 0x00, 0x34, 0x03, 0x74, 0x68, 0x65, 0x20,
    0x71, 0x75, 0x69, 0x63, 0x6B, 0x20, 0x62, 0x72, 0x6F, 0x77, 0x6E, 0x20, 0x66, 0x6F, 0x78, 0x20, 0x6A, 0x75, 0x6D, 0x70,
    0x73, 0x20, 0x6F, 0x76, 0x65, 0x72, 0x20, 0x74, 0x68, 0x65, 0x20, 0x6C, 0x61, 0x7A, 0x79, 0x20, 0x64, 0x6F, 0x67, 0x3B,
    0x20, 0x73, 0x6C, 0x65, 0x65, 0x6E, 0x2E, 0x04, 0x00, 0x18, 0x21, 0xE1, 0x83, 0x9D, 0x82, 0x95, 0x0A, 0x34, 0xE3, 0x98,
    0x65, 0x5B, 0xA6, 0x49, 0x51]

// ZsPeer sends rest on srv after 2 ms of simulated time, then closes srv.
ZsPeer(io: Io, srv: net.Stream, rest: []u8) {
    io.Sleep(core.DurationOf(2_000_000)) else {
        srv.Close()
        return
    }
    srv.WriteAll(io, rest) else {
    }
    srv.Close()
}

// ZsCancel cancels s after 1 ms of simulated time.
ZsCancel(s: &Scope, io: Io) {
    io.Sleep(core.DurationOf(1_000_000)) else {
        return
    }
    s.Cancel()
}

// ZsOut is what a reader task saw: the bytes it decoded and their
// Adler-32, the reads that failed under the cancelled scope (cuts) and
// after it (late), and whether Cause was none.
struct ZsOut {
    n: int
    sum: u32
    cuts: int
    late: int
    clean: bool
}

// ZsReadAll reads a ZstdDecoder over c, through its Reader when reader,
// while s may be cancelled, then once more after the scope.
ZsReadAll(io: Io, h: &Heap, c: net.Stream, reader: bool, o: &Mutex[ZsOut]) {
    var ar := Arena.New(h) else {
        c.Close()
        return
    }
    defer ar.Free()
    var sb: [16]u8 = [0; 16]
    var src := c.Reader(io, mut sb[..])
    var d := ZstdDecoder.New(&ar, 65536)
    var rb: [64]u8 = [0; 64]
    var dst: [40]u8 = [0; 40]
    var n := 0
    var sum := Adler32(dst[0..0])
    var cuts := 0
    var late := 0
    var done := false
    if reader {
        var r := d.Reader(mut src, mut rb[..])
        scope inner := io.Scope() {
            inner.go ZsCancel(&inner, io)
            for !done {
                k := r.Read(mut dst[..]) else {
                    cuts += 1
                    break
                }
                done = k == 0
                sum = AdlerUpdate(sum, dst[0..k])
                n += k
            }
        }
        for !done {
            k := r.Read(mut dst[..]) else {
                late += 1
                break
            }
            done = k == 0
            sum = AdlerUpdate(sum, dst[0..k])
            n += k
        }
    } else {
        scope inner := io.Scope() {
            inner.go ZsCancel(&inner, io)
            for !done {
                k := d.Read(mut src, mut dst[..]) else {
                    cuts += 1
                    break
                }
                done = k == 0
                sum = AdlerUpdate(sum, dst[0..k])
                n += k
            }
        }
        for !done {
            k := d.Read(mut src, mut dst[..]) else {
                late += 1
                break
            }
            done = k == 0
            sum = AdlerUpdate(sum, dst[0..k])
            n += k
        }
    }
    clean := d.Cause().IsNone()
    c.Close()
    var g := o.Lock(io) else {
        return
    }
    *g.Mut() = ZsOut(n: n, sum: sum, cuts: cuts, late: late, clean: clean)
    g.Unlock()
}

// ZsRun sends ZsFrame over a loopback stream of a Sim seeded seed, its
// first p bytes at once, and returns what a reader task saw.
ZsRun(a: &Arena, h: &Heap, seed: u64, p: int, reader: bool): OutOfMemory|net.Error!ZsOut {
    var t: [1400]u8 = [0; 1400]
    for i in 0..20 {
        (mut t[i * ZsText.Len()..(i + 1) * ZsText.Len()]).CopyFrom(ZsText.AsBytes())
    }
    var sm := try sim.Sim.New(a, seed)
    defer sm.Free()
    io := sm.Io()
    l := try net.Listen(io, "127.0.0.1:7551")
    defer l.Close()
    c := try net.Dial(io, "127.0.0.1:7551")
    srv := l.Accept(io) else e {
        c.Close()
        fail e
    }
    srv.WriteAll(io, ZsFrame[0..p]) else e {
        srv.Close()
        c.Close()
        fail e
    }
    out := Mutex[ZsOut].New(ZsOut(n: -1, sum: 0, cuts: -1, late: -1, clean: false))
    frame := ZsFrame
    rest := frame[p..]
    scope outer := io.Scope() {
        outer.go ZsPeer(io, srv, rest)
        outer.go ZsReadAll(io, h, c, reader, &out)
    }
    o := out.Into()
    plain := t[0..20 * ZsText.Len()]
    Assert(o.n <= plain.Len())
    if o.n == plain.Len() {
        Assert(o.sum == Adler32(plain))
    }
    return o
}

test "under std.sim: ZstdDecoder.Read over a stream is repeatable across a cancel while it waits" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    for p in [1, 3, 4, 9, 30, 70, 80] {
        o := try ZsRun(&ar, sys.heap, Cast[u64](40 + p), p, false)
        Assert(o.cuts == 1)
        Assert(o.late == 0)
        Assert(o.n == 20 * ZsText.Len())
        Assert(o.clean)
        ar.Reset()
    }
}

test "under std.sim: ZstdDecoder.Reader over an interrupted stream fails again, and Cause is none" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    for p in [1, 3, 4, 9, 30, 70, 80] {
        o := try ZsRun(&ar, sys.heap, Cast[u64](60 + p), p, true)
        Assert(o.cuts == 1)
        Assert(o.late == 1)
        // From p = 77 the block is buffered whole and only the checksum
        // waits, so every byte is out before the cut.
        Assert(o.n == 0 || p >= 77)
        Assert(o.clean)
        ar.Reset()
    }
}

test "ZstdDecoder.Reader over a corrupt frame fails, fails again, and Cause names the descriptor" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var sm := try sim.Sim.New(&ar, 7)
    defer sm.Free()
    io := sm.Io()
    l := try net.Listen(io, "127.0.0.1:7552")
    defer l.Close()
    c := try net.Dial(io, "127.0.0.1:7552")
    defer c.Close()
    srv := try l.Accept(io)
    // A frame descriptor with its reserved bit set: Corrupt at byte 4.
    var bad: [6]u8 = [0x28, 0xB5, 0x2F, 0xFD, 0x28, 0x00]
    srv.WriteAll(io, bad[..]) else e {
        srv.Close()
        fail e
    }
    srv.Close()
    var sb: [16]u8 = [0; 16]
    var src := c.Reader(io, mut sb[..])
    var d := ZstdDecoder.New(&ar, 0)
    var rb: [16]u8 = [0; 16]
    var r := d.Reader(mut src, mut rb[..])
    var dst: [8]u8 = [0; 8]
    first := r.Read(mut dst[..]) else {
        -1
    }
    again := r.Read(mut dst[..]) else {
        -1
    }
    Assert(first == -1 && again == -1)
    e := d.Cause() else {
        Assert(false)
        return
    }
    match e {
        Corrupt(offset: o) {
            Assert(o == 4)
        }
        _ {
            Assert(false)
        }
    }
}
`
