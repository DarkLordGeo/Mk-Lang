// Why: a user extern must run on its carrier's system stack, not the task
// slot, on every carrier; only a real module outside std can show that.
// What: blocks for SystemStack and the extern call path, with helpers
// ExternOf, XCall, XWork, XDeep and DeepWant.
// How: kind 1 blocks, run by std/rt-sched_harness_test.go: an extern call made by a module outside std
// runs through std.task.SystemStack on whatever carrier its task is on, and
// tasks that land on many carriers each get their own carrier's system
// stack. The extern is the support pack's memcmp, declared with integer
// parameters by the harness's probe package (module ext, which pkg.mk lists
// as unsafe) and registered by its Main with SetExternProbe. std cannot
// declare it itself: codegen routes only externs declared outside std
// Under mk test no module outside std exists, so the
// blocks check nothing there.
//
// The stack halves ("RT-Extern-SystemStack", "RT-F21-SystemStackEveryCarrier")
// call the probe package's second extern, ZzRtsDeep(kib),
// declared bodiless in module ext and defined with an Mk body (`export
// extern`) in the package's module deep: it keeps a
// 7 MiB frame and touches one byte per page of its first kib KiB, returning
// the sum of (page & 255) over those pages, as tests/bench/ccall's RefStack
// does (8128 for 512, 228480 for 7,168). The call site sees a bodiless
// extern declared outside std, so codegen routes it through SystemStack; a
// direct call of the same function from a task faults the 1 MiB slot's
// guard ("stack limit", measured), so a run that returns
// shows the system stack was used and holds the frame. A real C object
// (tests/bench/ccall/ccall.c) would need the driver's
// ExtraObjects seam, which no mk command reaches.

import std.raw
import std.rt

// ExternSlot holds the registered extern wrappers: f calls memcmp, deep
// calls ZzRtsDeep.
struct ExternSlot {
    f: fn(uint, uint, uint): i64
    deep: fn(i64): i64
}

// The address of the ExternSlot SetExternProbe made, 0 when none.
var ExternHook: raw.Atomic[u64] = raw.Atomic[u64].New(0)

/// SetExternProbe registers f and deep, wrappers of externs declared outside
/// std (harness only; the slot lives for the process). Called once by Main
/// before any block runs.
export SetExternProbe(f: fn(uint, uint, uint): i64, deep: fn(i64): i64) {
    p := rt.HeapAlloc(SizeOf[ExternSlot](), 8)
    Assert(!raw.IsNull(p))
    unsafe "p is a fresh block of one ExternSlot that is never freed" {
        raw.Write(raw.PtrCast[u8, ExternSlot](p), ExternSlot(f: f, deep: deep))
        // Edge: the slot's write precedes this store in S; ExternOf's load
        // that reads the address follows it, so it sees the slot.
        ExternHook.Store(Trunc[u64](raw.Addr(p)))
    }
}

// ExternOf is the registered slot, or null.
ExternOf(): *ExternSlot {
    unsafe "the slot SetExternProbe made, or null" {
        // Edge: SetExternProbe's store (see there).
        return raw.FromAddr[ExternSlot](Trunc[uint](ExternHook.Load()))
    }
}

// XCall makes one extern call of memcmp(a, b, 16) on the calling task and
// checks it: the call raises the running carrier's extern_calls by exactly
// one (codegen called SystemStack, which found the task, so the call went
// through mk_call_system), the stack pointer SystemStack prepared lies inside
// that carrier's system stack and is 16-aligned, the task's own stack is
// outside it, in_c is 0 again after the call, and memcmp's sign is want's.
// The task cannot change carrier between the reads: nothing here suspends.
// Returns the carrier's index, or -1 after a failed check.
XCall(slot: *ExternSlot, a: uint, b: uint, want: int): int {
    t := Current()
    unsafe "the running task's carrier record and slot header" {
        c := (*t).carrier
        e0 := (*c).extern_calls
        r := (*slot).f(a, b, 16)
        if (*c).extern_calls != e0 + 1 {
            return -1
        }
        sp := raw.Addr((*c).sys_stack.sp)
        if sp < (*c).sys_lo || sp >= (*c).sys_hi || sp & 15 != 0 {
            return -1
        }
        me := rt.StackPointer()
        if me >= (*c).sys_lo && me < (*c).sys_hi {
            return -1
        }
        if (*HeaderOf(t)).in_c != 0 {
            return -1
        }
        if (want < 0 && r >= 0) || (want > 0 && r <= 0) || (want == 0 && r != 0) {
            return -1
        }
        return int((*c).index)
    }
}

