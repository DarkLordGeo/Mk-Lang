// Why: a memory fault in Mk code must become a clear report (a stack
// overflow, a fault in a code region, a bad access) instead of a silent
// kill, and the runtime must tell its own faults from those of foreign code.
//
// What: the SIGSEGV, SIGBUS and SIGILL handlers on the alternate signal
// stack, SIGPIPE ignored, FatalWrite, FaultOwned and the fault hook state.
//
// How: a SIGSEGV or SIGBUS becomes a FaultInfo of kind 2 and a SIGILL one of
// kind 3 (as Windows' EXCEPTION_ILLEGAL_INSTRUCTION), handed to the hook;
// rt.OnFault makes kind 2 kind 1 when the address is a guard page it owns
// (std.sys does not know the slot regions). The filters (filter.mk) see each
// with the same kind, so runtime.FaultKind.Illegal exists on both targets.

import std.raw

// FaultState: the hook and its set word; the image's bounds (ImageBounds
// stores them); the ownership hook's fn bytes and its set word
// (FaultOwnerHook).
struct FaultState {
    hook: raw.Atomic[u64]
    set: raw.Atomic[u32]
    img_lo: raw.Atomic[u64]
    img_hi: raw.Atomic[u64]
    own: raw.Atomic[u64]
    own_set: raw.Atomic[u32]
    // main_tid is the main thread's id (ImageBounds, at rt.Init), 0
    // before: ThreadMade counts the main thread as Mk's.
    main_tid: raw.Atomic[u32]
}

var Faults: FaultState = FaultState(hook: raw.Atomic[u64].New(0), set: raw.Atomic[u32].New(0), img_lo: raw.Atomic[u64].New(0),
    img_hi: raw.Atomic[u64].New(0), own: raw.Atomic[u64].New(0), own_set: raw.Atomic[u32].New(0),
    main_tid: raw.Atomic[u32].New(0))

const SaSigInfo: u64 = 0x4
const SaOnStack: u64 = 0x0800_0000
const SaRestart: u64 = 0x1000_0000
const SaRestorer: u64 = 0x0400_0000
// SaNoDefer leaves SIGSEGV and SIGBUS unblocked while the handler runs.
// OnFault never returns, so without it a second fault on the same thread
// (inside the report's flush) would arrive blocked, and the kernel would
// kill the process with the default action instead of letting rt's nested
// report path exit 101 with the first report.
const SaNoDefer: u64 = 0x4000_0000
// SIGILL is 4 on x86_64 and aarch64 (asm-generic/signal.h).
const SigIll: uint = 4
const SigBus: uint = 7
const SigSegv: uint = 11
const SigPipe: uint = 13

