// Why: net.Conn serialises Read and WriteAll across tasks, and only many
// interleavings show a lock that does not.
// What: TestStdNet_SimSweep and TestStdNet_SimSweepIsLoadBearing, plus a
// stream under a Recorder.
// How: 200 Sim seeds, 16 tasks on one Conn over a yielding transport; with a
// lock removed from a copy of std/net/conn.mk the blocks must fail.
// Its tests are cancel-safety tests; tools/gate/safety.go lists each.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const net3SweepBlocks = `import std.core
import std.net
import std.raw
import std.sim
import std.time

// Tally is an in-memory transport: WriteAll adds data's length to the
// total, Read reports the total in 8 bytes. Each yields inside its
// critical part and counts a call that found another of its kind inside,
// which the Conn's locks must make impossible.
linear view struct Tally {
    total: Mutex[int]
    in_read: raw.Atomic[u32]
    in_write: raw.Atomic[u32]
    overlaps: &raw.Atomic[u32]
}

// Enter marks one call of a kind inside; a second is an overlap.
Enter(inside: &raw.Atomic[u32], overlaps: &raw.Atomic[u32]) {
    if !inside.Cas(0, 1) {
        _ = overlaps.Add(1)
    }
}

impl net.Transport for Tally {
    Read(&self, io: Io, buf: mut []u8): net.Error!int {
        Enter(&self.in_read, self.overlaps)
        defer self.in_read.Store(0)
        io.Yield() else {
            fail Canceled
        }
        g := try self.total.Lock(io)
        n := *g.Ref()
        g.Unlock()
        for i in 0..8 {
            buf[i] = Trunc[u8](n >> (8 * i))
        }
        return 8
    }
    WriteAll(&self, io: Io, data: []u8): net.Error!void {
        Enter(&self.in_write, self.overlaps)
        defer self.in_write.Store(0)
        io.Yield() else {
            fail Canceled
        }
        var g := try self.total.Lock(io)
        *g.Mut() += data.Len()
        g.Unlock()
    }
    Shutdown(&self, io: Io, how: net.Shutdown): net.Error!void {
    }
    PeerAddr(&self): ?net.Addr {
        return none
    }
    Close(&self, io: Io): net.Error!void {
    }
    Abandon(self) {
        Tally(total, in_read: _, in_write: _, overlaps: _) := self
        _ = total.Into()
    }
}

// Hammer writes five bytes and reads the total through c, 20 times.
Hammer(io: Io, c: &net.Conn, bad: &raw.Atomic[u32]) {
    var got: [8]u8 = [0; 8]
    for _ in 0..20 {
        c.WriteAll(io, "abcde".AsBytes()) else {
            _ = bad.Add(1)
            return
        }
        n := c.Read(io, mut got[..]) else {
            _ = bad.Add(1)
            return
        }
        if n != 8 {
            _ = bad.Add(1)
        }
    }
}

// Total reads c's total.
Total(c: &net.Conn, io: Io): net.Error!int {
    var buf: [8]u8 = [0; 8]
    n := try c.Read(io, mut buf[..])
    Assert(n == 8)
    var total := 0
    for i in 0..8 {
        total |= int(buf[i]) << (8 * i)
    }
    return total
}

// Sweep runs 16 Hammers over one Conn under a Sim seeded seed and returns
// the overlaps the transport saw.
Sweep(a: &Arena, h: &Heap, seed: u64): OutOfMemory|net.Error!u32 {
    var sm := try sim.Sim.New(a, seed)
    defer sm.Free()
    io := sm.Io()
    overlaps := raw.Atomic[u32].New(0)
    t := Tally(total: Mutex[int].New(0), in_read: raw.Atomic[u32].New(0), in_write: raw.Atomic[u32].New(0),
        overlaps: &overlaps)
    c := net.NewConn(h, t) else e {
        t.Abandon()
        fail e
    }
    onerr c.Abandon()
    bad := raw.Atomic[u32].New(0)
    scope s := io.Scope() {
        for _ in 0..16 {
            s.go Hammer(io, &c, &bad)
        }
    }
    Assert(bad.Load() == 0)
    Assert(try Total(&c, io) == 16 * 20 * 5)
    try c.Close(io)
    return overlaps.Load()
}

test "Under std.sim seeds 1 to 200, 16 tasks sharing a Conn never overlap two Reads or two WriteAlls" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    for seed in 1..201 {
        Assert(try Sweep(&ar, sys.heap, Cast[u64](seed)) == 0)
    }
}

