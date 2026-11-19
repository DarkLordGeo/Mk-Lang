// Why: defects found in epoll waits, large semaphore posts and console reads
// each keep the case that failed before the fix.
// What: rtioProbe5, the Mk source of std/rtioprobe/probe5.mk.
// How: std/rt-io_waitedges_test.go writes it next to the earlier probe files,
// whose helpers it uses (NewPage, Sh, Spin, Reader, Echo, HugeWait, MaxI64);
// each case returns 0, or the number of the step that failed.

package std

// rtioProbe5 is std/rtioprobe/probe5.mk of the copy.
const rtioProbe5 = `// A probe of std.sys, fifth file (std/rt-io_probe5_test.go).

import std.core
import std.raw
import std.sys

// MaxWaitNs is the largest timeout epoll_pwait takes, in nanoseconds
// (0x7FFF_FFFF ms).
const MaxWaitNs: i64 = 2_147_483_647_000_000

// EpollMs: with test point 6 on, epoll waits by epoll_pwait (milliseconds),
// the path of every kernel before 5.11, where every Linux run takes epoll
// (io_uring needs 5.11). A wait within 1 ms of i64's largest value
// overflowed the rounding add (a panic); every huge timeout must wait
// until a wake, a 30 ms wait must last at least 30 ms, and the earlier cases
// that wait (Echo, Wake, CancelRecv, HugeWait) must pass on that path. On
// io_uring and IOCP the point does nothing and the same steps must pass.
export EpollMs(): u32 {
    _ = sys.SysTestHook(6, ParkCount, raw.Null[u8](), true)
    var code: u32 = 0
    var out: [8]*sys.Op = [raw.Null[sys.Op](); 8]
    var step: u32 = 0
    for t in [MaxI64, MaxI64 - 1, MaxI64 - 999_999, MaxWaitNs + 1, MaxWaitNs, MaxWaitNs - 1] {
        step += 1
        sys.PollWake()
        t0 := sys.Now()
        n := sys.PollWait(t, mut out[..])
        if n != 0 {
            code = step
            break
        }
        if sys.Now() - t0 > 1_000_000_000 {
            code = 10 + step
            break
        }
    }
    if code == 0 {
        t1 := sys.Now()
        n := sys.PollWait(30_000_000, mut out[..])
        d := sys.Now() - t1
        if n != 0 {
            code = 20
        } else if d < 30_000_000 {
            // 21, and the length in microseconds above it.
            code = 21 + 100 * Trunc[u32](d / 1_000)
        } else if d > 1_000_000_000 {
            code = 22
        }
    }
    if code == 0 {
        e := Echo()
        if e != 0 {
            code = 100 + e
        }
    }
    if code == 0 {
        w := Wake()
        if w != 0 {
            code = 200 + w
        }
    }
    if code == 0 {
        c := CancelRecv()
        if c != 0 {
            code = 300 + c
        }
    }
    if code == 0 {
        h := HugeWait()
        if h != 0 {
            code = 400 + h
        }
    }
    _ = sys.SysTestHook(6, ParkCount, raw.Null[u8](), false)
    return code
}

// SemBig: SemPost saturates at 2^31 - 1 on both targets. Windows passed a
// count of 2^31 or more to ReleaseSemaphore as a negative number and posted
// nothing; Linux's u32 Add wrapped (2^32 - 1, then 1, left 0).
export SemBig(): u32 {
    var code: u32 = 0
    p1 := NewPage()
    p2 := NewPage()
    if raw.IsNull(p1) || raw.IsNull(p2) {
        return 10
    }
    unsafe "the case's pages" {
        g1 := Sh(p1)
        if !sys.SemInit(mut (*g1).sem) {
            return 11
        }
        sys.SemPost(&(*g1).sem, 0x8000_0000)
        if !sys.SemWait(&(*g1).sem, 0) {
            code = 1
        } else if !sys.SemWait(&(*g1).sem, 0) {
            code = 2
        }
        g2 := Sh(p2)
        if !sys.SemInit(mut (*g2).sem) {
            return 12
        }
        sys.SemPost(&(*g2).sem, 0xFFFF_FFFF)
        sys.SemPost(&(*g2).sem, 1)
        if code == 0 && !sys.SemWait(&(*g2).sem, 0) {
            code = 3
        }
        // A post of 0 posts nothing.
        g3 := Sh(p1)
        if !sys.SemInit(mut (*g3).sem) {
            return 13
        }
        sys.SemPost(&(*g3).sem, 0)
        if code == 0 && sys.SemWait(&(*g3).sem, 0) {
            code = 4
        }
    }
    sys.Release(p1, 4096)
    sys.Release(p2, 4096)
    return code
}

// Typed types units into the console and reads until a '\n' arrives (or 64
// bytes), with reads of at most size bytes each, into got. code 0, or the
// step: 1 refused, 2 a read failed, 3 a read returned 0, 4 a read returned
// more than size; n the bytes read.
Typed(h: uint, units: []u16, size: int, got: mut [64]u8): (code: u32, n: int) {
    if sys.ConsoleTestType(units) != units.Len() {
        return (code: 1, n: 0)
    }
    var n := 0
    var b: [64]u8 = [0; 64]
    for n < 64 {
        r := sys.ReadSync(h, mut b[..size], -1)
        if r.err != core.SysErr.None {
            return (code: 2, n: n)
        }
        if r.value == 0 {
            return (code: 3, n: n)
        }
        if r.value > i64(size) {
            return (code: 4, n: n)
        }
        var last: u8 = 0
        for i in 0..Trunc[int](r.value) {
            if n < 64 {
                got[n] = b[i]
                n += 1
            }
            last = b[i]
        }
        if last == 10 {
            break
        }
    }
    return (code: 0, n: n)
}

// Same: got[..n] is exactly want.
Same(got: &[64]u8, n: int, want: []u8): bool {
    if n != want.Len() {
        return false
    }
    for i in 0..n {
        if got[i] != want[i] {
            return false
        }
    }
    return true
}

// ConStep types units, reads them back size bytes at a time and compares
// with want: 0, or base + Typed's step, or base when the bytes differ.
ConStep(h: uint, units: []u16, size: int, want: []u8, base: u32): u32 {
    var got: [64]u8 = [0; 64]
    t := Typed(h, units, size, mut got)
    if t.code != 0 {
        return base + t.code
    }
    if !Same(&got, t.n, want) {
        // 20 + the count read, and the first byte above it.
        return base + 20 + Trunc[u32](t.n) + 1_000 * u32(got[0])
    }
    return 0
}

// ConRead (not verified): a console read returns UTF-8.
// Windows only, in a console of the probe's own (the Go harness starts it
// with CREATE_NO_WINDOW), typed with ConsoleTestType: a BMP scalar and a
// surrogate pair; a 3-byte scalar read one byte at a time; an unpaired high
// surrogate then a 3-byte scalar read one and two bytes at a time (6 bytes,
// up to 5 left over for the carry: the first carry kept 3); a pair split
// across two reads by a 6-byte buffer; ThreadInterrupt of a blocked
// ReadOnce, which must be Canceled (the first version returned 0, end of
// input) and leave the console readable; Ctrl+Z, end of input. Linux: 0
// (no console path).
export ConRead(): u32 {
    if Target.os != Os.Windows {
        return 0
    }
    r := sys.OpenFile("CONIN$", 0, false)
    if !Ok(r) {
        return 90
    }
    h := Trunc[uint](r.value)
    if !sys.IsConsole(h) {
        _ = sys.Close(h)
        return 91
    }
    var u1: [4]u16 = [0xE9, 0xD83D, 0xDE00, 13]
    var w1: [8]u8 = [0xC3, 0xA9, 0xF0, 0x9F, 0x98, 0x80, 13, 10]
    var u2: [2]u16 = [0x20AC, 13]
    var w2: [5]u8 = [0xE2, 0x82, 0xAC, 13, 10]
    var u3: [3]u16 = [0xD800, 0x20AC, 13]
    var w3: [8]u8 = [0xEF, 0xBF, 0xBD, 0xE2, 0x82, 0xAC, 13, 10]
    var u5: [4]u16 = [0x61, 0xD83D, 0xDE00, 13]
    var w5: [7]u8 = [0x61, 0xF0, 0x9F, 0x98, 0x80, 13, 10]
    var u7: [2]u16 = [0x7A, 13]
    var w7: [3]u8 = [0x7A, 13, 10]
    var u6: [2]u16 = [0x79, 13]
    var w6: [3]u8 = [0x79, 13, 10]
    var u8z: [2]u16 = [0x1A, 13]
    var code := ConStep(h, u1[..], 64, w1[..], 100)
    if code == 0 {
        code = ConStep(h, u2[..], 1, w2[..], 200)
    }
    if code == 0 {
        code = ConStep(h, u3[..], 1, w3[..], 300)
    }
    if code == 0 {
        code = ConStep(h, u3[..], 2, w3[..], 400)
    }
    if code == 0 {
        code = ConStep(h, u5[..], 6, w5[..], 500)
    }
    if code == 0 {
        code = ConInterrupt(h)
    }
    // 7: the console still reads after the interrupt. Windows' console host
    // hands the next typed line to the cancelled ReadConsoleW: the units it
    // asked for are dropped (measured with a Go program: a cancelled 2-unit
    // read, then "z" Enter, reads only the line feed). So the first line
    // after the interrupt may arrive whole or as its last units; the next
    // line must arrive whole.
    if code == 0 {
        var got: [64]u8 = [0; 64]
        t := Typed(h, u7[..], 64, mut got)
        if t.code != 0 {
            code = 700 + t.code
        } else if !Same(&got, t.n, w7[..]) && !Same(&got, t.n, w7[2..]) {
            code = 720 + Trunc[u32](t.n)
        }
    }
    if code == 0 {
        code = ConStep(h, u6[..], 64, w6[..], 750)
    }
    if code == 0 {
        if sys.ConsoleTestType(u8z[..]) != 2 {
            code = 801
        } else {
            var b: [64]u8 = [0; 64]
            x := sys.ReadSync(h, mut b[..], -1)
            if x.err != core.SysErr.None || x.value != 0 {
                code = 800
            }
        }
    }
    _ = sys.Close(h)
    return code
}

// ConInterrupt: Reader blocks in ReadOnce on the console h; it must end
// Canceled within 2 s of the first ThreadInterrupt (re-sent while it has
// not returned: an interrupt before the call blocks interrupts nothing).
ConInterrupt(h: uint): u32 {
    page := NewPage()
    if raw.IsNull(page) {
        return 610
    }
    g := Sh(page)
    var code: u32 = 0
    unsafe "g is the case's page" {
        (*g).ops[0].Store(u64(h))
        t := sys.ThreadStart(Reader, page, 262144)
        if !Ok(t) {
            return 611
        }
        for (*g).flag.Load() == 0 {
            sys.ThreadYield()
        }
        Spin(50)
        deadline := sys.Now() + 2_000_000_000
        for (*g).exited.Load() == 0 {
            _ = sys.ThreadInterrupt(Trunc[uint](t.value))
            Spin(5)
            if sys.Now() > deadline {
                code = 62
                break
            }
        }
        if code == 0 && (*g).res.Load() != 1 {
            code = 6_000 + Trunc[u32]((*g).res.Load())
        }
    }
    if code != 62 {
        sys.Release(page, 4096)
    }
    return code
}

// NowCost is the mean cost of one sys.Now() in nanoseconds over 1,000,000
// calls (logged, not judged): the number 4.10's vDSO question needs (Linux
// Now is a clock_gettime system call; Windows QueryPerformanceCounter).
export NowCost(): i64 {
    var sink: i64 = 0
    t0 := sys.Now()
    for _ in 0..1_000_000 {
        sink = sink ^ sys.Now()
    }
    d := sys.Now() - t0
    if sink == 1 {
        return -1
    }
    return d / 1_000_000
}

// StrayTimer: with test point 7 on, a finite Windows PollWait finds a
// stray timer packet (another slot's late timer) before it waits; it must
// still last its whole timeout by Now() (the first version returned at
// once). Elsewhere the point does nothing and the same check holds.
export StrayTimer(): u32 {
    var out: [8]*sys.Op = [raw.Null[sys.Op](); 8]
    var code: u32 = 0
    for _ in 0..5 {
        // Wakes left by earlier cases make a wait return at once (the
        // contract): short blocking waits take them first.
        for _ in 0..3 {
            _ = sys.PollWait(1_000_000, mut out[..])
        }
        _ = sys.SysTestHook(7, ParkCount, raw.Null[u8](), true)
        t0 := sys.Now()
        n := sys.PollWait(20_000_000, mut out[..])
        d := sys.Now() - t0
        _ = sys.SysTestHook(7, ParkCount, raw.Null[u8](), false)
        if n != 0 {
            code = 1
            break
        }
        if d < 20_000_000 {
            // 2, and the length in microseconds above it.
            code = 2 + 10 * Trunc[u32](d / 1_000)
            break
        }
        if d > 1_000_000_000 {
            code = 3
            break
        }
    }
    return code
}
`
