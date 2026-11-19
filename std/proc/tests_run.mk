// Why: Run must drain both pipes, honour its limit and reap every child on
// every exit path; SuperviseOn must keep its workers running.
// What: test blocks of proc.Run and of Supervise's SuperviseOn.
// How: a child is this test executable run again with the block's index and a
// mode; Supervise's children are its own re-exec (MK_INHERIT set).

import std.core
import std.raw
import std.sys
import std.task

// PtSame: a and b are the same bytes.
PtSame(a: str, b: str): bool {
    x := a.AsBytes()
    y := b.AsBytes()
    if x.Len() != y.Len() {
        return false
    }
    for i in 0..x.Len() {
        if x[i] != y[i] {
            return false
        }
    }
    return true
}

// PtInt parses a decimal argument (no sign).
PtInt(s: str): int {
    var n := 0
    for c in s.AsBytes() {
        n = n * 10 + int(c) - 48
    }
    return n
}

// PtDigits writes n in decimal into b: the text.
PtDigits(b: mut [24]u8, n: int): str from b {
    var d: [24]u8 = [0; 24]
    var k := 0
    var x := n
    for {
        d[k] = Trunc[u8](48 + x % 10)
        k += 1
        x = x / 10
        if x == 0 {
            break
        }
    }
    for i in 0..k {
        b[i] = d[k - 1 - i]
    }
    return str.From(b[0..k]).Must()
}

// PtByte is byte i of the test pattern.
PtByte(i: int): u8 {
    return Trunc[u8](i * 7 % 251)
}

// PtPattern fills b with the pattern from offset off.
PtPattern(b: mut []u8, off: int) {
    for i in 0..b.Len() {
        b[i] = PtByte(off + i)
    }
}

// PtIsPattern: b is the pattern from 0.
PtIsPattern(b: []u8): bool {
    for i in 0..b.Len() {
        if b[i] != PtByte(i) {
            return false
        }
    }
    return true
}

// PtWrite writes all of b to stream h of this process (1 stdout, 2
// stderr) through the runtime's write entry; false on a failure.
PtWrite(io: Io, w: mut Writer, b: []u8): bool {
    w.Bytes(b) else {
        return false
    }
    return true
}

// PtSplit writes o pattern bytes to out and e to err, in 4 KiB pieces taken
// in turn, then flushes both; a failure exits 90 or 91.
PtSplit(sys: &System, out: mut Writer, err: mut Writer, buf: mut [4096]u8, o: int, e: int) {
    var so := 0
    var se := 0
    for so < o || se < e {
        if so < o {
            var k := o - so
            if k > 4096 {
                k = 4096
            }
            PtPattern(mut buf[0..k], so)
            if !PtWrite(sys.io, mut *out, buf[0..k]) {
                Exit(90)
            }
            so += k
        }
        if se < e {
            var k := e - se
            if k > 4096 {
                k = 4096
            }
            PtPattern(mut buf[0..k], se)
            if !PtWrite(sys.io, mut *err, buf[0..k]) {
                Exit(91)
            }
            se += k
        }
    }
    out.Flush() else {
        Exit(90)
    }
    err.Flush() else {
        Exit(91)
    }
}

// PtCloseOutput closes this process's stdout and stderr handles (std.sys:
// the OS's own close), so a parent's readers of them reach the end.
PtCloseOutput() {
    _ = sys.Close(sys.StdHandle(1))
    _ = sys.Close(sys.StdHandle(2))
}

