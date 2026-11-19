// Why: the runtime needs one place for the process's state (arguments,
// the native Io, carriers' system stacks, task ids) that any thread can
// reach before and after tasks exist, and one way to end the process.
//
// What: RtState and RtWords, Init, R, Proc, Args, NativeIo, NextTaskId, the
// system-stack registry (SetSysStack, SysStackOf), Exit, Abandon and Fatal.
//
// How: RtState lives in a page Init maps, not in a `var`: its fields are
// then written through a raw pointer into memory no loan covers, and the
// one global is an atomic word holding its address.

import std.core
import std.raw
import std.sys

/// The runtime's process state, one per process in the page Init maps; R()
/// returns it. std.task reads its fields; nothing frees it.
export struct RtState {
    /// std.core's Process: the standard streams and the environment.
    export proc: core.Process
    /// The command-line arguments, decoded once (Args).
    export args: []str
    /// The general-purpose heap every Alloc of the process draws on.
    export heap: Heap
    /// The heap's shared cache and its per-carrier magazines.
    export hs: HeapState
    /// The native IoTable std.task.Start installs (SetNativeIo).
    export native_io: Io
    /// The task-stack regions by slot class (region.mk): default, large,
    /// then StackSize.Bytes's power-of-two classes, reserved on first use.
    export regions: [16]Region
    /// The stack pointer at entry, from which std.sys finds argv and the
    /// environment on Linux.
    export init_sp: *u8
    /// The executable's name for reports, "" until known.
    export exe_name: str
    /// One row per emitted function, for the trace and fault reports.
    export func_table: []FuncRow
    /// The id counter of spawned tasks (NextTaskId).
    export next_id: raw.Atomic[u64]
    // sys_lo[i] and sys_hi[i] bound carrier i's system stack, 0 and 0 when
    // unknown (on Linux a fault in the page below sys_lo is kind 1;
    // on Windows a fault on the stack while its task is in a user extern
    // is that code's own). Written by SetSysStack, read by FaultKind and
    // SysStackOf.
    sys_lo: [256]raw.Atomic[u64]
    sys_hi: [256]raw.Atomic[u64]
}

// RtWords is the static part: the state page's address, the leak counters
// and the executable image's range (0 to 0 until Init: OnFault then
// does not bound the site), usable before Init and from any thread.
struct RtWords {
    state: raw.Atomic[u64]
    counting: raw.Atomic[u32]
    live_count: raw.Atomic[i64]
    live_bytes: raw.Atomic[i64]
    image_lo: raw.Atomic[u64]
    image_hi: raw.Atomic[u64]
}

var Rt: RtWords = RtWords(state: raw.Atomic[u64].New(0), counting: raw.Atomic[u32].New(0), live_count: raw.Atomic[i64].New(0),
    live_bytes: raw.Atomic[i64].New(0), image_lo: raw.Atomic[u64].New(0), image_hi: raw.Atomic[u64].New(0))

const StateBytes: int = 65536

/// R is the RtState page, or null before Init.
/// O(1); allocates nothing.
export R(): *RtState {
    unsafe "the state word holds the page Init mapped, or 0" {
        // Edge: Init's store of the page address, after the page's fields.
        return raw.FromAddr[RtState](Trunc[uint](Rt.state.Load()))
    }
}

/// Init maps the state page and records sp (Start steps before 2). false
/// when the page cannot be had.
/// O(1): one mapping, kept until exit.
export Init(sp: *u8): bool {
    if SizeOf[RtState]() > StateBytes {
        return false
    }
    p := sys.Reserve(StateBytes, 65536)
    if raw.IsNull(p) || !sys.Commit(p, StateBytes) {
        return false
    }
    (lo, hi) := sys.ImageBounds(sp)
    // Edge: written once, before any task or fault (FaultSite reads them).
    Rt.image_lo.Store(u64(lo))
    Rt.image_hi.Store(u64(hi))
    unsafe "p is the fresh state page, zero filled" {
        st := raw.PtrCast[u8, RtState](p)
        (*st).init_sp = sp
        (*st).exe_name = ""
        (*st).func_table = FuncTable()
        // Edge: the page's fields are written before its address is published;
        // R() loads the address after this store.
        Rt.state.Store(u64(raw.Addr(p)))
    }
    return true
}

/// Proc is std.core's Process: streams, environment.
/// O(1); allocates nothing.
export Proc(): *core.Process {
    unsafe "Init ran, so R() is the page it mapped, which lives until exit" {
        return raw.Of(&(*R()).proc)
    }
}

/// SetNativeIo installs io as the process's native IoTable. Called once, by
/// std.task.Start, before Main runs.
/// O(1); allocates nothing.
export SetNativeIo(io: Io) {
    unsafe "Init ran, so R() is the page it mapped, which lives until exit; std.task.Start writes it once, before any task reads it" {
        (*R()).native_io = io
    }
}

/// NativeIo is the IoTable SetNativeIo installed.
/// O(1); allocates nothing.
export NativeIo(): Io {
    unsafe "Init ran, so R() is the page it mapped, which lives until exit" {
        return (*R()).native_io
    }
}

/// Args is sys.args, decoded once.
/// O(1); allocates nothing (DecodeArgs did, at start).
export Args(): []str {
    unsafe "Init ran, so R() is the page it mapped, which lives until exit" {
        return (*R()).args
    }
}

