// Why: every carrier's system stack is SystemStackSize = 8 MiB (on macOS
// up to a page more, SsSizeOk), carrier 0's included, and the guard page
// below it must report a stack overflow.
// What: RT-SysStack-Bounds and RT-SysStack-GuardFault (SsCheck, SsSizeOk,
// SsNote, SsTouchGuard),
// run by std/rt-sched_harness_test.go on both targets.
// How: tasks spread over the carriers and read each carrier record's bounds;
// the fault block touches the guard page and expects "stack limit".

import std.raw
import std.rt
import std.sys

// SsFirst holds the first failing SsCheck's why bits, carrier index, lo,
// hi, scheduler sp and task sp (SsNote); why is 0 until then.
var SsFirst: [6]raw.Atomic[u64] = [raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
    raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0)]

// SsNote records a failing check in SsFirst when it is the first: the Cas
// of why from 0 admits one writer.
SsNote(why: u64, i: u64, lo: u64, hi: u64, ssp: u64, me: u64) {
    if SsFirst[0].Cas(0, why) {
        SsFirst[1].Store(i)
        SsFirst[2].Store(lo)
        SsFirst[3].Store(hi)
        SsFirst[4].Store(ssp)
        SsFirst[5].Store(me)
    }
}

// SsSizeOk is whether n is a carrier thread's system stack size: exactly
// SystemStackSize, but on macOS up to a page more. libSystem puts the
// thread's own record in the last page of the mapping, just above the
// stack, and reports the stack's top as that record's start, so the rest
// of that page (12 KiB with arm64's 16 KiB pages and its 4 KiB record) is
// stack too.
SsSizeOk(n: uint): bool {
    size := Trunc[uint](SystemStackSize)
    if Target.os == Os.Macos {
        return n >= size && n < size + Trunc[uint](sys.PageSize())
    }
    return n == size
}

// SsCheck checks, from a task, the bounds of the carrier it runs on: 8 MiB,
// the scheduler's saved stack pointer inside them, the task's own stack
// pointer outside them (it is in a slot). bad counts failures; seen gets
// bit i set for carrier i < 64; on Linux read gets the lowest byte of the
// recorded range plus one, so the load that byte needs is a value the
// block checks, which no build may drop.
SsCheck(io: Io, bad: &raw.Atomic[u32], seen: &raw.Atomic[u64], read: &raw.Atomic[u64]) {
    // Spread over the carriers: spin 3 ms by the clock, then check.
    t0 := BenchNow()
    for BenchNow() - t0 < 3_000_000 {
    }
    t := Current()
    unsafe "the running task's carrier record" {
        c := (*t).carrier
        lo := (*c).sys_lo
        hi := (*c).sys_hi
        ssp := Trunc[uint]((*c).sys_ctx[0])
        me := rt.StackPointer()
        var why: u64 = 0
        if !SsSizeOk(hi - lo) {
            why |= 1
        }
        if ssp < lo || ssp >= hi {
            why |= 2
        }
        if me >= lo && me < hi {
            why |= 4
        }
        if why != 0 {
            _ = bad.Add(1)
            SsNote(why, Trunc[u64]((*c).index), u64(lo), u64(hi), u64(ssp), u64(me))
        }
        if Target.os == Os.Linux {
            // The lowest byte of the recorded range is readable: with
            // RT-SysStack-GuardFault (the byte below faults) this pins the
            // derived sys_lo to the real guard page from both sides.
            _ = read.Add(u64(raw.Read(raw.FromAddr[u8](lo))) + 1)
        }
        i := Trunc[u64]((*c).index)
        if i < 64 {
            for {
                w := seen.Load()
                if seen.Cas(w, w | (u64(1) << i)) {
                    break
                }
            }
        }
    }
}

test "RT-SysStack-Bounds every started carrier's system stack is 8 MiB and holds its scheduler" (sys: &System) {
    bad := raw.Atomic[u32].New(0)
    seen := raw.Atomic[u64].New(0)
    read := raw.Atomic[u64].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..32 {
            s.go SsCheck(sys.io, &bad, &seen, &read)
        }
    }
    if bad.Load() != 0 {
        // The first failing check's bits (1 size, 2 the scheduler's sp, 4
        // the task's sp) and values, so a failure names its cause.
        sys.PrintErr("bad {n}: why {w} carrier {i} lo {lo} hi {hi} ssp {ssp} me {me}\n", (n: bad.Load(),
            w: SsFirst[0].Load(), i: SsFirst[1].Load(), lo: SsFirst[2].Load(), hi: SsFirst[3].Load(),
            ssp: SsFirst[4].Load(), me: SsFirst[5].Load())) else {}
    }
    Assert(bad.Load() == 0)
    // On Linux each of the 32 tasks read its carrier's lowest byte.
    Assert(Target.os != Os.Linux || read.Load() >= 32)
    // Carrier 0 ran at least the body; when more carriers exist some ran
    // tasks too (RT-MN-Carriers shows the spread itself).
    Assert(seen.Load() != 0)
    // The body runs on a slot, never on a system stack.
    t := Current()
    unsafe "the running task's carrier record" {
        c := (*t).carrier
        me := rt.StackPointer()
        Assert(me < (*c).sys_lo || me >= (*c).sys_hi)
        // Carrier 0's own bounds, whatever carrier the body is on now.
        c0 := C0()
        Assert((*c0).sys_hi - (*c0).sys_lo == Trunc[uint](SystemStackSize))
    }
}

// SsTouchGuard reads the first byte below the system stack of the carrier
// it runs on (Linux), or recurses past its own slot's guard (Windows, where
// a system-stack overflow is the kernel's EXCEPTION_STACK_OVERFLOW and the
// page below the reservation is not a guard). It never returns.
SsTouchGuard(): int {
    if Target.os == Os.Linux {
        t := Current()
        unsafe "a read of the guard page below this carrier's system stack, on purpose" {
            c := (*t).carrier
            // A whole int, so that the caller's == 12345 can be true and a
            // release build keeps the load: a u8 can never equal 12345, and
            // LLVM drops a load whose value no path reads.
            return raw.Read(raw.FromAddr[int]((*c).sys_lo - 8))
        }
    }
    return FDeep(0)
}

test "RT-SysStack-GuardFault the guard below a system stack is a stack limit [exit 101: panic: stack limit]" (sys: &System) {
    // Reduced form: classification only. The Linux half faults in the guard
    // page below the running carrier's system stack from a task stack (the
    // kernel's si_addr is the guard address, so FaultKind's system-stack
    // rule is what turns the SIGSEGV into "stack limit"); an overflow that
    // runs down the system stack needs a C extern, which this test lacks.
    x := SsTouchGuard()
    Assert(x == 12345)
}