// PtChild is the child half of every Run block: args[2] is the mode, the
// rest its numbers. It never returns.
//   gen O E C   O pattern bytes to stdout and E to stderr, in 4 KiB
//               pieces taken in turn, then exit C
//   cat         stdin copied to stdout and to stderr, piece by piece
//   forever     the pattern to stdout until the parent kills it
//   sleep       sleep 60 s, then exit 3 (a parent that failed to kill)
//   noread      exit 0 at once, stdin unread
//   split O E   O pattern bytes to stdout and E to stderr, flushed, then
//               sleep 60 s with both streams open
//   grand       start a grandchild (nap 6) that inherits all three
//               streams, then write 1,000 bytes to stdout, flush, and
//               sleep 60 s without reading stdin
//   nap [S]     sleep S seconds (15 when absent), then exit 0 (the
//               grandchild)
//   closed MS   close stdout and stderr, then sleep MS ms and exit 4: the
//               parent's readers reach the end while the child runs on
PtChild(sys: &System): never {
    mode := sys.args[2]
    var out := sys.Stdout()
    var err := sys.Stderr()
    var buf: [4096]u8 = [0; 4096]
    if PtSame(mode, "gen") {
        o := PtInt(sys.args[3])
        e := PtInt(sys.args[4])
        PtSplit(sys, mut out, mut err, mut buf, o, e)
        Exit(PtInt(sys.args[5]))
    }
    if PtSame(mode, "cat") {
        var inp := sys.Stdin()
        for {
            n := inp.Read(mut buf[..]) else {
                Exit(92)
            }
            if n == 0 {
                break
            }
            if !PtWrite(sys.io, mut out, buf[0..n]) || !PtWrite(sys.io, mut err, buf[0..n]) {
                Exit(93)
            }
        }
        Exit(0)
    }
    if PtSame(mode, "forever") {
        PtPattern(mut buf[..], 0)
        for {
            if !PtWrite(sys.io, mut out, buf[..]) {
                Exit(94)
            }
        }
    }
    if PtSame(mode, "split") {
        o := PtInt(sys.args[3])
        e := PtInt(sys.args[4])
        PtSplit(sys, mut out, mut err, mut buf, o, e)
        sys.io.Sleep(core.DurationOf(60_000_000_000)) else {
            Exit(5)
        }
        Exit(3)
    }
    if PtSame(mode, "grand") {
        var g: [4]str = [sys.args[0], sys.args[1], "nap", "6"]
        var c := core.IoChild(stdio: [core.IoStdInherit, core.IoStdInherit, core.IoStdInherit], inherit: 0, pipes: [0; 3], pid: 0)
        r := core.IoSpawn(sys.io, g[..], mut c)
        if r.err != core.SysErr.None {
            Exit(95)
        }
        _ = core.IoClose(sys.io, Trunc[uint](r.value))
        PtSplit(sys, mut out, mut err, mut buf, 1000, 0)
        sys.io.Sleep(core.DurationOf(60_000_000_000)) else {
            Exit(5)
        }
        Exit(3)
    }
    if PtSame(mode, "nap") {
        secs: i64 = if sys.args.Len() > 3 { i64(PtInt(sys.args[3])) } else { 15 }
        sys.io.Sleep(core.DurationOf(secs * 1_000_000_000)) else {
            Exit(5)
        }
        Exit(0)
    }
    if PtSame(mode, "sleep") {
        sys.io.Sleep(core.DurationOf(60_000_000_000)) else {
            Exit(5)
        }
        Exit(3)
    }
    if PtSame(mode, "closed") {
        // The standard streams' handles closed for real, so the pipes'
        // write ends are gone; nothing is written to them after this.
        PtCloseOutput()
        sys.io.Sleep(core.DurationOf(i64(PtInt(sys.args[3])) * 1_000_000)) else {
            Exit(5)
        }
        Exit(4)
    }
    Exit(0)
}

// PtRun runs this program again in the mode given (up to four words): the
// result.
PtRun(sys: &System, mode: str, x: str, y: str, z: str, input: []u8, limit: int): Error!Output from sys {
    var a: [6]str = [sys.args[0], sys.args[1], mode, x, y, z]
    return try Run(sys.arena, sys.io, a[..], input, limit)
}