// Slow is an in-memory transport whose WriteAll takes 5 ms of simulated
// time before it adds data's length to the total; Read reports the total.
// entered counts the WriteAlls that reached the transport.
linear view struct Slow {
    total: Mutex[int]
    entered: &raw.Atomic[u32]
}

impl net.Transport for Slow {
    Read(&self, io: Io, buf: mut []u8): net.Error!int {
        g := try self.total.Lock(io)
        n := *g.Ref()
        g.Unlock()
        for i in 0..8 {
            buf[i] = Trunc[u8](n >> (8 * i))
        }
        return 8
    }
    WriteAll(&self, io: Io, data: []u8): net.Error!void {
        _ = self.entered.Add(1)
        io.Sleep(core.DurationOf(5_000_000)) else {
            fail Canceled
        }
        var g := try self.total.Lock(io)
        *g.Mut() += data.Len()
        g.Unlock()
    }
    Shutdown(&self, io: Io, how: net.Shutdown): net.Error!void {
    }
    PeerAddr(&self): ?net.Addr {
        return none
    }
    Close(&self, io: Io): net.Error!void {
    }
    Abandon(self) {
        Slow(total, entered: _) := self
        _ = total.Into()
    }
}

// Holder writes five bytes through c, holding its write lock for 5 ms.
Holder(io: Io, c: &net.Conn, done: &raw.Atomic[u32]) {
    c.WriteAll(io, "abcde".AsBytes()) else {
        return
    }
    _ = done.Add(1)
}

// CancelAt cancels s after nap ns of simulated time.
CancelAt(s: &Scope, io: Io, nap: i64) {
    io.Sleep(core.DurationOf(nap)) else {
        return
    }
    s.Cancel()
}

// Waiter starts after Holder holds c's write lock, and writes three bytes
// in a scope a task cancels 1 ms later, while it waits for the lock: 1
// when that WriteAll returned Canceled, 2 for another error, 3 when it
// wrote.
Waiter(io: Io, c: &net.Conn, res: &raw.Atomic[u32]) {
    io.Sleep(core.DurationOf(500_000)) else {
        res.Store(4)
        return
    }
    scope s := io.Scope() {
        s.go CancelAt(&s, io, 1_000_000)
        c.WriteAll(io, "xyz".AsBytes()) else e {
            res.Store(match e {
                Canceled { 1 }
                _ { 2 }
            })
            return
        }
        res.Store(3)
    }
}

// LockRun is one seed of the lock case: the total Waiter's cancelled
// WriteAll left, which must be Holder's five bytes alone, then eight once
// Waiter's bytes are written again outside the cancelled scope.
LockRun(a: &Arena, h: &Heap, seed: u64): OutOfMemory|net.Error!void {
    var sm := try sim.Sim.New(a, seed)
    defer sm.Free()
    io := sm.Io()
    entered := raw.Atomic[u32].New(0)
    t := Slow(total: Mutex[int].New(0), entered: &entered)
    c := net.NewConn(h, t) else e {
        t.Abandon()
        fail e
    }
    onerr c.Abandon()
    done := raw.Atomic[u32].New(0)
    res := raw.Atomic[u32].New(0)
    scope s := io.Scope() {
        s.go Holder(io, &c, &done)
        s.go Waiter(io, &c, &res)
    }
    Assert(done.Load() == 1 && res.Load() == 1)
    Assert(try Total(&c, io) == 5)
    Assert(entered.Load() == 1)
    try c.WriteAll(io, "xyz".AsBytes())
    Assert(try Total(&c, io) == 8)
    Assert(entered.Load() == 2)
    try c.Close(io)
}

// StreamRun is one seed of the stream case: a Stream.WriteAll in a
// cancelled scope returns Canceled having written nothing (its prefix is
// empty), and the peer then reads only what came after.
StreamRun(a: &Arena, seed: u64): OutOfMemory|net.Error!void {
    var sm := try sim.Sim.New(a, seed)
    defer sm.Free()
    io := sm.Io()
    l := try net.Listen(io, "127.0.0.1:7533")
    defer l.Close()
    d := try net.Dial(io, "127.0.0.1:7533")
    defer d.Close()
    p := try l.Accept(io)
    defer p.Close()
    var why := 0
    scope s := io.Scope() {
        s.Cancel()
        d.WriteAll(io, "lost".AsBytes()) else e {
            why = match e {
                Canceled { 1 }
                _ { 2 }
            }
        }
    }
    Assert(why == 1)
    try d.WriteAll(io, "kept".AsBytes())

    var got: [8]u8 = [0; 8]
    var n := 0
    for n < 4 {
        k := try p.Read(io, mut got[n..])
        if k == 0 {
            break
        }
        n += k
    }
    Assert(n == 4 && got[0] == 0x6B && got[3] == 0x74)
}

