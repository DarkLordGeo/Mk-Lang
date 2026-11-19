// Why: Conn.Read must be repeatable: a read cancelled while it waits may lose
// nothing the reader later reads.
// What: TestStdNet_SimCancel and TestStdNet_SimCancelIsLoadBearing.
// How: under a Sim, a reader, a peer and a canceller at every split and cancel
// time; the reader's bytes must equal the story, in fixture module std.netsim.
// Its tests are cancel-safety tests; tools/gate/safety.go lists each.

package std

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

const net3SimBlocks = `import std.core
import std.net
import std.sim

// Story is what the peer sends over a loopback stream under std.sim.
const Story = "first line\nsecond line is longer\nthird\n"

// Peer sends rest on srv after nap ns of simulated time, then closes srv.
Peer(io: Io, srv: net.Stream, rest: str, nap: i64) {
    io.Sleep(core.DurationOf(nap)) else {
        srv.Close()
        return
    }
    srv.WriteAll(io, rest.AsBytes()) else {
    }
    srv.Close()
}

// CancelAt cancels s after nap ns of simulated time.
CancelAt(s: &Scope, io: Io, nap: i64) {
    io.Sleep(core.DurationOf(nap)) else {
        return
    }
    s.Cancel()
}

struct Out {
    data: [64]u8
    n: int
    cuts: int
    late: int
}

// Step makes one read: a direct Conn.Read of want bytes (mode 0), or a
// Line through the Conn's Reader r (mode 1), copying what it gave to dst.
Step(c: &net.Conn, r: mut Reader, io: Io, a: &Arena, mode: int, want: int, dst: mut []u8): net.Error|ReadError|OutOfMemory!int {
    if mode == 0 {
        return try c.Read(io, mut dst[0..want])
    }
    line := try r.Line(a)
    s := line else {
        return 0
    }
    (mut dst[0..s.Len()]).CopyFrom(s.AsBytes())
    dst[s.Len()] = 0x0A
    return s.Len() + 1
}

// Reader reads c to its end, under a scope a task cancels at cancel_at
// (none when 0), then after it, and stores what it delivered in out.
ReadTask(io: Io, h: &Heap, c: net.Conn, mode: int, want: int, cancel_at: i64, out: &Mutex[Out]) {
    var ar := Arena.New(h) else {
        c.Abandon()
        return
    }
    var b: [24]u8 = [0; 24]
    var r := c.Reader(io, mut b[..])
    var got: [64]u8 = [0; 64]
    var n := 0
    var cuts := 0
    var late := 0
    var done := false
    scope inner := io.Scope() {
        if cancel_at > 0 {
            inner.go CancelAt(&inner, io, cancel_at)
        }
        for !done {
            k := Step(&c, mut r, io, &ar, mode, want, mut got[n..]) else {
                cuts += 1
                break
            }
            if k == 0 {
                done = true
            }
            n += k
        }
    }
    for !done {
        k := Step(&c, mut r, io, &ar, mode, want, mut got[n..]) else {
            late += 1
            break
        }
        if k == 0 {
            done = true
        }
        n += k
    }
    ar.Free()
    c.Abandon()
    var g := out.Lock(io) else {
        return
    }
    o := g.Mut()
    (mut o.data[0..n]).CopyFrom(got[0..n])
    o.n = n
    o.cuts = cuts
    o.late = late
    g.Unlock()
}

// Run sends Story over a loopback stream of a Sim seeded seed, its first p
// bytes at once and the rest from a peer task 2 ms in, and returns what a
// reader task delivered through the stream's Conn.
Run(a: &Arena, h: &Heap, seed: u64, p: int, mode: int, want: int, cancel_at: i64): OutOfMemory|net.Error!Out {
    var sm := try sim.Sim.New(a, seed)
    defer sm.Free()
    io := sm.Io()
    l := try net.Listen(io, "127.0.0.1:7532")
    defer l.Close()
    c := try net.Dial(io, "127.0.0.1:7532")
    srv := l.Accept(io) else e {
        c.Close()
        fail e
    }
    srv.WriteAll(io, Story[0..p].AsBytes()) else e {
        srv.Close()
        c.Close()
        fail e
    }
    out := Mutex[Out].New(Out(data: [0; 64], n: 0, cuts: -1, late: -1))
    scope outer := io.Scope() {
        outer.go Peer(io, srv, Story[p..], 2_000_000)
        outer.go ReadTask(io, h, c.Conn(), mode, want, cancel_at, &out)
    }
    return out.Into()
}

Same(b: []u8, s: str): bool {
    t := str.From(b) else {
        return false
    }
    return t == s
}

// Case checks mode at every split point, with no cancel (nothing is
// interrupted), with the cancel while the reader waits (1 ms: exactly
// once) and about when the peer's bytes arrive (at most once): the story
// arrives whole every time.
Case(a: &Arena, h: &Heap, mode: int, want: int): OutOfMemory|net.Error!void {
    for p in 0..Story.Len() + 1 {
        for cancel_at in [0, 1_000_000, 2_000_000, 2_100_000, 2_300_000, 2_600_000] {
            o := try Run(a, h, Cast[u64](800 + p), p, mode, want, i64(cancel_at))
            Assert(o.late == 0)
            if cancel_at == 0 {
                Assert(o.cuts == 0)
            } else if cancel_at == 1_000_000 {
                Assert(o.cuts == 1)
            } else {
                Assert(o.cuts <= 1)
            }
            Assert(Same(o.data[0..o.n], Story))
        }
    }
}

test "under std.sim: Conn.Read is repeatable across a cancel while it waits" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    try Case(&ar, sys.heap, 0, 3)
    try Case(&ar, sys.heap, 0, 16)
}

test "under std.sim: a Conn's buffered Reader.Line is repeatable across a cancel while it waits" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    try Case(&ar, sys.heap, 1, 0)
}
`

// net3SimRun runs the fixture's blocks at the given carrier count.
func net3SimRun(t *testing.T, blocks string, carriers int) (string, int) {
	t.Helper()
	root := t.TempDir()
	stdDir := copyStd(t, root, map[string]string{"netsim/tests_conn.mk": blocks})
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	t.Setenv("MK_CARRIERS", fmt.Sprint(carriers))
	out, status := mkCmd(t, root, stdDir, "test", "--std=std.netsim")
	if strings.Contains(out, "no usable LLVM") {
		t.Skipf("needs the LLVM toolchain: %s", out)
	}
	return out, status
}

func TestStdNet_SimCancel(t *testing.T) {
	for _, c := range []int{1, 16} {
		t.Run(fmt.Sprintf("carriers=%d", c), func(t *testing.T) {
			out, status := net3SimRun(t, net3SimBlocks, c)
			if status != 0 || strings.Count(out, "PASS std.netsim") != 2 {
				t.Fatalf("status %d\n%s", status, out)
			}
		})
	}
}

// TestStdNet_SimCancelIsLoadBearing: with the cancel removed, both blocks
// must fail (the 1 ms case asserts one interruption).
func TestStdNet_SimCancelIsLoadBearing(t *testing.T) {
	blocks := strings.Replace(net3SimBlocks, "    s.Cancel()\n", "", 1)
	if blocks == net3SimBlocks {
		t.Fatal("the fixture has no cancel to remove")
	}
	out, _ := net3SimRun(t, blocks, 1)
	if n := strings.Count(out, "FAIL std.netsim"); n != 2 {
		t.Fatalf("%d blocks failed without the cancel, want 2\n%s", n, out)
	}
}