// PtGen is gen O E C with a limit: the result.
PtGen(sys: &System, o: int, e: int, c: int, limit: int): Error!Output from sys {
    var bo: [24]u8 = [0; 24]
    var be: [24]u8 = [0; 24]
    var bc: [24]u8 = [0; 24]
    return try PtRun(sys, "gen", PtDigits(mut bo, o), PtDigits(mut be, e), PtDigits(mut bc, c), b"", limit)
}

test "Run: exit codes 0, 7 and 255 with empty output" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    for c in [0, 7, 255] {
        r := PtGen(sys, 0, 0, c, 0).Must()
        Assert(r.code == c && r.stdout.Len() == 0 && r.stderr.Len() == 0)
    }
}

test "Run: stdout and stderr at 1 byte, 64 KiB and 4 MiB, drained at once" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    for n in [1, 16383, 16384, 16385, 65536, 4_194_304] {
        r := PtGen(sys, n, n, 0, 2 * n).Must()
        Assert(r.code == 0 && r.stdout.Len() == n && r.stderr.Len() == n)
        Assert(PtIsPattern(r.stdout) && PtIsPattern(r.stderr))
    }
    // One stream only, past the first buffer.
    r := PtGen(sys, 0, 300_000, 4, 300_000).Must()
    Assert(r.code == 4 && r.stdout.Len() == 0 && r.stderr.Len() == 300_000 && PtIsPattern(r.stderr))
}

test "Run: input of 1 byte, 64 KiB and 4 MiB fed while both outputs drain" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    for n in [1, 65536, 4_194_304] {
        var b := sys.arena.Slice[u8](n).Must()
        PtPattern(mut b[..], 0)
        r := PtRun(sys, "cat", "", "", "", b[..], 2 * n).Must()
        Assert(r.code == 0 && r.stdout.Len() == n && r.stderr.Len() == n)
        Assert(PtIsPattern(r.stdout) && PtIsPattern(r.stderr))
    }
}

test "Run: input to a child that exits without reading it is no error" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    var b := sys.arena.Slice[u8](1_048_576).Must()
    r := PtRun(sys, "noread", "", "", "", b[..], 10).Must()
    Assert(r.code == 0 && r.stdout.Len() == 0)
}

test "Run: a limit met exactly passes, one byte past it is TooLarge, both streams counted" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    Assert(PtGen(sys, 100, 0, 0, 100).Must().stdout.Len() == 100)
    Assert(PtGen(sys, 50, 50, 0, 100).Must().stderr.Len() == 50)
    var got := 0
    _ = PtGen(sys, 100, 0, 0, 99) else e {
        if PtName(e) == "TooLarge" {
            got += 1
        }
    }
    _ = PtGen(sys, 50, 50, 0, 99) else e {
        if PtName(e) == "TooLarge" {
            got += 1
        }
    }
    _ = PtGen(sys, 1, 0, 0, 0) else e {
        if PtName(e) == "TooLarge" {
            got += 1
        }
    }
    Assert(got == 3)
    Assert(PtGen(sys, 0, 0, 0, 0).Must().code == 0)
}

test "Run: a child that writes for ever is killed at the limit" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    for limit in [0, 10, 1_000_000] {
        var got := false
        _ = PtRun(sys, "forever", "", "", "", b"", limit) else e {
            if PtName(e) == "TooLarge" {
                got = true
            }
        }
        Assert(got)
    }
}

test "Run: a missing program, empty args and an empty args[0] are NotFound" (sys: &System) {
    var got := 0
    var a: [2]str = ["mk-no-such-program", "x"]
    _ = Run(sys.arena, sys.io, a[..], b"in", 10) else e {
        if PtName(e) == "NotFound" {
            got += 1
        }
    }
    var noargs: [0]str = []
    _ = Run(sys.arena, sys.io, noargs[..], b"", 10) else e {
        if PtName(e) == "NotFound" {
            got += 1
        }
    }
    var empty: [1]str = [""]
    _ = Run(sys.arena, sys.io, empty[..], b"", 10) else e {
        if PtName(e) == "NotFound" {
            got += 1
        }
    }
    Assert(got == 3)
}

