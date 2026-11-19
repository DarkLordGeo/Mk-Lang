// Why: every carrier's system stack is SystemStackSize = 8 MiB, carrier 0's
// included, and the guard page below it must report a stack overflow.
// What: RT-SysStack-Bounds and RT-SysStack-GuardFault (SsCheck, SsTouchGuard),
// run by std/rt-sched_harness_test.go on both targets.
// How: tasks spread over the carriers and read each carrier record's bounds;
// the fault block touches the guard page and expects "stack limit".

import std.raw
import std.rt

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
        if hi - lo != Trunc[uint](SystemStackSize) {
            _ = bad.Add(1)
        }
        if ssp < lo || ssp >= hi {
            _ = bad.Add(1)
        }
        if me >= lo && me < hi {
            _ = bad.Add(1)
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
