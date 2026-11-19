// Why: the scheduler has cost budgets (spawn, switch, yield, heap, idle
// memory per task) that need repeatable measurements.
// What: the RT-Bench-* blocks and their helpers BenchLine, BenchNow,
// BenchWakes and BenchHeap.
// How: run by std/rt-sched_harness_test.go's TestRtSched_Bench under
// RTS_BENCH=1 (kind 1 blocks; they also pass as ordinary tests). Each
// prints one line "<name> <n> <total ns> <ns per op>" on standard output;
// a recorded number names the machine and the command beside it.

import std.core
import std.raw
import std.rt
import std.sys

// BenchLine prints one result line.
BenchLine(out: mut Writer, name: str, n: int, ns: i64) {
    per := ns / Trunc[i64](n)
    Print(out, "{name} {n} {ns} {per}\n", (name, n, ns, per)) else {
        return
    }
}

// BenchNow is the monotonic clock, for blocks whose parameter shadows sys.
BenchNow(): i64 {
    return sys.Now()
}

// BenchWakes is WakeOne's PollWake count so far (a test counter).
BenchWakes(): u64 {
    unsafe "the TaskState page Start mapped, which lives until exit" {
        // Edge: none; a test counter.
        return (*TS()).wakes_posted.Load()
    }
}

BenchEmpty(io: Io) {
}

test "RT-Bench-Spawn 100,000 spawns of an empty task, 1,000 per scope" (sys: &System) {
    t0 := BenchNow()
    for _ in 0..100 {
        scope s := sys.io.Scope() {
            for _ in 0..1_000 {
                s.go BenchEmpty(sys.io)
            }
        }
    }
    ns := BenchNow() - t0
    var out := sys.Stdout()
    BenchLine(mut out, "spawn", 100_000, ns)
    out.Flush() else {
        return
    }
}

test "RT-Bench-PingPong 200,000 round trips of a turn word between two tasks" (sys: &System) {
    turn := raw.Atomic[u32].New(0)
    w0 := BenchWakes()
    t0 := BenchNow()
    scope s := sys.io.Scope() {
        s.go MnPing(sys.io, &turn, 0, 200_000)
        s.go MnPing(sys.io, &turn, 1, 200_000)
    }
    ns := BenchNow() - t0
    var out := sys.Stdout()
    // One round trip is two hand-offs, each a park and a resume: four
    // switches (the budget's time / (4 x round trips)).
    BenchLine(mut out, "pingpong-roundtrip", 200_000, ns)
    BenchLine(mut out, "pingpong-switch", 800_000, ns)
    // WakeOne's PollWakes during the run (Linger).
    BenchLine(mut out, "pingpong-wakes", 1, Trunc[i64](BenchWakes() - w0))
    out.Flush() else {
        return
    }
}

test "RT-Bench-Yield 1,000,000 yields of one task" (sys: &System) {
    t0 := BenchNow()
    for _ in 0..1_000_000 {
        sys.io.Yield() else {
            return
        }
    }
    ns := BenchNow() - t0
    var out := sys.Stdout()
    BenchLine(mut out, "yield", 1_000_000, ns)
    out.Flush() else {
        return
    }
}

// BenchHeap allocates and frees n 64-byte blocks, keeping 16 live, through
// the runtime heap; bad counts blocks that came back unusable.
BenchHeap(io: Io, n: int, bad: &raw.Atomic[u32]) {
    var live: [16]*u8 = [raw.Null[u8](); 16]
    for k in 0..n {
        i := k & 15
        if !raw.IsNull(live[i]) {
            rt.HeapFree(live[i], 64, 16)
        }
        p := rt.HeapAlloc(64, 16)
        if raw.IsNull(p) {
            _ = bad.Add(1)
        } else {
            unsafe "a fresh 64-byte block" {
                raw.Fill(p, Trunc[u8](k), 64)
            }
        }
        live[i] = p
    }
    for i in 0..16 {
        if !raw.IsNull(live[i]) {
            rt.HeapFree(live[i], 64, 16)
        }
    }
}

test "RT-Bench-Heap 8 tasks x 200,000 alloc and free of 64 bytes" (sys: &System) {
    // One heap under one lock today (std/rt/heap.mk); with 8 carriers the
    // tasks contend for it. Per-carrier caches are measured against
    // this line.
    bad := raw.Atomic[u32].New(0)
    t0 := BenchNow()
    scope s := sys.io.Scope() {
        for _ in 0..8 {
            s.go BenchHeap(sys.io, 200_000, &bad)
        }
    }
    ns := BenchNow() - t0
    Assert(bad.Load() == 0)
    var out := sys.Stdout()
    BenchLine(mut out, "heap-alloc-free", 1_600_000, ns)
    out.Flush() else {
        return
    }
}

// IdlePark parks on word until it is not 0 (one WaitWord, the park every
// Chan, Mutex and join reduces to); arrived counts the tasks that reached
// the wait, woken those that returned.
IdlePark(io: Io, word: &raw.Atomic[u32], arrived: &raw.Atomic[u32], woken: &raw.Atomic[u32]) {
    _ = arrived.Add(1)
    for word.Load() == 0 {
        unsafe "word outlives every task of the scope" {
            _ = WaitWord(raw.PtrCast[raw.Atomic[u32], u32](raw.Of(word)), 0, false)
        }
    }
    _ = woken.Add(1)
}