// PtRunCancel runs a sleeping child; got: 1 Canceled, 2 anything else.
PtRunCancel(io: Io, heap: &Heap, exe: str, idx: str, got: &raw.Atomic[u32]) {
    var a: [3]str = [exe, idx, "sleep"]
    var ar := Arena.New(heap).Must()
    _ = Run(&ar, io, a[..], b"", 100) else e {
        if PtName(e) == "Canceled" {
            got.Store(1)
        }
    }
    if got.Load() == 0 {
        got.Store(2)
    }
    ar.Free()
}

test "Run: a cancel while the child sleeps returns Canceled at once and kills it" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    // Rounds 1 to 3 (12 cancels) must leave the handle count within 8 of
    // where the warm-up round 0 did: a child that was not reaped, or a
    // pipe left open, is a handle, 12 or more in all.
    var before := 0
    for round in 0..4 {
        if round == 1 {
            before = PtHandles()
        }
        for d in [0, 1_000_000, 50_000_000, 200_000_000] {
            g := raw.Atomic[u32].New(0)
            t0 := Now(sys.io)
            scope s := sys.io.Scope() {
                s.go PtRunCancel(sys.io, sys.heap, sys.args[0], sys.args[1], &g)
                sys.io.Sleep(core.DurationOf(i64(d))) else {
                    Assert(false)
                }
                s.Cancel()
            }
            // The child sleeps 60 s; a Run that waited for it would take that.
            Assert(g.Load() == 1 && Now(sys.io) - t0 < 10_000_000_000)
        }
    }
    PtNoLeak(sys, before, 8)
}

test "Run: 100 runs leave the handle count as it was" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    for round in 0..2 {
        before := PtHandles()
        var times := 2
        if round == 1 {
            times = 100
        }
        for _ in 0..times {
            var ar := Arena.New(sys.heap).Must()
            var a: [6]str = [sys.args[0], sys.args[1], "gen", "100", "100", "1"]
            r := Run(&ar, sys.io, a[..], b"x", 1000).Must()
            Assert(r.code == 1)
            ar.Free()
        }
        if round == 1 {
            // A pool thread started on the way is a handle: allow 2.
            Assert(PtHandles() <= before + 2)
        }
    }
}

// PtNoLeak prints how far the handle count moved from before (shown only
// when the block fails), then asserts it moved by at most slack.
PtNoLeak(sys: &System, before: int, slack: int) {
    d := PtHandles() - before
    var o := sys.Stdout()
    _ = Print(mut o, "handles moved by {d}, at most {slack}\n", (d, slack)) else {
    }
    _ = o.Flush() else {
    }
    Assert(d <= slack)
}

// PtHandles is the process's open handle count less the handles the runtime
// made on demand and keeps (task.KeptHandles: on Windows the carriers'
// threads and std.sys's timer slots), so a carrier started or a timer slot
// made inside a block's window does not count; the pool's threads still do.
// -1 when the count cannot be read. A carrier is counted in KeptHandles
// just before its thread starts, so during a start in flight this count
// reads one low: as
// std.task's PHandles does, the count is taken until two samples 2 ms
// apart agree (at most 20 tries). (The blocks' parameter is named sys, so
// the module is reached from here.)
PtHandles(): int {
    var last := PtHandlesNow()
    if last < 0 {
        return last
    }
    for _ in 0..20 {
        t0 := sys.Now()
        for sys.Now() - t0 < 2_000_000 {
            sys.ThreadYield()
        }
        n := PtHandlesNow()
        if n == last || n < 0 {
            return n
        }
        last = n
    }
    return last
}

// PtHandlesNow is one sample of PtHandles' count.
PtHandlesNow(): int {
    n := sys.HandleCount()
    if n < 0 {
        return n
    }
    return n - task.KeptHandles()
}