test "under std.sim: a Conn.WriteAll cancelled while it waits for the write lock writes nothing, and a Stream.WriteAll in a cancelled scope writes an empty prefix" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    for seed in 1..51 {
        try LockRun(&ar, sys.heap, Cast[u64](seed))
        try StreamRun(&ar, Cast[u64](seed))
    }
}
// Zero reports whether a is the zero Addr.
Zero(a: net.Addr): bool {
    if a.port != 0 || a.v6 {
        return false
    }
    for i in 0..16 {
        if a.ip[i] != 0 {
            return false
        }
    }
    return true
}

// Foreign checks that s is another Io's stream: zero addresses and
// Unsupported options, with no system call behind them.
Foreign(s: &net.Stream) {
    Assert(Zero(s.PeerAddr()) && Zero(s.LocalAddr()))
    var why := 0
    s.SetNoDelay(true) else e {
        why = match e {
            net.Unsupported { 1 }
            _ { 2 }
        }
    }
    Assert(why == 1)
    why = 0
    s.SetKeepAlive(none) else e {
        why = match e {
            net.Unsupported { 1 }
            _ { 2 }
        }
    }
    Assert(why == 1)
}

// Script listens, dials and accepts on loopback through io, checks both
// streams are foreign, sends five bytes across and returns the port.
Script(io: Io): net.Error!int {
    l := try net.Listen(io, "127.0.0.1:0")
    defer l.Close()
    var b: [32]u8 = [0; 32]
    var w := core.Capped.New(mut b[..])
    var ww := w.Writer()
    Print(mut ww, "127.0.0.1:{p}", (p: l.Port())) else {
        Panic("an address longer than 32 bytes")
    }
    d := try net.Dial(io, str.From(w.Bytes()).Must())
    defer d.Close()
    s := try l.Accept(io)
    defer s.Close()
    Foreign(&d)
    Foreign(&s)
    try d.WriteAll(io, "hello".AsBytes())
    var got: [8]u8 = [0; 8]
    var n := 0
    for n < 5 {
        n += try s.Read(io, mut got[n..])
    }
    Assert(got[0] == 0x68 && got[4] == 0x6F)
    return l.Port()
}

test "Under a Recorder and its Replayer a stream is another Io's: zero addresses and Unsupported options, alike in both" (sys: &System) {
    var rec := try sim.Recorder.New(sys.io)
    defer rec.Free()
    p1 := try Script(rec.Io())
    var rp := try sim.Replayer.New(try rec.Log(sys.arena))
    defer rp.Free()
    p2 := try Script(rp.Io())
    Assert(p1 == p2 && p1 > 0)
    Assert(rp.Done() && !rp.Diverged())
}

// Bye is a transport over a simulated stream (its Conn, since a Stream
// is not Share) whose Close speaks to the peer, as a TLS close_notify
// does: it writes "bye" and waits for the peer's three-byte answer for at
// most limit ns of the Sim's time. Past the limit it is time.TimedOut; a
// cancel of the caller's scope ends it with the operation's own error
// (Canceled). Its Abandon releases the stream.
linear view struct Bye {
    s: net.Conn
    limit: i64
}

// Speak writes "bye" and reads the peer's three-byte answer whole.
Speak(s: &net.Conn, io: Io): net.Error!bool {
    try s.WriteAll(io, "bye".AsBytes())
    var got: [3]u8 = [0; 3]
    var n := 0
    for n < 3 {
        k := try s.Read(io, mut got[n..])
        if k == 0 {
            return false
        }
        n += k
    }
    return got[0] == 0x61 && got[1] == 0x63 && got[2] == 0x6B
}