// XWork spins 3 ms by the clock (so idle carriers steal the queued tasks),
// then makes 40 extern calls, yielding between them, and ORs the bit of
// each carrier a call ran on into seen; bad counts failed checks.
XWork(io: Io, k: int, bad: &raw.Atomic[u32], seen: &raw.Atomic[u64]) {
    slot := ExternOf()
    var x: [16]u8 = [0; 16]
    var y: [16]u8 = [0; 16]
    y[k % 16] = 1
    t0 := BenchNow()
    for BenchNow() - t0 < 3_000_000 {
    }
    unsafe "x and y are this frame's; memcmp reads 16 bytes of each" {
        xa := raw.Addr(raw.Data(x[..]))
        ya := raw.Addr(raw.Data(y[..]))
        for j in 0..40 {
            var i := 0
            match j % 3 {
                0 {
                    i = XCall(slot, xa, ya, -1)
                }
                1 {
                    i = XCall(slot, ya, xa, 1)
                }
                _ {
                    i = XCall(slot, xa, xa, 0)
                }
            }
            if i < 0 {
                _ = bad.Add(1)
            } else {
                bit := u64(1) << Trunc[u64](i & 63)
                for {
                    // Edge: none needed beyond the read-modify-write: the
                    // block reads seen after the scope joins every task.
                    w := seen.Load()
                    if seen.Cas(w, w | bit) {
                        break
                    }
                }
            }
            YieldNow()
        }
    }
}

test "RT-F21-ExternEveryCarrier extern calls from 64 tasks run through the system stack of each carrier they land on" (sys: &System) {
    if raw.IsNull(ExternOf()) {
        // mk test: no module outside std, so no extern to call.
        return
    }
    // The body's own call: carrier 0's system stack, or the one it is on.
    var x: [16]u8 = [0; 16]
    unsafe "x is 16 bytes in this frame, live across the call" {
        xa := raw.Addr(raw.Data(x[..]))
        Assert(XCall(ExternOf(), xa, xa, 0) >= 0)
    }
    var want: u32 = 1
    unsafe "the TaskState page Start mapped, which lives until exit" {
        want = (*TS()).want.Load()
    }
    if want > 4 {
        want = 4
    }
    // Up to 3 rounds of 64 tasks until the calls covered min(carriers, 4)
    // carriers (4 are wanted at MK_CARRIERS=8); every call is checked
    // in every round.
    bad := raw.Atomic[u32].New(0)
    seen := raw.Atomic[u64].New(0)
    var rounds := 0
    for rounds < 3 && Trunc[u32](raw.Popcount64(seen.Load())) < want {
        scope s := sys.io.Scope() {
            for k in 0..64 {
                s.go XWork(sys.io, k, &bad, &seen)
            }
        }
        rounds += 1
    }
    Assert(bad.Load() == 0)
    Assert(Trunc[u32](raw.Popcount64(seen.Load())) >= want)
    FNote("f21-carriers", i64(raw.Popcount64(seen.Load())))
    FNote("f21-rounds", i64(rounds))
}

// DeepWant is ZzRtsDeep's answer for kib (a positive multiple of 4): the
// sum of (p & 255) over its pages p.
DeepWant(kib: int): i64 {
    var s: i64 = 0
    for p in 0..kib / 4 {
        s += i64(p & 255)
    }
    return s
}

// XDeep makes one ZzRtsDeep(kib) call on the calling task and checks it as
// XCall does: the running carrier's extern_calls goes up by exactly one,
// the prepared sp is inside that carrier's system stack and 16-aligned, the
// task's own stack is outside it, in_c is 0 again, and the answer is
// DeepWant(kib). Nothing here suspends, so the carrier stays the same.
// Returns the carrier's index, or -1 after a failed check.
XDeep(slot: *ExternSlot, kib: int): int {
    t := Current()
    unsafe "the running task's carrier record and slot header" {
        c := (*t).carrier
        e0 := (*c).extern_calls
        r := (*slot).deep(i64(kib))
        if (*c).extern_calls != e0 + 1 {
            return -1
        }
        sp := raw.Addr((*c).sys_stack.sp)
        if sp < (*c).sys_lo || sp >= (*c).sys_hi || sp & 15 != 0 {
            return -1
        }
        me := rt.StackPointer()
        if me >= (*c).sys_lo && me < (*c).sys_hi {
            return -1
        }
        return if (*HeaderOf(t)).in_c != 0 || r != DeepWant(kib) { -1 } else { int((*c).index) }
    }
}