// PtName is the name of a proc.Error member.
PtName(e: Error): str {
    return match e {
        NotFound { "NotFound" }
        Denied { "Denied" }
        TooLarge { "TooLarge" }
        Os(code: _) { "Os" }
        Canceled { "Canceled" }
        OutOfMemory { "OutOfMemory" }
        Unsupported { "Unsupported" }
    }
}

// PtFailsIn: Run of this program in mode (two words) on io, with input
// and limit, fails with want within ms milliseconds.
PtFailsIn(sys: &System, io: Io, mode: str, x: str, y: str, input: []u8, limit: int, want: str, ms: i64): bool {
    var a: [6]str = [sys.args[0], sys.args[1], mode, x, y, "0"]
    t0 := Now(sys.io)
    var got := "ok"
    _ = Run(sys.arena, io, a[..], input, limit) else e {
        got = PtName(e)
    }
    return PtSame(got, want) && Now(sys.io) - t0 < ms * 1_000_000
}

// The child writes more than the limit across both streams, neither
// stream's first buffer fills, and it stays alive: the shared budget sees
// the limit passed and kills it at once. Under the old rounds the limit
// was checked only when a round ended, and Run waited out the child's
// 60 s.
test "Run: output past the limit across both streams kills a child that stays alive, at once" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    Assert(PtFailsIn(sys, sys.io, "split", "60", "60", b"", 100, "TooLarge", 5000))
    Assert(PtFailsIn(sys, sys.io, "split", "600000", "600000", b"", 1_048_576, "TooLarge", 10000))
    Assert(PtFailsIn(sys, sys.io, "split", "101", "0", b"", 100, "TooLarge", 5000))
    Assert(PtFailsIn(sys, sys.io, "split", "0", "1", b"", 0, "TooLarge", 5000))
}

// Run on a NoCancel Io: no reader is ever cancelled to grow a buffer, so
// output past the first segments still drains, and a limit still ends the
// child, by the kill alone.
test "Run: on a NoCancel Io it drains growing output, feeds input, and stops at the limit" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    q := sys.io.NoCancel()
    t0 := Now(sys.io)
    var a: [6]str = [sys.args[0], sys.args[1], "gen", "1000000", "0", "0"]
    r := Run(sys.arena, q, a[..], b"", 2_000_000).Must()
    Assert(r.code == 0 && r.stdout.Len() == 1_000_000 && r.stderr.Len() == 0 && PtIsPattern(r.stdout))
    var a2: [6]str = [sys.args[0], sys.args[1], "gen", "1000000", "1000000", "0"]
    r2 := Run(sys.arena, q, a2[..], b"", 2_000_000).Must()
    Assert(r2.stdout.Len() == 1_000_000 && r2.stderr.Len() == 1_000_000 && PtIsPattern(r2.stdout) && PtIsPattern(r2.stderr))
    var b := sys.arena.Slice[u8](1_048_576).Must()
    PtPattern(mut b[..], 0)
    var a3: [3]str = [sys.args[0], sys.args[1], "cat"]
    r3 := Run(sys.arena, q, a3[..], b[..], 2_097_152).Must()
    Assert(r3.stdout.Len() == 1_048_576 && r3.stderr.Len() == 1_048_576 && PtIsPattern(r3.stdout) && PtIsPattern(r3.stderr))
    Assert(Now(sys.io) - t0 < 20_000_000_000)
    Assert(PtFailsIn(sys, q, "split", "60", "60", b"", 100, "TooLarge", 5000))
    Assert(PtFailsIn(sys, q, "forever", "", "", b"", 1000, "TooLarge", 5000))
}

// A grandchild holds the child's stdin (and its output pipes) and never
// reads; the child writes past the limit. TooLarge cancels the feed and
// the readers, so Run returns at once instead of when the grandchild
// exits.
test "Run: TooLarge returns at once while a grandchild holds every pipe" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    t0 := Now(sys.io)
    var b := sys.arena.Slice[u8](4_194_304).Must()
    Assert(PtFailsIn(sys, sys.io, "grand", "", "", b[..], 10, "TooLarge", 5000))
    // The grandchild sleeps 6 s holding this executable: the block ends
    // after it, so no process of the test outlives the test.
    left := 7_000_000_000 - (Now(sys.io) - t0)
    if left > 0 {
        sys.io.Sleep(core.DurationOf(left)) else {}
    }
}

