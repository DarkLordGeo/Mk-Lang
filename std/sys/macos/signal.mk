// Why: programs wait on Ctrl-C, SIGTERM and SIGHUP as events, terminals
// must be restored on a fatal signal and after SIGCONT, and a pool thread
// blocked in a system call must be interruptible for a cancel.
//
// What: SignalWatch, SignalCount, Raise and ThreadInterrupt; the state and
// handlers behind term.mk's TermContinueInstall and child.mk's
// TermFatalInstall and RaiseFatal (SignalContinueInstall,
// SignalFatalInstall, SignalRaiseFatal).
//
// How: libSystem's sigaction (fault.mk's Action), with Darwin's numbers:
// SIGINT 2 is Interrupt, SIGTERM 15 Terminate, SIGHUP 1 Hangup, SIGWINCH 28
// Resize, SIGCONT 19 Continue. The counting handler only counts and wakes
// the poller (PollWake). A handler calls libSystem, which may set errno, so
// each one saves the interrupted thread's errno and puts it back.
// ThreadInterrupt sends SIGURG (16) with pthread_kill; its handler does
// nothing and is installed without SA_RESTART, so the interrupted thread's
// blocking call returns EINTR. Raise and RaiseFatal use kill(getpid()), a
// process-directed signal, not raise(3), which targets the calling thread.

import std.raw

struct SignalState {
    counts: [5]raw.Atomic[u64]
    // Two bits per handler (slot i at bits 2i, 2i + 1): 0 not installed,
    // 1 a thread is installing it, 2 installed. Slots 0 to 2 are the
    // watched signals 0 to 2, slot 3 SIGURG, slots 4 and 5 the watched
    // signals 3 and 4 (SlotOf).
    installed: raw.Atomic[u32]
    // fatal: the address of the terminal-restore registry's function
    // (SignalFatalInstall), 0 before; read by SigFatal.
    fatal: raw.Atomic[u64]
    // cont: the address of the registry's SIGCONT hook
    // (SignalContinueInstall), 0 before; read by SigCount.
    cont: raw.Atomic[u64]
}

var Signals: SignalState = SignalState(counts: [raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
    raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0)], installed: raw.Atomic[u32].New(0),
    fatal: raw.Atomic[u64].New(0), cont: raw.Atomic[u64].New(0))

// SigCont and SigUrg are Darwin's SIGCONT and SIGURG.
const SigCont: i32 = 19
const SigUrg: i32 = 16
const UrgSlot: u32 = 3

// SignalNumber maps 0 Interrupt, 1 Terminate, 2 Hangup, 3 Resize and 4
// Continue to Darwin's SIGINT, SIGTERM, SIGHUP, SIGWINCH and SIGCONT.
SignalNumber(sig: u32): i32 {
    return match sig {
        0 { 2 }
        1 { 15 }
        2 { 1 }
        3 { 28 }
        _ { 19 }
    }
}

// SlotOf is watched signal sig's slot in SignalState.installed.
SlotOf(sig: u32): u32 {
    return if sig <= 2 { sig } else { sig + 1 }
}

// IndexOf is the watched signal of the OS signal signo (SignalNumber's
// inverse); 4 for SIGCONT and any other.
IndexOf(signo: i32): int {
    return match signo {
        2 { 0 }
        15 { 1 }
        1 { 2 }
        28 { 3 }
        _ { 4 }
    }
}

// SigErrnoSave is the calling thread's errno, for a handler to put back
// with SigErrnoRestore before it returns. Async-signal-safe.
SigErrnoSave(): i32 {
    unsafe "__error is the calling thread's errno word" {
        return *__error()
    }
}

SigErrnoRestore(e: i32) {
    unsafe "__error is the calling thread's errno word" {
        *__error() = e
    }
}

/// SignalWatch installs the counting handler for sig (0 Interrupt, 1
/// Terminate, 2 Hangup, 3 Resize, 4 Continue) once; true when it is
/// installed. A caller that finds another thread installing waits for that
/// thread's outcome (returning true at once would answer before the
/// handler existed or after it failed to install).
/// O(1) system calls; allocates nothing.
export SignalWatch(sig: u32): bool {
    if sig > 4 {
        return false
    }
    return InstallOnce(SlotOf(sig), SignalNumber(sig), HandlerWord(SigCount), SaSigInfo | SaOnStack | SaRestart)
}