impl net.Transport for Bye {
    Read(&self, io: Io, buf: mut []u8): net.Error!int {
        return try self.s.Read(io, buf)
    }
    WriteAll(&self, io: Io, data: []u8): net.Error!void {
        try self.s.WriteAll(io, data)
    }
    Shutdown(&self, io: Io, how: net.Shutdown): net.Error!void {
        try self.s.Shutdown(io, how)
    }
    PeerAddr(&self): ?net.Addr {
        return none
    }
    Close(&self, io: Io): net.Error!void {
        var why := 0
        var expired := false
        scope sc := io.Scope() {
            sc.CancelAfter(core.DurationOf(self.limit))
            ok := Speak(&self.s, io) else e {
                why = match e {
                    Canceled { 1 }
                    _ { 2 }
                }
                false
            }
            if !ok && why == 0 {
                why = 2
            }
            expired = sc.Expired()
        }
        if why == 0 {
            return
        }
        if expired {
            fail time.TimedOut
        }
        if why == 1 {
            fail Canceled
        }
        fail net.Unsupported
    }
    Abandon(self) {
        Bye(s, limit: _) := self
        s.Abandon()
    }
}

// ByeWhy is 1 for Canceled, 2 for time.TimedOut, 3 for anything else.
ByeWhy(e: net.Error): int {
    return match e {
        Canceled { 1 }
        time.TimedOut { 2 }
        _ { 3 }
    }
}

// Answer reads the peer's "bye" and writes "ack".
Answer(io: Io, p: &net.Conn, res: &raw.Atomic[u32]) {
    var got: [8]u8 = [0; 8]
    k := p.Read(io, mut got[..]) else {
        res.Store(2)
        return
    }
    if k == 0 {
        res.Store(3)
        return
    }
    p.WriteAll(io, "ack".AsBytes()) else {
        res.Store(4)
        return
    }
    res.Store(1)
}

// Idle keeps a task alive past the close limit. Without one, std.sim
// ends main's parked Read at once with Canceled when main is the only
// task, where a native read waits for the scope's deadline.
Idle(io: Io) {
    io.Sleep(core.DurationOf(50_000_000)) else {
    }
}

// ByeLate is the bounded half: c's Close masked from the cancel runs to
// its limit (TimedOut, the Sim's clock at least limit later, the Conn
// still the caller's and still writing), then closes once the peer
// answers.
ByeLate(io: Io, p: &net.Conn, c: net.Conn) {
    t0 := time.Now(io)
    scope w := io.Scope() {
        w.go Idle(io)
        c.Close(io) else e {
            Assert(ByeWhy(e) == 2)
            Assert(time.Now(io).Since(t0).Nanos() >= 20_000_000)
            c.WriteAll(io, "!".AsBytes()) else {
                c.Abandon()
                Panic("a Conn kept by a failed Close does not write")
            }
            res := raw.Atomic[u32].New(0)
            scope s := io.Scope() {
                s.go Answer(io, p, &res)
                c.Close(io) else {
                    c.Abandon()
                    Panic("a Close the peer answered failed")
                }
            }
            Assert(res.Load() == 1)
            return
        }
        Panic("a Close past its limit with no answer succeeded")
    }
}

// ByeRun is one seed: a Conn over Bye, closed first in a cancelled scope
// (Canceled, nothing written, the Conn kept), then past its limit, then
// with the peer answering.
ByeRun(a: &Arena, h: &Heap, seed: u64): OutOfMemory|net.Error!void {
    var sm := try sim.Sim.New(a, seed)
    defer sm.Free()
    io := sm.Io()
    l := try net.Listen(io, "127.0.0.1:7534")
    defer l.Close()
    d := try net.Dial(io, "127.0.0.1:7534")
    p := l.Accept(io) else e {
        d.Close()
        fail e
    }
    // Conns, which are Share, so Answer's task can take the peer's.
    pc := p.Conn()
    defer pc.Abandon()
    t := Bye(s: d.Conn(), limit: 20_000_000)
    c := net.NewConn(h, t) else e {
        t.Abandon()
        fail e
    }
    scope s := io.Scope() {
        s.Cancel()
        c.Close(io) else e {
            Assert(ByeWhy(e) == 1)
            ByeLate(io.NoCancel(), &pc, c)
            return
        }
        Panic("a Close under a cancelled scope succeeded")
    }
}

test "under std.sim: a Conn.Close over a transport that speaks to the peer is Canceled in a cancelled scope and TimedOut past its limit, keeps the Conn on each failure, and closes once the peer answers" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    for seed in 1..51 {
        try ByeRun(&ar, sys.heap, Cast[u64](seed))
    }
}

// Trickle is an in-memory transport whose Read takes 5 ms of simulated
// time and then hands out the next bytes of "abcdefgh", as many as buf
// holds; entered counts the Reads that reached it.
linear view struct Trickle {
    next: Mutex[int]
    entered: &raw.Atomic[u32]
}