// Denied and Os from Run: a directory is no program
// (execve's EACCES, CreateProcessW's ERROR_ACCESS_DENIED), and a NUL in an
// argument is InvalidPath, so Os.
test "Run: a directory as the program is Denied, a NUL byte in an argument is Os" (sys: &System) {
    var d: [1]str = ["./"]
    var got := "ok"
    _ = Run(sys.arena, sys.io, d[..], b"", 10) else e {
        got = PtName(e)
    }
    Assert(PtSame(got, "Denied"))
    var nb: [3]u8 = [97, 0, 98]
    var z: [2]str = [sys.args[0], str.From(nb[..]).Must()]
    got = "ok"
    _ = Run(sys.arena, sys.io, z[..], b"", 10) else e {
        got = PtName(e)
    }
    Assert(PtSame(got, "Os"))
}

// PtRunCancelIn runs this program in mode with input; got: 1 Canceled, 2
// anything else.
PtRunCancelIn(io: Io, heap: &Heap, exe: str, idx: str, mode: str, input: []u8, got: &raw.Atomic[u32]) {
    var a: [3]str = [exe, idx, mode]
    var ar := Arena.New(heap).Must()
    _ = Run(&ar, io, a[..], input, 100) else e {
        if PtName(e) == "Canceled" {
            got.Store(1)
        }
    }
    if got.Load() == 0 {
        got.Store(2)
    }
    ar.Free()
}

// A cancel while the feed is blocked writing a full stdin pipe (the child
// sleeps and never reads 8 MiB): Canceled at once, the child killed and
// reaped, every handle closed.
test "Run: a cancel while the feed waits on a full stdin pipe returns Canceled at once, nothing left open" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    var b := sys.arena.Slice[u8](8_388_608).Must()
    // Round 0 warms the runtime up (the pool threads a spawn starts are
    // handles, and their number moves with load); rounds 1 to 3, 12
    // cancels, must leave the count within 8 of where round 0 did. A
    // cancel that leaked its child or a pipe would add 12 or more.
    var before := 0
    for round in 0..4 {
        if round == 1 {
            before = PtHandles()
        }
        for d in [0, 1_000_000, 50_000_000, 200_000_000] {
            g := raw.Atomic[u32].New(0)
            t0 := Now(sys.io)
            scope s := sys.io.Scope() {
                s.go PtRunCancelIn(sys.io, sys.heap, sys.args[0], sys.args[1], "sleep", b[..], &g)
                sys.io.Sleep(core.DurationOf(i64(d))) else {
                    Assert(false)
                }
                s.Cancel()
            }
            Assert(g.Load() == 1 && Now(sys.io) - t0 < 10_000_000_000)
        }
    }
    PtNoLeak(sys, before, 8)
}

// PtRunClosed runs this program in mode closed, sleeping 60 s after it
// closed its output; got: 1 Canceled, 2 anything else.
PtRunClosed(io: Io, heap: &Heap, exe: str, idx: str, got: &raw.Atomic[u32]) {
    var a: [4]str = [exe, idx, "closed", "60000"]
    var ar := Arena.New(heap).Must()
    _ = Run(&ar, io, a[..], b"", 100) else e {
        if PtName(e) == "Canceled" {
            got.Store(1)
        }
    }
    if got.Load() == 0 {
        got.Store(2)
    }
    ar.Free()
}

test "Run: a child that closes its output and runs on is waited for, and its code returned" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    t0 := Now(sys.io)
    r := PtRun(sys, "closed", "300", "", "", b"", 100).Must()
    Assert(r.code == 4 && r.stdout.Len() == 0 && r.stderr.Len() == 0)
    Assert(Now(sys.io) - t0 >= 300_000_000)
}