// XSeen ORs carrier i's bit into seen (i < 0 counts in bad instead).
XSeen(i: int, bad: &raw.Atomic[u32], seen: &raw.Atomic[u64]) {
    if i < 0 {
        _ = bad.Add(1)
        return
    }
    bit := u64(1) << Trunc[u64](i & 63)
    for {
        // Edge: none needed beyond the read-modify-write: the block reads
        // seen after the scope joins every task.
        w := seen.Load()
        if seen.Cas(w, w | bit) {
            break
        }
    }
}

// XDeepWork spins 3 ms by the clock (so idle carriers steal the queued
// tasks), yields, then calls ZzRtsDeep(512). Every fourth task, and every
// task that finds itself on carrier 0 while no 7 MiB call has run there,
// also calls ZzRtsDeep(7168) right after, on the same carrier: nothing
// between the two calls suspends. Without the carrier-0 arm the 16 calls a
// round makes miss carrier 0 in about half the rounds at 16 carriers, and
// five rounds in a row about once in a hundred runs.
XDeepWork(io: Io, k: int, bad: &raw.Atomic[u32], s512: &raw.Atomic[u64], s7: &raw.Atomic[u64]) {
    slot := ExternOf()
    t0 := BenchNow()
    for BenchNow() - t0 < 3_000_000 {
    }
    YieldNow()
    i := XDeep(slot, 512)
    XSeen(i, bad, s512)
    if k % 4 == 0 || (i == 0 && s7.Load() & 1 == 0) {
        XSeen(XDeep(slot, 7168), bad, s7)
    }
}

// XDeepOne calls ZzRtsDeep(512) and (7168) from a spawned task.
XDeepOne(io: Io, bad: &raw.Atomic[u32]) {
    slot := ExternOf()
    if XDeep(slot, 512) < 0 || XDeep(slot, 7168) < 0 {
        _ = bad.Add(1)
    }
}

test "RT-Extern-SystemStack an extern using 512 KiB and 7 MiB of stack runs from a task with a 1 MiB slot" (sys: &System) {
    if raw.IsNull(ExternOf()) {
        // mk test: no module outside std, so no extern to call.
        return
    }
    Assert(DeepWant(512) == 8128 && DeepWant(7168) == 228480)
    slot := ExternOf()
    // The block's own task, then a spawned one, 10 times each: the frame
    // fits only on a system stack (8 MiB), never in the task's slot.
    bad := raw.Atomic[u32].New(0)
    for _ in 0..10 {
        Assert(XDeep(slot, 512) >= 0)
        Assert(XDeep(slot, 7168) >= 0)
        scope s := sys.io.Scope() {
            s.go XDeepOne(sys.io, &bad)
        }
    }
    Assert(bad.Load() == 0)
}

test "RT-F21-SystemStackEveryCarrier 64 tasks call a 512 KiB extern on min(carriers, 4) carriers, and a 7 MiB one runs on carrier 0 and another" (sys: &System) {
    if raw.IsNull(ExternOf()) {
        return
    }
    var want: u32 = 1
    unsafe "the TaskState page Start mapped, which lives until exit" {
        want = (*TS()).want.Load()
    }
    if want > 4 {
        want = 4
    }
    want7: u32 = if want > 1 { 2 } else { 1 }
    // Up to 5 rounds of 64 tasks until the 512 KiB calls covered
    // min(carriers, 4) carriers and the 7 MiB calls carrier 0 and (with 2
    // or more carriers) another; every call is checked in every round.
    bad := raw.Atomic[u32].New(0)
    s512 := raw.Atomic[u64].New(0)
    s7 := raw.Atomic[u64].New(0)
    var rounds := 0
    for rounds < 5 && (Trunc[u32](raw.Popcount64(s512.Load())) < want || s7.Load() & 1 == 0 || Trunc[u32](raw.Popcount64(s7.Load())) < want7) {
        scope s := sys.io.Scope() {
            for k in 0..64 {
                s.go XDeepWork(sys.io, k, &bad, &s512, &s7)
            }
        }
        rounds += 1
    }
    Assert(bad.Load() == 0)
    Assert(Trunc[u32](raw.Popcount64(s512.Load())) >= want)
    Assert(s7.Load() & 1 == 1)
    Assert(Trunc[u32](raw.Popcount64(s7.Load())) >= want7)
    FNote("f21-deep-carriers", i64(raw.Popcount64(s512.Load())))
    FNote("f21-deep7-carriers", i64(raw.Popcount64(s7.Load())))
    FNote("f21-deep-rounds", i64(rounds))
}