// InstallOnce installs handler for signo in slot's two bits, once per
// process. Exactly one caller installs (the Cas from 0 to 1); every other
// caller yields until the state is 2 (true) or back at 0 after a failure,
// and then tries itself, so no caller reports a handler that is not there.
// The installer's Cas to 2 comes after sigaction returned, and a waiter
// that loads 2 is ordered after it (sequentially consistent): the handler
// is installed when SignalWatch returns true on any thread.
InstallOnce(slot: u32, signo: i32, handler: u64, flags: u32): bool {
    shift := slot * 2
    mask: u32 = 3 << shift
    for {
        // Edge: the Cas from 0 admits one installer per signal; its Load only
        // seeds the Cas.
        old := Signals.installed.Load()
        st := (old >> shift) & 3
        if st == 2 {
            return true
        }
        if st == 0 {
            if !Signals.installed.Cas(old, old | (1 << shift)) {
                continue
            }
            ok := Action(signo, handler, flags)
            done: u32 = if ok { 2 << shift } else { 0 }
            for {
                // Edge: the Cas publishes the outcome (2 installed, 0 failed)
                // after the handler's sigaction.
                cur := Signals.installed.Load()
                if Signals.installed.Cas(cur, (cur & ~mask) | done) {
                    break
                }
            }
            return ok
        }
        ThreadYield()
    }
}

/// SignalCount is how many times signal sig has arrived since start; 0
/// for a signal the runtime does not count.
/// O(1); allocates nothing.
export SignalCount(sig: u32): u64 {
    // Edge: the handler's Add, before its PollWake; the poller's Load after the
    // wake sees it.
    return if sig > 4 { 0 } else { Signals.counts[sig].Load() }
}

/// Raise sends sig (0 Interrupt, 1 Terminate, 2 Hangup, 3 Resize, 4
/// Continue) to this process, for tests: kill(getpid(), n), so the kernel
/// delivers it to some thread that does not block it and SigCount runs
/// there, as for a signal from outside. false when sig is not watched (its
/// default action could end the process) or the call fails. The installed
/// state is loaded after SignalWatch's Cas to 2 (sequentially consistent),
/// so a watched signal finds its handler.
/// O(1): one system call; allocates nothing.
export Raise(sig: u32): bool {
    // Edge: InstallOnce's Cas to 2 came after sigaction installed the
    // handler.
    if sig > 4 || ((Signals.installed.Load() >> (SlotOf(sig) * 2)) & 3) != 2 {
        return false
    }
    unsafe "kill and getpid pass no memory" {
        return kill(getpid(), SignalNumber(sig)) == 0
    }
}

SigCount(sig: i32, info: *u8, uc: *u8) {
    e := SigErrnoSave()
    if sig == SigCont {
        // Before the count, so a task the count wakes finds raw mode set
        // again already.
        SigContinue()
    }
    // Edge: counters: each Add happens before the PollWake that follows it,
    // so the poller loop's SignalCount load after the wake sees it; nothing
    // else is published by them. Async-signal-safe: one atomic add.
    _ = Signals.counts[IndexOf(sig)].Add(1)
    PollWake()
    SigErrnoRestore(e)
}

// SigContinue calls the registry's SIGCONT hook (std.rt's TermContinue),
// once SignalContinueInstall stored it. Async-signal-safe: an atomic load
// and the hook, which makes system calls only.
SigContinue() {
    // Edge: SignalContinueInstall's Store, before it installed this handler
    // (or, when Read's watch installed it first, before any record whose
    // raw flag the hook reads was registered).
    var w: [1]u64 = [Signals.cont.Load()]
    if w[0] != 0 {
        unsafe "w holds the address SignalContinueInstall stored, a function value" {
            again := raw.Read(raw.PtrCast[u64, fn()](raw.Data(w[..])))
            again()
        }
    }
}

// SignalContinueInstall is TermContinueInstall (term.mk): SIGCONT calls
// again, std.rt's TermContinue, before the signal is counted. SIGCONT has
// one handler, SigCount, whether std.term's Read watch (SignalWatch(4)) or
// this call installs it first, and SigCount calls the hook once it is
// stored. O(1) system calls; allocates nothing.
SignalContinueInstall(again: fn()) {
    var w: [1]fn() = [again]
    unsafe "w holds one function value, read as its address" {
        // Edge: the Store publishes again before the handler that loads it
        // is installed (SignalWatch below), and before the registrant's
        // record turns Ready (rt's TermRegister).
        Signals.cont.Store(raw.Read(raw.PtrCast[fn(), u64](raw.Data(w[..]))))
    }
    _ = SignalWatch(4)
}

// SigNop is SIGURG's handler: its only effect is that a blocking system
// call of the thread returns EINTR (no SA_RESTART).
SigNop(sig: i32, info: *u8, uc: *u8) {
}