// Run's last await point: both readers are at the end of their pipes, so
// Run waits for the child's exit (IoProcWait), and the cancel lands there
// once the child has started and closed its output (the later delays);
// the earlier ones land in the readers. Canceled at once, the child killed
// and reaped, no handle left.
test "Run: a cancel while it waits for a child that closed its output returns Canceled at once and reaps it" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    // Round 0 warms up, as the feed block's does; rounds 1 to 3 (12
    // cancels) must leave the handle count within 8 of round 0's.
    var before := 0
    for round in 0..4 {
        if round == 1 {
            before = PtHandles()
        }
        for d in [0, 50_000_000, 200_000_000, 500_000_000] {
            g := raw.Atomic[u32].New(0)
            t0 := Now(sys.io)
            scope s := sys.io.Scope() {
                s.go PtRunClosed(sys.io, sys.heap, sys.args[0], sys.args[1], &g)
                sys.io.Sleep(core.DurationOf(i64(d))) else {
                    Assert(false)
                }
                s.Cancel()
            }
            // The child sleeps 60 s; a Run that waited for it would take that.
            Assert(g.Load() == 1 && Now(sys.io) - t0 < 10_000_000_000)
        }
    }
    PtNoLeak(sys, before, 8)
}

// PtPar runs gen 70000 70000 c 20 times, each in an arena of its own:
// bad counts the runs whose result is not exact.
PtPar(io: Io, heap: &Heap, exe: str, idx: str, c: int, bad: &raw.Atomic[u32]) {
    var cb: [24]u8 = [0; 24]
    code := PtDigits(mut cb, c)
    var a: [6]str = [exe, idx, "gen", "70000", "70000", code]
    for _ in 0..20 {
        var ar := Arena.New(heap).Must()
        var ok := false
        r := Run(&ar, io, a[..], b"", 140_000) else {
            _ = bad.Add(1)
            ar.Free()
            continue
        }
        ok = r.code == c && r.stdout.Len() == 70_000 && r.stderr.Len() == 70_000 && PtIsPattern(r.stdout) && PtIsPattern(r.stderr)
        if !ok {
            _ = bad.Add(1)
        }
        ar.Free()
    }
}

// 16 tasks run 20 children each at once: concurrent spawns, pipes that
// must not leak into a sibling's child, and every output exact to the
// byte.
test "Run: 16 tasks run 20 children each at once, every output and code exact" (sys: &System) {
    if sys.args.Len() > 2 {
        PtChild(sys)
    }
    bad := raw.Atomic[u32].New(0)
    t0 := Now(sys.io)
    // Round 0 warms the pool up (16 spawns at once start pool threads,
    // each a handle); round 1, 320 runs, must leave the count within 8 of
    // where round 0 did.
    var before := 0
    for round in 0..2 {
        if round == 1 {
            before = PtHandles()
        }
        scope s := sys.io.Scope() {
            for i in 0..16 {
                s.go PtPar(sys.io, sys.heap, sys.args[0], sys.args[1], i + 1, &bad)
            }
        }
    }
    Assert(bad.Load() == 0 && Now(sys.io) - t0 < 100_000_000_000)
    PtNoLeak(sys, before, 8)
}

// The Linux runner's two variants really run on two backends: MK_IO=auto
// must have chosen io_uring and MK_IO=epoll epoll, else "the epoll
// fallback gives io_uring's results" proves nothing.
// Windows (no MK_IO) is IOCP.
test "Run: MK_IO=auto runs on io_uring and MK_IO=epoll on epoll" (sys: &System) {
    m := sys.Env("MK_IO") else { "" }
    b := task.PollBackend()
    if PtSame(m, "auto") {
        Assert(b == 2)
    } else if PtSame(m, "epoll") {
        Assert(b == 3)
    } else {
        Assert(b >= 1 && b <= 3)
    }
}