impl net.Transport for Trickle {
    Read(&self, io: Io, buf: mut []u8): net.Error!int {
        _ = self.entered.Add(1)
        io.Sleep(core.DurationOf(5_000_000)) else {
            fail Canceled
        }
        var g := try self.next.Lock(io)
        defer g.Unlock()
        msg := "abcdefgh".AsBytes()
        at := *g.Ref()
        var k := 0
        for k < buf.Len() && at + k < msg.Len() {
            buf[k] = msg[at + k]
            k += 1
        }
        *g.Mut() = at + k
        return k
    }
    WriteAll(&self, io: Io, data: []u8): net.Error!void {
    }
    Shutdown(&self, io: Io, how: net.Shutdown): net.Error!void {
    }
    PeerAddr(&self): ?net.Addr {
        return none
    }
    Close(&self, io: Io): net.Error!void {
    }
    Abandon(self) {
        Trickle(next, entered: _) := self
        _ = next.Into()
    }
}

// Taker reads three bytes through c, holding its read lock for 5 ms: got
// is 1 when they are "abc".
Taker(io: Io, c: &net.Conn, got: &raw.Atomic[u32]) {
    var buf: [3]u8 = [0; 3]
    n := c.Read(io, mut buf[..]) else {
        return
    }
    if n == 3 && buf[0] == 0x61 && buf[2] == 0x63 {
        got.Store(1)
    }
}

// Queued starts after Taker holds c's read lock, and reads in a scope a
// task cancels 1 ms later, while it waits for the lock: 1 when that Read
// returned Canceled, 2 for another error, 3 when it read.
Queued(io: Io, c: &net.Conn, res: &raw.Atomic[u32]) {
    io.Sleep(core.DurationOf(500_000)) else {
        res.Store(4)
        return
    }
    var buf: [8]u8 = [0; 8]
    scope s := io.Scope() {
        s.go CancelAt(&s, io, 1_000_000)
        _ = c.Read(io, mut buf[..]) else e {
            res.Store(match e {
                Canceled { 1 }
                _ { 2 }
            })
            return
        }
        res.Store(3)
    }
}

// ReadRun is one seed of the read-lock case: Queued's Read, cancelled while
// it waits for the read lock, never reaches the transport, and the next
// Read gets exactly the bytes Taker left, as in a run with no cancel.
ReadRun(a: &Arena, h: &Heap, seed: u64): OutOfMemory|net.Error!void {
    var sm := try sim.Sim.New(a, seed)
    defer sm.Free()
    io := sm.Io()
    entered := raw.Atomic[u32].New(0)
    t := Trickle(next: Mutex[int].New(0), entered: &entered)
    c := net.NewConn(h, t) else e {
        t.Abandon()
        fail e
    }
    onerr c.Abandon()
    got := raw.Atomic[u32].New(0)
    res := raw.Atomic[u32].New(0)
    scope s := io.Scope() {
        s.go Taker(io, &c, &got)
        s.go Queued(io, &c, &res)
    }
    Assert(got.Load() == 1 && res.Load() == 1)
    Assert(entered.Load() == 1)
    var buf: [8]u8 = [0; 8]
    n := try c.Read(io, mut buf[..])
    Assert(n == 5 && buf[0] == 0x64 && buf[4] == 0x68)
    Assert(entered.Load() == 2)
    try c.Close(io)
}