/// ThreadInterrupt makes a blocking system call of thread (the pthread_t
/// ThreadStart returned) return: pthread_kill with SIGURG, whose handler
/// is a no-op installed once without SA_RESTART. The call then fails with
/// EINTR, which every blocking call of std.sys reports as Canceled with
/// os 4 (MapError), except ReadSync and WriteSync, which retry it (use
/// ReadOnce and WriteOnce on a thread that can be interrupted). A signal
/// that arrives before the call blocks, or after it returned, interrupts
/// nothing: the caller re-sends (the early window) and the job checks its
/// own cancel flag (the late window). thread must still run (a pthread_t
/// is not checked for life: libSystem may reuse it once the thread ended,
/// so the caller interrupts only a thread it knows to be in its call).
/// false when the handler cannot be installed or pthread_kill refuses.
/// O(1): one system call; allocates nothing.
export ThreadInterrupt(thread: uint): bool {
    if !InstallOnce(UrgSlot, SigUrg, HandlerWord(SigNop), SaSigInfo | SaOnStack) {
        return false
    }
    unsafe "pthread_kill passes no memory; thread is a running thread's pthread_t" {
        return pthread_kill(u64(thread), SigUrg) == 0
    }
}

// SignalFatalInstall is TermFatalInstall (child.mk): Interrupt, Terminate
// and Hangup restore the terminal-restore registry's modes before their
// default action. restore is std.rt's TermRestoreAll, kept for SigFatal.
// Each of the three gets SigFatal unless it is watched (its counting
// handler stays) or ignored (an inherited SIG_IGN, as nohup's SIGHUP,
// stays). O(1) system calls; allocates nothing.
SignalFatalInstall(restore: fn()) {
    var w: [1]fn() = [restore]
    unsafe "w holds one function value, read as its address" {
        // Edge: the Store publishes restore before the handlers that load
        // it exist (sigaction below comes after it).
        Signals.fatal.Store(raw.Read(raw.PtrCast[fn(), u64](raw.Data(w[..]))))
    }
    for sig in 0..3 {
        signo := SignalNumber(Trunc[u32](sig))
        // Edge: a watched signal keeps its handler; one installed after this
        // load replaces SigFatal, and SigFatal itself defers to a watch.
        if ((Signals.installed.Load() >> (Trunc[u32](sig) * 2)) & 3) != 0 {
            continue
        }
        // cur receives the old struct sigaction (16 bytes): the handler
        // first, 1 for SIG_IGN.
        var cur: [2]u64 = [0; 2]
        unsafe "cur holds a struct sigaction of 16 bytes" {
            r := sigaction(signo, raw.Null[u8](), raw.PtrCast[u64, u8](raw.DataMut(mut cur[..])))
            if r != 0 || cur[0] == 1 {
                continue
            }
        }
        _ = Action(signo, HandlerWord(SigFatal), SaSigInfo | SaOnStack | SaRestart)
    }
}

// SigFatal is the fatal path of the terminal-restore registry: a signal
// whose watch was installed meanwhile is counted as SigCount counts it;
// else the registry's modes are put back, the default action is set and
// the signal sent again, so the process ends by it once this returns (the
// signal is blocked while its handler runs). Async-signal-safe: atomics,
// the registry's restores (system calls only), sigaction, getpid and kill.
SigFatal(sig: i32, info: *u8, uc: *u8) {
    // Edge: InstallOnce's Cas to 1 or 2; a watch being installed wins.
    if ((Signals.installed.Load() >> (SlotOf(Trunc[u32](IndexOf(sig))) * 2)) & 3) != 0 {
        SigCount(sig, info, uc)
        return
    }
    e := SigErrnoSave()
    // Edge: SignalFatalInstall's Store, before this handler existed.
    var w: [1]u64 = [Signals.fatal.Load()]
    if w[0] != 0 {
        unsafe "w holds the address SignalFatalInstall stored, a function value" {
            restore := raw.Read(raw.PtrCast[u64, fn()](raw.Data(w[..])))
            restore()
        }
    }
    _ = Action(sig, 0, 0)
    unsafe "kill and getpid pass no memory" {
        _ = kill(getpid(), sig)
    }
    SigErrnoRestore(e)
}

// SignalRaiseFatal is RaiseFatal (child.mk): sends Interrupt, Terminate or
// Hangup (sig 0 to 2) to this process whatever handles it, for the
// registry's tests: kill(getpid()), so an unwatched one takes SigFatal's
// path and the process ends by it. false for another sig or when the call
// fails. O(1): one system call; allocates nothing.
SignalRaiseFatal(sig: u32): bool {
    if sig > 2 {
        return false
    }
    unsafe "kill and getpid pass no memory" {
        return kill(getpid(), SignalNumber(sig)) == 0
    }
}