/// NextTaskId hands out the numbers of spawned tasks: 2, 3, ... Main is 1,
/// laid by Start without a call here. next_id starts at 0 in
/// the zero-filled state page and Add returns the old value, so
/// the n-th call returns n + 1; OnFault names a task by id > 1.
/// O(1): one atomic add; allocates nothing.
export NextTaskId(): u64 {
    unsafe "Init ran, so R() is the page it mapped, which lives until exit" {
        // Edge: none needed; the Add only has to return a distinct value.
        return (*R()).next_id.Add(1) + 2
    }
}

/// Exit is proc.Exit: flush, the profile, then the process ends at once;
/// other tasks are not joined. The flush is not cancellable: proc.Exit
/// from a task in a cancelled scope still writes the buffered output.
/// Never returns. Its cost is the flush's and the profile's; allocates
/// nothing.
export Exit(code: int): never {
    var c := code
    if !raw.IsNull(R()) {
        // A failed last flush is reported and turns 0 into 1.
        c = core.FlushStdCode(Proc(), core.IoMasked(NativeIo()), code)
        // A terminal mode std.term saved is put back, and the
        // alternate screen left, after the program's last output.
        TermExitHook()
        WriteProfile()
    }
    sys.Exit(Trunc[i32](c))
}

/// Abandon closes a runtime handle and ignores the result.
/// O(1), as CloseRuntimeHandle; frees what it frees and allocates nothing.
export Abandon(h: uint) {
    _ = CloseRuntimeHandle(h)
}

/// SetSysStack records carrier i's system stack [lo, hi). On
/// Linux, where std.sys reports every SIGSEGV as kind 2, the page below lo
/// is its guard (InSysGuard); Windows reports a system-stack overflow
/// itself (EXCEPTION_STACK_OVERFLOW), and the page below its reservation is
/// not a guard. On both, SysStackOf finds the carrier a stack pointer is on,
/// for the fault filter's in_c test.
/// O(1); allocates nothing.
export SetSysStack(i: int, lo: uint, hi: uint) {
    st := R()
    if raw.IsNull(st) || i < 0 || i >= 256 || lo < 4096 {
        return
    }
    unsafe "st is R(), not null, which lives until exit; i is below 256" {
        // Edge: carrier i stores its own entry before its scheduler loop
        // runs on that stack, so a fault on the stack, which the kernel
        // delivers on the same thread, reads it in program order. A fault
        // on another thread may or may not see it; it is not about this
        // stack. hi is stored first, so a reader that sees lo sees its hi;
        // a hi of 0 (bounds unknown) keeps the Linux guard and matches no
        // stack pointer.
        (*st).sys_hi[i].Store(u64(hi))
        (*st).sys_lo[i].Store(u64(lo))
    }
}

/// ClearSysStack forgets carrier i's system stack (a joined
/// runtime.Thread's carrier, whose stack is unmapped): its guard is no
/// longer InSysGuard's and no stack pointer matches it in SysStackOf, so a
/// later mapping at those addresses is not taken for that stack. lo is
/// cleared first, so a fault handler that reads it 0 skips the entry.
/// O(1); allocates nothing.
export ClearSysStack(i: int) {
    st := R()
    if raw.IsNull(st) || i < 0 || i >= 256 {
        return
    }
    unsafe "st is R(), not null, which lives until exit; i is below 256" {
        // Edge: see SetSysStack; 0 is "none", and lo is the word every
        // reader tests first.
        (*st).sys_lo[i].Store(0)
        (*st).sys_hi[i].Store(0)
    }
}

// InSysGuard is whether a lies in the guard page below a recorded system
// stack (Linux only; see SetSysStack).
InSysGuard(a: uint): bool {
    st := R()
    if Target.os != Os.Linux || raw.IsNull(st) {
        return false
    }
    unsafe "st is R(), not null, which lives until exit" {
        for i in 0..256 {
            // Edge: see SetSysStack; 0 is "none".
            lo := Trunc[uint]((*st).sys_lo[i].Load())
            if lo != 0 && a < lo && lo - a <= 4096 {
                return true
            }
        }
    }
    return false
}

/// SysStackOf is the index of the carrier whose recorded system stack holds
/// sp, or -1. A stack pointer is on one thread's stack, and only carrier
/// i's thread runs on carrier i's system stack, so an answer i for the
/// calling thread's own sp names the calling thread's carrier.
/// O(256) loads; allocates nothing. Safe in a fault handler.
export SysStackOf(sp: uint): int {
    st := R()
    if raw.IsNull(st) {
        return -1
    }
    unsafe "st is R(), not null, which lives until exit" {
        for i in 0..256 {
            // Edge: see SetSysStack. The calling thread's own entry was
            // stored by it; another carrier's may be seen half written,
            // with lo still 0, which matches nothing.
            // lo is loaded again after hi: a ClearSysStack and a new
            // SetSysStack between the two loads would pair the old lo with
            // the new hi, and the second load then differs.
            lo := Trunc[uint]((*st).sys_lo[i].Load())
            if lo != 0 && sp >= lo && sp < Trunc[uint]((*st).sys_hi[i].Load()) && Trunc[uint]((*st).sys_lo[i].Load()) == lo {
                return i
            }
        }
    }
    return -1
}

/// Fatal writes "panic: <msg>" with no site and exits with 101.
/// Never returns. O(len(msg)); allocates nothing.
export Fatal(msg: str): never {
    Report(8, raw.Null[Site](), msg, "")
}