// IdleScan reads a /proc file (Linux) in 64 KiB pieces: which 0 is statm's
// second field (resident pages), 1 the number of lines (maps), 2 the first
// number (max_map_count); -1 when it cannot be read.
IdleScan(path: str, which: int): int {
    r := sys.OpenFile(path, 0, false)
    if r.err != core.SysErr.None {
        return -1
    }
    h := Trunc[uint](r.value)
    var buf: [65536]u8 = [0; 65536]
    var field := 0
    var num := 0
    var lines := 0
    var done := false
    for !done {
        n := sys.ReadSync(h, mut buf[..], -1)
        if n.err != core.SysErr.None || n.value <= 0 {
            break
        }
        for i in 0..int(n.value) {
            b := buf[i]
            if b == 10 {
                lines += 1
            }
            if which != 1 && !done {
                if b >= 48 && b <= 57 {
                    num = num * 10 + int(b) - 48
                } else {
                    if which == 2 || field == 1 {
                        done = true
                    } else {
                        field += 1
                        num = 0
                    }
                }
            }
        }
    }
    _ = sys.Close(h)
    return if which == 1 { lines } else { num }
}

// IdleMem is the resident bytes of the process (Linux), else -1.
IdleMem(): int {
    if Target.os != Os.Linux {
        return -1
    }
    p := IdleScan("/proc/self/statm", 0)
    return if p < 0 { -1 } else { p * 4096 }
}

// IdleOut prints one line and flushes it at once, so the harness can act on
// it while the process waits.
IdleOut(out: mut Writer, line: str) {
    Print(out, "{line}\n", (line,)) else {
        return
    }
    out.Flush() else {
        return
    }
}

test "RT-Bench-Idle n tasks parked at once, memory per task" (sys: &System) {
    // The task memory budget (at most 8 KiB per blocked task, x86_64
    // Linux) and the goal of 1M idle tasks. n is RTS_IDLE (default 1,000, so the block
    // is cheap as an ordinary test). On Linux each prepared slot is two VMAs;
    // a slot the OS refuses makes s.go wait for one to be freed,
    // which never happens while every task is parked, so n is capped
    // at what vm.max_map_count leaves, and the line says so.
    var n := 1_000
    var measured := false
    unsafe "the RtState page Init mapped, which lives until exit; init_sp is set once at start" {
        e := EnvInt((*rt.R()).init_sp, "RTS_IDLE")
        if e > 0 {
            n = e
            measured = true
        }
    }
    var out := sys.Stdout()
    var cap := n
    if Target.os == Os.Linux {
        limit := IdleScan("/proc/sys/vm/max_map_count", 2)
        maps := IdleScan("/proc/self/maps", 1)
        if limit > 0 && maps >= 0 {
            room := (limit - maps) / 2 - 512
            if room < cap {
                cap = room
            }
        }
        BenchLine(mut out, "idle-max_map_count", 1, Trunc[i64](limit))
        BenchLine(mut out, "idle-maps-before", 1, Trunc[i64](maps))
    }
    if cap < 1 {
        cap = 1
    }
    BenchLine(mut out, "idle-asked", 1, Trunc[i64](n))
    BenchLine(mut out, "idle-tasks", 1, Trunc[i64](cap))
    IdleOut(mut out, "idle-base-ready")
    // With RTS_IDLE set, a pause the Windows harness measures the process in
    // (Get-Process), here before any idle task exists.
    if measured && Target.os == Os.Windows {
        sys.io.Sleep(core.DurationOf(3_000_000_000)) else {
            return
        }
    }
    before := IdleMem()
    word := raw.Atomic[u32].New(0)
    arrived := raw.Atomic[u32].New(0)
    woken := raw.Atomic[u32].New(0)
    t0 := BenchNow()
    scope s := sys.io.Scope() {
        for _ in 0..cap {
            s.go IdlePark(sys.io, &word, &arrived, &woken)
        }
        // The body yields until every task reached its wait, then sleeps
        // 20 ms so the last ones have parked.
        for Trunc[int](arrived.Load()) < cap {
            sys.io.Yield() else {
                return
            }
        }
        sys.io.Sleep(core.DurationOf(20_000_000)) else {
            return
        }
        t1 := BenchNow()
        BenchLine(mut out, "idle-spawn-and-park", cap, t1 - t0)
        after := IdleMem()
        if before >= 0 && after >= 0 {
            BenchLine(mut out, "idle-rss-per-task", cap, Trunc[i64](after - before))
            BenchLine(mut out, "idle-rss-before", 1, Trunc[i64](before))
            BenchLine(mut out, "idle-rss-after", 1, Trunc[i64](after))
        }
        if Target.os == Os.Linux {
            BenchLine(mut out, "idle-maps-after", 1, Trunc[i64](IdleScan("/proc/self/maps", 1)))
        }
        IdleOut(mut out, "idle-parked-ready")
        if measured && Target.os == Os.Windows {
            sys.io.Sleep(core.DurationOf(3_000_000_000)) else {
                return
            }
        }
        t2 := BenchNow()
        word.Store(1)
        unsafe "word outlives every task of the scope" {
            WakeWord(raw.PtrCast[raw.Atomic[u32], u32](raw.Of(&word)), true)
        }
        BenchLine(mut out, "idle-wake-all", cap, BenchNow() - t2)
    }
    Assert(Trunc[int](woken.Load()) == cap)
    BenchLine(mut out, "idle-total", cap, BenchNow() - t0)
    out.Flush() else {
        return
    }
}