test "under std.sim: a Conn.Read cancelled while it waits for the read lock reads nothing, and the next Read gets exactly the bytes the holder left" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    for seed in 1..51 {
        try ReadRun(&ar, sys.heap, Cast[u64](seed))
    }
}
`

// net3SweepBlock is the sweep block's name, for -run.
const net3SweepBlock = "Under std.sim seeds 1 to 200, 16 tasks sharing a Conn never overlap two Reads or two WriteAlls"

// net3LockBlock is the WriteAll cancel block's name: its waiter, cancelled
// while it waits for the write lock, must never reach the transport.
const net3LockBlock = "under std.sim: a Conn.WriteAll cancelled while it waits for the write lock writes nothing, and a Stream.WriteAll in a cancelled scope writes an empty prefix"

// net3WriteLock is Conn.WriteAll's lock, which the load-bearing test
// removes from a copy of std/net/conn.mk.
const net3WriteLock = "    g := try self.writes.Lock(io)\n    defer g.Unlock()\n    f := self.do_write\n"

// net3ReadLockBlock is the Read cancel block's name: its reader, cancelled while it
// waits for the read lock, must never reach the transport.
const net3ReadLockBlock = "under std.sim: a Conn.Read cancelled while it waits for the read lock reads nothing, and the next Read gets exactly the bytes the holder left"

// net3ReadLock is Conn.Read's lock, which the load-bearing test removes
// from a copy of std/net/conn.mk.
const net3ReadLock = "    g := try self.reads.Lock(io)\n    defer g.Unlock()\n    f := self.do_read\n"

// net3CloseBlock is the Close cancel block's name: a Close that drops the
// transport's failure must fail it.
const net3CloseBlock = "under std.sim: a Conn.Close over a transport that speaks to the peer is Canceled in a cancelled scope and TimedOut past its limit, keeps the Conn on each failure, and closes once the peer answers"

// net3CloseCall is Conn.Close's call of the transport's Close, which the
// load-bearing test makes ignore a failure.
const net3CloseCall = "    f := self.do_close\n    try f(&self, io)\n"

// net3SweepRun runs the fixture's blocks (only the one named run, when
// run is not empty) with conn.mk replaced by conn when conn is not empty.
func net3SweepRun(t *testing.T, conn, run string, carriers int, flags ...string) (string, int) {
	t.Helper()
	root := t.TempDir()
	fix := map[string]string{"netsweep/tests_sweep.mk": net3SweepBlocks}
	if conn != "" {
		fix["net/conn.mk"] = conn
	}
	stdDir := copyStd(t, root, fix)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	t.Setenv("MK_CARRIERS", fmt.Sprint(carriers))
	args := append([]string{"test", "--std=std.netsweep"}, flags...)
	if run != "" {
		args = append(args, "-run", run)
	}
	out, status := mkCmd(t, root, stdDir, args...)
	if strings.Contains(out, "no usable LLVM") {
		t.Skipf("needs the LLVM toolchain: %s", out)
	}
	return out, status
}

func TestStdNet_SimSweep(t *testing.T) {
	for _, c := range []struct {
		carriers int
		flags    []string
	}{{1, nil}, {16, nil}, {16, []string{"--release"}}} {
		where := fmt.Sprintf("carriers=%d%s", c.carriers, strings.Join(c.flags, ""))
		t.Run(where, func(t *testing.T) {
			out, status := net3SweepRun(t, "", "", c.carriers, c.flags...)
			if status != 0 || strings.Count(out, "PASS std.netsweep") != 5 {
				t.Fatalf("status %d\n%s", status, out)
			}
		})
	}
}

func TestStdNet_SimSweepIsLoadBearing(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("net", "conn.mk"))
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	mut := strings.Replace(src, net3WriteLock, "    f := self.do_write\n", 1)
	if mut == src {
		t.Fatal("conn.mk has no WriteAll lock to remove")
	}
	for _, block := range []string{net3SweepBlock, net3LockBlock} {
		out, _ := net3SweepRun(t, mut, block, 1)
		if !strings.Contains(out, fmt.Sprintf("FAIL std.netsweep %q", block)) {
			t.Errorf("%q passed with Conn.WriteAll's lock removed\n%s", block, out)
		}
	}
	// With Conn.Read's lock gone, the cancelled reader reaches
	// the transport (a second entry), so the read-lock block must fail.
	mut = strings.Replace(src, net3ReadLock, "    f := self.do_read\n", 1)
	if mut == src {
		t.Fatal("conn.mk has no Read lock to remove")
	}
	out, _ := net3SweepRun(t, mut, net3ReadLockBlock, 1)
	if !strings.Contains(out, fmt.Sprintf("FAIL std.netsweep %q", net3ReadLockBlock)) {
		t.Errorf("%q passed with Conn.Read's lock removed\n%s", net3ReadLockBlock, out)
	}
	// A Close that ignores the transport's failure releases the Conn
	// anyway: the Close block must see the cancelled Close succeed.
	mut = strings.Replace(src, net3CloseCall, "    f := self.do_close\n    f(&self, io) else {\n    }\n", 1)
	if mut == src {
		t.Fatal("conn.mk has no Close call to change")
	}
	out, _ = net3SweepRun(t, mut, net3CloseBlock, 1)
	if !strings.Contains(out, fmt.Sprintf("FAIL std.netsweep %q", net3CloseBlock)) {
		t.Errorf("%q passed with Conn.Close ignoring the transport's failure\n%s", net3CloseBlock, out)
	}
}
