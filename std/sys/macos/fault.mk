// Why: a memory fault in Mk code must become a clear report (a stack
// overflow, a bad access) instead of a silent kill, and the runtime must
// tell its own faults from those of foreign code.
//
// What: the SIGSEGV, SIGBUS and SIGILL handlers on the alternate signal
// stack, SIGPIPE ignored, FatalWrite, FaultOwned and the fault hook state.
//
// How: as on Linux, through libSystem's sigaction (whose own trampoline
// returns from the handler): a SIGSEGV or SIGBUS becomes a FaultInfo of
// kind 2 and a SIGILL one of kind 3, handed to the hook, and rt.OnFault
// makes kind 2 kind 1 when the address is a guard page it owns. The fault's
// pc and sp come from the thread state in uc_mcontext (<mach/arm/
// _structs.h>, <mach/i386/_structs.h>). runtime.AddFaultFilter's filters
// run first, on that thread state (filter.mk); a resume returns, and
// libSystem's trampoline installs the state the filter edited. A library's
// handler chain is not built (lib.mk).

import std.raw

// FaultState: the hook and its set word; the image's bounds (ImageBounds
// stores them); the ownership hook's fn bytes and its set word
// (FaultOwnerHook); the main thread's pthread_self.
struct FaultState {
    hook: raw.Atomic[u64]
    set: raw.Atomic[u32]
    img_lo: raw.Atomic[u64]
    img_hi: raw.Atomic[u64]
    own: raw.Atomic[u64]
    own_set: raw.Atomic[u32]
}

var Faults: FaultState = FaultState(hook: raw.Atomic[u64].New(0), set: raw.Atomic[u32].New(0), img_lo: raw.Atomic[u64].New(0),
    img_hi: raw.Atomic[u64].New(0), own: raw.Atomic[u64].New(0), own_set: raw.Atomic[u32].New(0))

// Darwin's sa_flags (<sys/signal.h>).
const SaOnStack: u32 = 0x1
const SaRestart: u32 = 0x2
const SaNoDefer: u32 = 0x10
const SaSigInfo: u32 = 0x40
// Darwin's signal numbers.
const SigIll: i32 = 4
const SigBus: i32 = 10
const SigSegv: i32 = 11
const SigPipe: i32 = 13

// Action installs a handler (a C-ABI function, or SIG_IGN as 1, SIG_DFL as
// 0) for sig through libSystem's struct sigaction: the handler, then
// sa_mask and sa_flags.
Action(sig: i32, handler: u64, flags: u32): bool {
    var act: [2]u64 = [0; 2]
    act[0] = handler
    act[1] = u64(flags) << 32
    unsafe "act is a struct sigaction of 16 bytes" {
        return sigaction(sig, raw.PtrCast[u64, u8](raw.Data(act[..])), raw.Null[u8]()) == 0
    }
}

// HandlerWord is the address of a signal handler, for sigaction.
HandlerWord(f: fn(i32, *u8, *u8)): u64 {
    var w: u64 = 0
    unsafe "the bytes of a fn value are its code address" {
        raw.Write(raw.PtrCast[u64, fn(i32, *u8, *u8)](raw.OfMut(mut w)), f)
    }
    return w
}

/// ExternFaultHook has std/sys/windows's contract, where the vectored
/// handler asks f whether a fault is in a user extern's call. Here it does
/// nothing, as on Linux: a signal handler is process-wide.
/// O(1); allocates nothing.
export ExternFaultHook(f: fn(uint): uint) {
}

/// FuncRowHook has std/sys/windows's contract. Here it does nothing, as
/// ExternFaultHook.
/// O(1); allocates nothing.
export FuncRowHook(rows: *u8, n: int) {
}

/// FaultOwnerHook installs f, which answers whether a stack pointer lies on
/// a stack Mk runs on (a task slot, a carrier's system stack): std.task's,
/// at start. Before it, and in a program with no task, every fault is
/// taken as Mk's.
/// O(1); allocates nothing.
export FaultOwnerHook(f: fn(uint): bool) {
    unsafe "the hook word is an atomic's memory" {
        raw.Write(raw.PtrCast[raw.Atomic[u64], fn(uint): bool](raw.Of(&Faults.own)), f)
    }
    // Edge: the Store publishes the hook's bytes, written above, to the
    // handler's Load.
    Faults.own_set.Store(1)
}

/// FaultOwned is the handler's ownership test: a fault is Mk's when its pc
/// is in the executable's image, when its thread is one std.sys made or
/// the main thread, or when its stack pointer is on a stack Mk runs on
/// (FaultOwnerHook's answer). Otherwise it is C code outside the image on a
/// foreign thread, and the signal gets its default action (Redeliver).
/// Async-signal-safe: loads, and pthread calls that take no lock. O(1)
/// besides the hook; allocates nothing.
export FaultOwned(pc: uint, sp: uint): bool {
    // Edge: as FaultOwnerHook's Store; before it every fault is Mk's.
    if Faults.own_set.Load() == 0 {
        return true
    }
    lo := Trunc[uint](Faults.img_lo.Load())
    hi := Trunc[uint](Faults.img_hi.Load())
    if lo == 0 || (pc >= lo && pc < hi) {
        return true
    }
    return FaultOwnedBy(pc, sp, ThreadMade())
}

