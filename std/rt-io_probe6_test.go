// Why: ThreadStart, Reserve and the Windows SemWait had defects at the
// limits of their argument types; each keeps the case that failed.
// What: rtioProbe6, the Mk source of std/rtioprobe/probe6.mk.
// How: std/rt-io_argedges_test.go writes it next to the earlier probe files,
// whose helpers it uses (NewPage, Sh, Spin, SemPoster, BoundsThread,
// WaitWord); each case returns 0, or the number of the step that failed.

package std

// rtioProbe6 is std/rtioprobe/probe6.mk of the copy.
const rtioProbe6 = `// A probe of std.sys, sixth file (std/rt-io_probe6_test.go).

import std.core
import std.raw
import std.sys

// MaxInt is int's largest value.
const MaxInt: int = 0x7FFF_FFFF_FFFF_FFFF

// SemLong: a finite SemWait returns false only once its deadline by Now()
// has passed. With test point 8 on, one Windows WaitForSingleObject waits
// at most 2 ms, as each call of a timeout of 49.7 days or more does (the
// millisecond cap); SemWait returned false at the cap, so a 20 ms wait
// ended after 2 ms. On Linux the point does nothing and the same steps
// must pass (SemWait there waits by CLOCK_MONOTONIC).
export SemLong(): u32 {
    page := NewPage()
    if raw.IsNull(page) {
        return 100
    }
    g := Sh(page)
    var code: u32 = 0
    unsafe "g is the case's page" {
        if !sys.SemInit(mut (*g).sem) {
            return 101
        }
        base := sys.SysTestHook(8, ParkCount, raw.Null[u8](), true)
        for round in 0..5 {
            t0 := sys.Now()
            ok := sys.SemWait(&(*g).sem, 20_000_000)
            d := sys.Now() - t0
            if ok {
                code = 10 + Trunc[u32](round)
                break
            }
            if d < 20_000_000 {
                // 20 + the round, and the length in microseconds above it.
                code = 20 + Trunc[u32](round) + 100 * Trunc[u32](d / 1_000)
                break
            }
            if d > 1_000_000_000 {
                code = 30 + Trunc[u32](round)
                break
            }
        }
        if code == 0 {
            // A post 50 ms later ends a 5 s wait with true.
            if !Ok(sys.ThreadStart(SemPoster, page, 262144)) {
                return 102
            }
            t0 := sys.Now()
            if !sys.SemWait(&(*g).sem, 5_000_000_000) {
                code = 40
            } else if sys.Now() - t0 < 40_000_000 {
                code = 41
            } else if sys.Now() - t0 > 2_000_000_000 {
                code = 42
            }
            if !WaitWord(raw.Of(&(*g).exited), 1, 10_000) {
                code = 43
            }
        }
        ran := sys.SysTestHook(8, ParkCount, raw.Null[u8](), false) - base
        if code == 0 && Target.os == Os.Windows && ran == 0 {
            // The point must have shortened the Windows waits.
            code = 50
        }
        if code == 0 && Target.os != Os.Windows && ran != 0 {
            code = 51
        }
        // Without the point: a zero wait is false at once, and short waits
        // last at least their timeout by Now() (the interrupt clock ended
        // Windows' millisecond waits early, as PollWait's).
        if code == 0 {
            t0 := sys.Now()
            if sys.SemWait(&(*g).sem, 0) {
                code = 60
            } else if sys.Now() - t0 > 100_000_000 {
                code = 61
            }
        }
        if code == 0 {
            for round in 0..20 {
                t0 := sys.Now()
                if sys.SemWait(&(*g).sem, 3_000_000) {
                    code = 70 + Trunc[u32](round)
                    break
                }
                d := sys.Now() - t0
                if d < 3_000_000 {
                    code = 90 + Trunc[u32](round) + 100 * Trunc[u32](d / 1_000)
                    break
                }
            }
        }
    }
    return code
}

// StackArg: ThreadStart refuses a stack of 0, a negative one and one past
// MaxThreadStack (1 TiB) with Other on both targets, before anything is
// made; and it rounds an odd size up to whole pages. Before: Windows took 0
// as the image's default; Linux overflowed (a panic) for int's largest
// value, put the start record in the guard page for 0, and mapped an odd
// size as given (a misaligned stack; hi - lo was the odd size).
export StackArg(): u32 {
    page := NewPage()
    if raw.IsNull(page) {
        return 100
    }
    g := Sh(page)
    var step: u32 = 0
    for s in [0, -1, -4096, MaxInt, MaxInt - 4095, (1 << 40) + 1] {
        step += 1
        r := sys.ThreadStart(BoundsThread, page, s)
        if Ok(r) {
            return step
        }
        if r.err != core.SysErr.Other {
            return 10 + step
        }
    }
    unsafe "g is the case's page; it is never released (the thread may still be ending)" {
        // 10,001 bytes are three pages on Linux (hi - lo is exactly 12,288,
        // BoundsThread's rule there), at least that on Windows.
        (*g).t0.Store(12288)
        if !Ok(sys.ThreadStart(BoundsThread, page, 10001)) {
            return 20
        }
        if !WaitWord(raw.Of(&(*g).exited), 1, 10_000) {
            return 21
        }
        if (*g).bad.Load() != 0 {
            return 22
        }
        // The largest stack taken is taken (1 TiB of address space; on
        // Linux it is committed only as touched, and the commit may be
        // refused: then the call fails with NoSpace or Other, never a
        // panic). Not judged beyond that: the machine's limits decide.
    }
    return 0
}

// ReserveLimits: Reserve answers null, never a panic or a misaligned
// range, at the limits: int's largest size, an align that is not a power
// of two, a huge align; a small align still gives whole pages; and on Linux
// an odd size leaves nothing mapped after Release (the tail was left
// mapped: 56 KiB of address space per Reserve(5000, 65536)).
export ReserveLimits(): u32 {
    if !raw.IsNull(sys.Reserve(MaxInt, 4096)) {
        return 1
    }
    if !raw.IsNull(sys.Reserve(MaxInt - 4096, 65536)) {
        return 2
    }
    if !raw.IsNull(sys.Reserve(4096, 3)) {
        return 3
    }
    if !raw.IsNull(sys.Reserve(4096, 3 * 65536)) {
        return 4
    }
    if !raw.IsNull(sys.Reserve(4096, 1 << 62)) {
        return 5
    }
    if !raw.IsNull(sys.Reserve(0, 4096)) || !raw.IsNull(sys.Reserve(4096, 0)) || !raw.IsNull(sys.Reserve(-1, 4096)) {
        return 6
    }
    p := sys.Reserve(4096, 16)
    if raw.IsNull(p) {
        return 7
    }
    if (raw.Addr(p) & 4095) != 0 {
        return 8
    }
    if !sys.Commit(p, 4096) {
        return 9
    }
    unsafe "the page just committed" {
        raw.Write(raw.Offset(p, 4095), Trunc[u8](7))
        if raw.Read(raw.Offset(p, 4095)) != 7 {
            return 10
        }
    }
    sys.Release(p, 4096)
    before := VmPages()
    for _ in 0..1000 {
        q := sys.Reserve(5000, 65536)
        if raw.IsNull(q) {
            return 11
        }
        if (raw.Addr(q) & 65535) != 0 {
            return 12
        }
        if !sys.Commit(q, 5000) {
            return 13
        }
        unsafe "the range just committed" {
            raw.Write(raw.Offset(q, 4999), Trunc[u8](1))
        }
        sys.Release(q, 5000)
    }
    after := VmPages()
    if before >= 0 && after >= 0 && after - before > 1024 {
        // 14, and the growth in pages above it (1,000 leaked tails of 56
        // KiB are 14,000 pages).
        return 14 + 100 * Trunc[u32](after - before)
    }
    return 0
}

// KindRefused: a kind PollSupports refuses (0, OpTimeoutWake, 99) completes
// inside PollSubmit with Other on every backend, and nothing is delivered
// for it (the contract's "PollSubmit on an unsupported kind").
export KindRefused(): u32 {
    var step: u32 = 0
    var kinds: [3]u32 = [0, sys.OpTimeoutWake, 99]
    for k in kinds {
        step += 1
        if sys.PollSupports(k) {
            return step
        }
        var op := sys.NewOp(k, 0)
        if sys.PollSubmit(raw.OfMut(mut op)) {
            return 10 + step
        }
        if op.res.err != core.SysErr.Other {
            return 20 + step
        }
    }
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    if sys.PollWait(0, mut out[..]) != 0 {
        return 30
    }
    return 0
}

// VmPages is the process's address space in pages (the first field of
// /proc/self/statm) on Linux; -1 elsewhere or when it cannot be read.
VmPages(): i64 {
    if Target.os != Os.Linux {
        return -1
    }
    r := sys.OpenFile("/proc/self/statm", 0, false)
    if !Ok(r) {
        return -1
    }
    h := Trunc[uint](r.value)
    var buf: [256]u8 = [0; 256]
    x := sys.ReadSync(h, mut buf[..], -1)
    _ = sys.Close(h)
    if !Ok(x) || x.value == 0 {
        return -1
    }
    var n: i64 = 0
    for i in 0..Trunc[int](x.value) {
        c := buf[i]
        if c < 48 || c > 57 {
            break
        }
        n = n * 10 + i64(c - 48)
    }
    return n
}
`