// Action installs a handler (a C-ABI function, or SIG_IGN as 1) for sig.
Action(sig: uint, handler: u64, flags: u64): bool {
    var act: [4]u64 = [0; 4]
    act[0] = handler
    act[1] = flags | SaRestorer
    unsafe "system call; act is a kernel sigaction" {
        act[2] = u64(raw.Addr(Restorer()))
        return !IsErr(Syscall(Nrs().rt_sigaction, sig, raw.Addr(raw.Data(act[..])), 0, 8, 0, 0))
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
/// nothing: a SIGSEGV handler is process-wide and has no first-chance
/// phase, so C code that handles its own faults installs its own handler,
/// which replaces std's.
/// O(1); allocates nothing.
export ExternFaultHook(f: fn(uint): uint) {
}

/// FuncRowHook has std/sys/windows's contract, where the vectored handler
/// tells Mk's code from C's by the rows. Here it does nothing, as
/// ExternFaultHook.
/// O(1); allocates nothing.
export FuncRowHook(rows: *u8, n: int) {
}

/// FaultOwnerHook installs f, which answers whether a stack pointer lies on
/// a stack Mk runs on (a task slot, a carrier's system stack): std.task's,
/// at start. With it the handler routes only the faults Mk owns
/// (FaultOwned), as Windows' frame walk does; before it, and in a program
/// with no task, every fault is taken as Mk's.
/// O(1); allocates nothing.
export FaultOwnerHook(f: fn(uint): bool) {
    unsafe "the hook word is an atomic's memory" {
        raw.Write(raw.PtrCast[raw.Atomic[u64], fn(uint): bool](raw.Of(&Faults.own)), f)
    }
    // Edge: the Store publishes the hook's bytes, written above, to the
    // handler's Load.
    Faults.own_set.Store(1)
}

/// FaultOwned is the handler's ownership test, Windows' FaultOwner in the
/// form a signal handler can make: a fault is Mk's when its pc is in the
/// executable's image (Mk's code, with the package's own C sources), when
/// its thread is the main thread (carrier 0's) or one std.sys started (a
/// carrier, a pool thread: Mk code called whatever runs there), or when its stack pointer is on a task
/// slot or a carrier's system stack (FaultOwnerHook's answer). Otherwise
/// it is C code outside the image on a thread Mk did not make (a foreign
/// library's own thread), which on Windows its own handlers or the
/// system's default get: the handler gives the signal its default action
/// at once (Redeliver), so the fault ends the process as it would without
/// Mk. A fault
/// in a std.vm code region is routed before this test. Async-signal-safe:
/// loads, and one gettid for ThreadMade. O(512) at worst; allocates
/// nothing.
export FaultOwned(pc: uint, sp: uint): bool {
    // Edge: as FaultOwnerHook's Store; before it every fault is Mk's.
    if Faults.own_set.Load() == 0 {
        return true
    }
    // A pc in the image is Mk's whatever the thread: ThreadMade's gettid
    // and table scan are paid only for a pc outside it.
    // Edge: as FaultOwnedBy's loads.
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
    // Edge: ImageBounds stored them before any thread std starts; 0 is
    // unknown, and then every fault is Mk's.
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
    // The hook is written before rt_sigaction installs the handler that
    // reads it (the system call orders it, after a sequentially consistent
    // Swap that admits one installer), so a fault on any thread reads h.
    // Edge: the Swap admits one installer; the hook's bytes, written above,
    // come before it.
    if Faults.set.Swap(1) == 0 {
        w := HandlerWord(SigFault)
        if LibFaults.on.Load() != 0 {
            // A library keeps what it replaces, to chain to it (lib.mk).
            _ = ActionSave(SigSegv, w, SaSigInfo | SaOnStack | SaNoDefer)
            _ = ActionSave(SigBus, w, SaSigInfo | SaOnStack | SaNoDefer)
            _ = ActionSave(SigIll, w, SaSigInfo | SaOnStack | SaNoDefer)
            _ = ActionSave(SigPipe, 1, 0)
            return
        }
        _ = Action(SigSegv, w, SaSigInfo | SaOnStack | SaNoDefer)
        _ = Action(SigBus, w, SaSigInfo | SaOnStack | SaNoDefer)
        _ = Action(SigIll, w, SaSigInfo | SaOnStack | SaNoDefer)
        _ = Action(SigPipe, 1, 0)
    }
}

// UcontextSp and UcontextPc are the offsets of the stack pointer and the
// program counter in ucontext_t: x86_64 REG_RSP
// and REG_RIP of uc_mcontext.gregs, aarch64 sp and pc of uc_mcontext.
UcontextSp(): int {
    return if Target.arch == Arch.Arm64 { 432 } else { 160 }
}

UcontextPc(): int {
    return if Target.arch == Arch.Arm64 { 440 } else { 168 }
}

// Redeliver gives sig its default action now: the disposition is restored
// to SIG_DFL and the signal sent again to the calling thread (tgkill;
// SA_NODEFER leaves it unblocked in the handler), so it is delivered as the
// handler returns and ends the process by that signal. Mk's handler is
// never left uninstalled for a later fault Mk owns. Async-signal-safe:
// four system calls.
Redeliver(sig: i32) {
    _ = Action(Trunc[uint](sig), 0, 0)
    pid := Trunc[uint](Syscall(Nrs().getpid, 0, 0, 0, 0, 0, 0))
    tid := Trunc[uint](Syscall(Nrs().gettid, 0, 0, 0, 0, 0, 0))
    _ = Syscall(Nrs().tgkill, pid, tid, Trunc[uint](sig), 0, 0, 0)
}

// SigFault runs on the alternate signal stack, for SIGSEGV, SIGBUS and
// SIGILL. A SIGILL's si_addr is the faulting instruction's address.
SigFault(sig: i32, info: *u8, uc: *u8) {
    unsafe "info and uc are the kernel's siginfo_t and ucontext_t" {
        // si_code (siginfo_t's third int): 0 or below is a signal another
        // process or thread sent (SI_USER from kill, SI_QUEUE, SI_TKILL
        // from tgkill), not a fault: no instruction faulted, and offset 16
        // holds the sender's pid and uid, not an address. It is not Mk's
        // fault and reaches neither the filters nor Mk's report: it gets
        // the signal's default action, raised again at once (Redeliver),
        // so the process ends by it exactly as it would without Mk.
        if raw.Read(raw.PtrCast[u8, i32](raw.Offset(info, 8))) <= 0 {
            Chain(sig, info, uc) // Redeliver, or a library's replaced handler (lib.mk)
            return
        }
        addr := raw.Read(raw.PtrCast[u8, uint](raw.Offset(info, 16)))
        sp := raw.Read(raw.PtrCast[u8, uint](raw.Offset(uc, UcontextSp())))
        pc := raw.Read(raw.PtrCast[u8, uint](raw.Offset(uc, UcontextPc())))
        // A fault Mk does not own (FaultOwned: C code outside the image on
        // a thread Mk did not make) reaches neither the filters nor Mk's
        // report, as on Windows: the default action is restored and the
        // signal raised again on this thread (tgkill; SA_NODEFER leaves it
        // unblocked), so the process ends by it now, as it would without
        // Mk, even when the fault would not recur on its own (another
        // thread fixed the page first): the default action is never left
        // installed for a later fault Mk owns. A fault in a code region is
        // Mk's to route.
        if !InCodeRegion(pc) && !FaultOwned(pc, sp) {
            Chain(sig, info, uc) // Redeliver, or a library's replaced handler (lib.mk)
            return
        }
        // runtime.AddFaultFilter's filters run first (filter.mk); a
        // resume returns, and the kernel's sigreturn installs the context
        // the filter edited. A fault in a std.vm code region that no filter
        // resumes is kind 4: std.rt reports it as a fault in a code region,
        // exit 101, as on Windows, unless its address is a guard page (a
        // stack overflow, reported as one wherever its pc is).
        own: u32 = if Trunc[uint](sig) == SigIll { 3 } else { 2 }
        if Filter(uc, own, addr, pc) {
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

/// FatalWrite writes data to standard error, synchronously and unbuffered.
/// Errors are ignored. O(len(data)); allocates nothing.
export FatalWrite(data: []u8) {
    _ = WriteSync(2, data, -1)
}