/// FaultOwnedBy is FaultOwned with the thread's answer given (made: a
/// thread std.sys started), for tests. O(1) besides the hook; allocates
/// nothing.
export FaultOwnedBy(pc: uint, sp: uint, made: bool): bool {
    lo := Trunc[uint](Faults.img_lo.Load())
    hi := Trunc[uint](Faults.img_hi.Load())
    if lo == 0 || (pc >= lo && pc < hi) || made {
        return true
    }
    if Faults.own_set.Load() == 0 {
        return true
    }
    unsafe "the hook word holds the fn FaultOwnerHook wrote before its set word" {
        f := raw.Read(raw.PtrCast[raw.Atomic[u64], fn(uint): bool](raw.Of(&Faults.own)))
        return f(sp)
    }
}

/// FaultHook installs h and the handlers, once per process.
/// O(1) system calls (the handler's installation); allocates nothing.
export FaultHook(h: fn(&FaultInfo): never) {
    unsafe "the hook word is an atomic's memory" {
        raw.Write(raw.PtrCast[raw.Atomic[u64], fn(&FaultInfo): never](raw.Of(&Faults.hook)), h)
    }
    // Edge: the Swap admits one installer; the hook's bytes, written above,
    // come before it, and sigaction orders the handler's install after both.
    if Faults.set.Swap(1) == 0 {
        w := HandlerWord(SigFault)
        _ = Action(SigSegv, w, SaSigInfo | SaOnStack | SaNoDefer)
        _ = Action(SigBus, w, SaSigInfo | SaOnStack | SaNoDefer)
        _ = Action(SigIll, w, SaSigInfo | SaOnStack | SaNoDefer)
        _ = Action(SigPipe, 1, 0)
    }
}

// McontextSp and McontextPc are the offsets of the stack pointer and the
// program counter in the thread state uc_mcontext points to: after the
// 16-byte exception state, arm64's x0 to x28, fp and lr come before sp and
// pc; x86_64's rsp is the eighth word and rip the seventeenth.
McontextSp(): int {
    return if Target.arch == Arch.Arm64 { 264 } else { 72 }
}

McontextPc(): int {
    return if Target.arch == Arch.Arm64 { 272 } else { 144 }
}

// Redeliver gives sig its default action now: the disposition is restored
// to SIG_DFL and the signal raised again on the calling thread
// (SA_NODEFER leaves it unblocked in the handler), so it is delivered as
// the handler returns and ends the process by that signal.
Redeliver(sig: i32) {
    _ = Action(sig, 0, 0)
    unsafe "pthread_kill of the calling thread passes no memory" {
        _ = pthread_kill(pthread_self(), sig)
    }
}

// SigFault runs on the alternate signal stack, for SIGSEGV, SIGBUS and
// SIGILL. siginfo_t's si_code is at 8 and si_addr at 24; ucontext_t's
// uc_mcontext pointer at 48.
SigFault(sig: i32, info: *u8, uc: *u8) {
    unsafe "info and uc are libSystem's siginfo_t and ucontext_t" {
        // si_code 0 or below is a signal another process or thread sent
        // (SI_USER), not a fault: it gets its default action.
        if raw.Read(raw.PtrCast[u8, i32](raw.Offset(info, 8))) <= 0 {
            Redeliver(sig)
            return
        }
        addr := raw.Read(raw.PtrCast[u8, uint](raw.Offset(info, 24)))
        mc := raw.FromAddr[u8](raw.Read(raw.PtrCast[u8, uint](raw.Offset(uc, 48))))
        sp := raw.Read(raw.PtrCast[u8, uint](raw.Offset(mc, McontextSp())))
        pc := raw.Read(raw.PtrCast[u8, uint](raw.Offset(mc, McontextPc())))
        if !InCodeRegion(pc) && !FaultOwned(pc, sp) {
            Redeliver(sig)
            return
        }
        // The filters run first; a fault in a code region that no filter
        // resumes is kind 4, as on Linux.
        own: u32 = if sig == SigIll { 3 } else { 2 }
        if Filter(mc, own, addr, pc) {
            return
        }
        kind: u32 = if InCodeRegion(pc) { 4 } else { own }
        f := FaultInfo(kind: kind, addr: addr, pc: pc, sp: sp)
        // Edge: FaultHook's Swap to 1 came after the hook's bytes were written,
        // so a Load of 1 reads them.
        if Faults.set.Load() == 1 {
            h := raw.Read(raw.PtrCast[raw.Atomic[u64], fn(&FaultInfo): never](raw.Of(&Faults.hook)))
            h(&f)
        }
    }
    FatalWrite("panic: fault\n".AsBytes())
    Exit(101)
}

/// FatalWrite writes data to standard error, synchronously and unbuffered,
/// repeating a short write. Errors are ignored. O(len(data)); allocates
/// nothing.
export FatalWrite(data: []u8) {
    var off := 0
    for off < data.Len() {
        unsafe "data holds off + n bytes" {
            r := write(2, raw.Offset(raw.Data(data), off), Trunc[uint](data.Len() - off))
            if r <= 0 {
                if r == -1 && Errno() == 4 {
                    continue
                }
                return
            }
            off += r
        }
    }
}
