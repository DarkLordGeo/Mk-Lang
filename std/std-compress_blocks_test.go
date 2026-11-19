// Why: std.compress's streaming decoders and Encoder must fail cleanly when cancelled or interrupted.
// What: c2SimBlocks, Mk cancel-safety test blocks under std.sim.
// How: written into a std copy as std/compress/tests_stream.mk and run by `mk test --std=std.compress`
// (dev at 1 and 16 carriers, release at 16); kept out of the live tree.

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
func TestCompress_MkTestBlocks(t *testing.T) {
	names := regexp.MustCompile(`(?m)^test "([^"]+)"`).FindAllStringSubmatch(c2SimBlocks, -1)
	if len(names) == 0 {
		t.Fatal("no test block in tests_stream.mk")
	}
	for _, c := range []struct {
		carriers int
		flags    []string
	}{{1, nil}, {16, nil}, {16, []string{"--release"}}} {
		t.Run(fmt.Sprintf("carriers=%d%s", c.carriers, strings.Join(c.flags, "")), func(t *testing.T) {
			root := t.TempDir()
			stdDir := czStdCopy(t, root)
			if err := os.WriteFile(filepath.Join(stdDir, "compress", "tests_stream.mk"), []byte(c2SimBlocks), 0o644); err != nil {
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

// c2SimBlocks is the Mk test file of the cancel-safety blocks.
const c2SimBlocks = `// Tests of the streaming decoders and the Encoder under std.sim, for
// the cancel-safety table (doc/std/core.md#cancel-safety): an Inflater's Reader
// whose source is interrupted is Failed (it fails again, and Cause names no
// compress.Error), Inflater.Read over a repeatable source is repeatable
// (called again it delivers what an uninterrupted run does), and an
// Encoder whose write fails fails every later Write, Flush and Finish and
// ends by Abandon. The source is a loopback stream of a Sim: its first
// part is sent at once and the rest by a peer task 2 ms in; a second task
// cancels the reader's scope at 1 ms, while the reader waits for the rest.

import std.core
import std.net
import std.sim

// SimText is the plain text the stream carries, compressed.
const SimText = "the quick brown fox jumps over the lazy dog; the lazy dog sleeps on. "

// SimPeer sends rest on srv after 2 ms of simulated time, then closes srv.
SimPeer(io: Io, srv: net.Stream, rest: []u8) {
    io.Sleep(core.DurationOf(2_000_000)) else {
        srv.Close()
        return
    }
    srv.WriteAll(io, rest) else {
    }
    srv.Close()
}

// SimCancel cancels s after 1 ms of simulated time.
SimCancel(s: &Scope, io: Io) {
    io.Sleep(core.DurationOf(1_000_000)) else {
        return
    }
    s.Cancel()
}

// SimOut is what a reader task saw: the bytes it decoded, the reads that
// failed under the cancelled scope (cuts) and after it (late), the reads
// after it that succeeded with data (resumed), and whether Cause was none.
struct SimOut {
    n: int
    sum: u32
    cuts: int
    late: int
    clean: bool
}

// SimReadAll reads d through its Reader over src while s may be cancelled,
// then once more after the scope, and records what it saw in o.
SimReadAll(io: Io, h: &Heap, c: net.Stream, reader: bool, o: &Mutex[SimOut]) {
    var ar := Arena.New(h) else {
        c.Close()
        return
    }
    defer ar.Free()
    var sb: [16]u8 = [0; 16]
    var src := c.Reader(io, mut sb[..])
    var d := Inflater.NewGzip(&ar)
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
            inner.go SimCancel(&inner, io)
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
            inner.go SimCancel(&inner, io)
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
    *g.Mut() = SimOut(n: n, sum: sum, cuts: cuts, late: late, clean: clean)
    g.Unlock()
}

// SimRun sends the gzip of SimText repeated reps times over a loopback
// stream of a Sim seeded seed, its first p bytes at once, and returns what
// a reader task (through Inflater.Reader when reader, else Read) saw.
SimRun(a: &Arena, h: &Heap, seed: u64, reps: int, p: int, reader: bool): OutOfMemory|net.Error!SimOut {
    var t: [2048]u8 = [0; 2048]
    for i in 0..reps {
        (mut t[i * SimText.Len()..(i + 1) * SimText.Len()]).CopyFrom(SimText.AsBytes())
    }
    plain := t[0..reps * SimText.Len()]
    gz := try Gzip(a, plain)
    var sm := try sim.Sim.New(a, seed)
    defer sm.Free()
    io := sm.Io()
    l := try net.Listen(io, "127.0.0.1:7541")
    defer l.Close()
    c := try net.Dial(io, "127.0.0.1:7541")
    srv := l.Accept(io) else e {
        c.Close()
        fail e
    }
    srv.WriteAll(io, gz[0..p]) else e {
        srv.Close()
        c.Close()
        fail e
    }
    out := Mutex[SimOut].New(SimOut(n: -1, sum: 0, cuts: -1, late: -1, clean: false))
    scope outer := io.Scope() {
        outer.go SimPeer(io, srv, gz[p..])
        outer.go SimReadAll(io, h, c, reader, &out)
    }
    o := out.Into()
    Assert(o.n <= plain.Len())
    if o.n == plain.Len() {
        Assert(o.sum == Adler32(plain))
    }
    return o
}

test "under std.sim: Inflater.Read over a stream is repeatable across a cancel while it waits" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    for reps in [1, 3, 20] {
        for p in [1, 9, 15, 30] {
            o := try SimRun(&ar, sys.heap, Cast[u64](40 + p), reps, p, false)
            Assert(o.cuts == 1)
            Assert(o.late == 0)
            Assert(o.n == reps * SimText.Len())
            Assert(o.clean)
            ar.Reset()
        }
    }
}

test "under std.sim: Inflater.Reader over an interrupted stream fails again, and Cause is none" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    for reps in [1, 3, 20] {
        for p in [1, 9, 15, 30] {
            o := try SimRun(&ar, sys.heap, Cast[u64](60 + p), reps, p, true)
            Assert(o.cuts == 1)
            Assert(o.late == 1)
            Assert(o.n < reps * SimText.Len())
            Assert(o.clean)
            ar.Reset()
        }
    }
}

test "Inflater.Reader over a corrupt stream fails, fails again, and Cause names the corrupt byte" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var sm := try sim.Sim.New(&ar, 7)
    defer sm.Free()
    io := sm.Io()
    l := try net.Listen(io, "127.0.0.1:7542")
    defer l.Close()
    c := try net.Dial(io, "127.0.0.1:7542")
    defer c.Close()
    srv := try l.Accept(io)
    // A zlib header whose check bits are wrong: Corrupt at byte 1.
    var bad: [2]u8 = [0x78, 0x9D]
    srv.WriteAll(io, bad[..]) else e {
        srv.Close()
        fail e
    }
    srv.Close()
    var sb: [16]u8 = [0; 16]
    var src := c.Reader(io, mut sb[..])
    var d := Inflater.NewZlib(&ar)
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
            Assert(o == 1)
        }
        _ {
            Assert(false)
        }
    }
}

test "under std.sim: an Encoder whose write failed fails every later call and ends by Abandon" (sys: &System) {
    for cap in [0, 64] {
        for k in 0..3 {
            var ar := try Arena.New(sys.heap)
            defer ar.Free()
            var sm := try sim.Sim.New(&ar, Cast[u64](80 + k))
            defer sm.Free()
            io := sm.Io()
            l := try net.Listen(io, "127.0.0.1:7543")
            defer l.Close()
            c := try net.Dial(io, "127.0.0.1:7543")
            defer c.Close()
            srv := try l.Accept(io)
            defer srv.Close()
            var wb: [64]u8 = [0; 64]
            var w := c.Writer(io, mut wb[0..cap])
            var e := try Encoder.New(&ar, Framing.Gzip, 6)
            var fails := 0
            for i in 0..3 {
                if i == k {
                    sm.FailNext(sim.Op.Write, sim.Fault.NoSpace)
                }
                e.Write(mut w, SimText.AsBytes()) else {
                    fails += 1
                }
                e.Flush(mut w) else {
                    fails += 1
                }
            }
            // The first failed call is the flush of piece k (a Write only
            // buffers so little); every later Write and Flush fails at once.
            Assert(fails == 2 * (3 - k) - 1)
            e.Finish(mut w) else {
                fails += 1
                e.Abandon()
            }
            Assert(fails == 2 * (3 - k))
        }
    }
}
`
