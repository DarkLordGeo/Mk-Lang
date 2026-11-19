// Why: Mk's Signal.Interrupt and Terminate must reach a Windows program,
// which gets console control events instead of signals.
//
// What: SignalWatch, SignalCount, Raise, TermFatalInstall and RaiseFatal,
// over console control handlers.
//
// How: Ctrl+C is Interrupt; Ctrl+Break, close, logoff and shutdown are
// Terminate; Hangup never fires. A watched signal loses its default action.

import std.raw

struct SignalState {
    installed: raw.Atomic[u32]
    watched: raw.Atomic[u32]
    ctrlc: raw.Atomic[u32]
    interrupt: raw.Atomic[u64]
    terminate: raw.Atomic[u64]
}

var Signals: SignalState = SignalState(installed: raw.Atomic[u32].New(0), watched: raw.Atomic[u32].New(0),
    ctrlc: raw.Atomic[u32].New(0), interrupt: raw.Atomic[u64].New(0), terminate: raw.Atomic[u64].New(0))

/// SignalWatch installs the counting handler for sig (0 Interrupt,
/// 1 Terminate, 2 Hangup) once; true when it is installed. A caller that
/// finds another thread installing waits for that thread's outcome.
/// Watching Interrupt also enables Ctrl+C for the process (EnableCtrlC).
/// O(1) system calls; allocates nothing.
export SignalWatch(sig: u32): bool {
    if !InstallWatch(sig) {
        return false
    }
    return if sig == 0 { EnableCtrlC() } else { true }
}

// InstallWatch marks sig watched and installs the handler once.
InstallWatch(sig: u32): bool {
    if sig > 2 {
        return false
    }
    // watched: a bit set by Cas; the handler reads it on the system's
    // thread, after SetConsoleCtrlHandler (a system call) published the
    // handler, so a watched signal is seen as watched.
    // Edge: the Cas sets the bit before SetConsoleCtrlHandler (a system call)
    // publishes the handler.
    var old := Signals.watched.Load()
    for !Signals.watched.Cas(old, old | (1 << sig)) {
        old = Signals.watched.Load()
    }
    // installed: 0 none, 1 installing, 2 installed. A failed install goes
    // back to 0, so a later call tries again rather than report a watch
    // that does not exist; a call that finds another thread installing
    // waits for its outcome.
    for {
        // Edge: the Cas from 0 admits one installer; its Store of 2 after
        // SetConsoleCtrlHandler publishes the handler, and a Store of 0 lets
        // the next try again.
        st := Signals.installed.Load()
        if st == 2 {
            return true
        }
        if st == 0 && Signals.installed.Cas(0, 1) {
            _ = TestPoint(1)
            unsafe "kernel32; MkCtrlHandler is a static function, alive for the whole process" {
                if SetConsoleCtrlHandler(some(MkCtrlHandler), 1) != 0 {
                    // Edge: the Store of 2 publishes the installed handler to
                    // Raise's and SignalWatch's Loads.
                    Signals.installed.Store(2)
                    return true
                }
            }
            // Edge: the Store of 0 lets the next caller's Cas try again.
            Signals.installed.Store(0)
            return false
        }
        ThreadYield()
    }
}

// EnableCtrlC clears an inherited Ctrl+C ignore, once per process: a
// process started with CREATE_NEW_PROCESS_GROUP, or by one that called
// SetConsoleCtrlHandler(NULL, TRUE), has Ctrl+C disabled, and
// Windows drops the event before any handler runs, so a watched Interrupt
// would never arrive. SetConsoleCtrlHandler(NULL, FALSE) is the documented
// way back. Linux's rule is the same: a handler installed for SIGINT
// replaces an inherited SIG_IGN. It runs after the handler is installed,
// so a Ctrl+C that the call enables finds the handler and never takes the
// default action. ctrlc: 0 not done, 1 a thread is calling, 2 done; a
// failed call goes back to 0 (the next watch tries again) and the watch
// reports false. The atomics are sequentially consistent: the Store of 2
// follows the system call, so a caller that loads 2 returns after Ctrl+C
// was enabled (the edge is that call to every later true).
EnableCtrlC(): bool {
    for {
        st := Signals.ctrlc.Load()
        if st == 2 {
            return true
        }
        if st == 0 && Signals.ctrlc.Cas(0, 1) {
            unsafe "kernel32; removing the handler passes no memory" {
                if SetConsoleCtrlHandler(none, 0) != 0 {
                    // Edge: as installed's: a Cas from 0 admits one, and the
                    // Store publishes the outcome.
                    Signals.ctrlc.Store(2)
                    return true
                }
            }
            Signals.ctrlc.Store(0)
            return false
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
    return match sig {
        0 { Signals.interrupt.Load() }
        1 { Signals.terminate.Load() }
        _ { 0 }
    }
}

/// Raise delivers sig (0 Interrupt, 1 Terminate) to this process, for tests:
/// it runs the installed handler on the calling thread with
/// CTRL_C_EVENT or CTRL_BREAK_EVENT, which counts and wakes the poller as
/// the system's handler thread would. There is no in-process console event:
/// GenerateConsoleCtrlEvent reaches every process attached to the console
/// (the test runner too). false for Hangup (never delivered) and for
/// a signal that is not watched or whose handler is not installed (the
/// installed and watched loads are after SignalWatch's stores: sequentially
/// consistent).
/// O(1): one system call; allocates nothing.
export Raise(sig: u32): bool {
    // Edge: SignalWatch's Store of 2, after SetConsoleCtrlHandler.
    return if sig > 1 || Signals.installed.Load() != 2 { false } else { MkCtrlHandler(sig) == 1 }
}

// MkCtrlHandler runs on a thread the system creates (a foreign thread).
MkCtrlHandler(ctrl: u32): i32 {
    sig: u32 = if ctrl == 0 { 0 } else { 1 }
    // Edge: SignalWatch's Cas set the bit before the handler was installed.
    if (Signals.watched.Load() & (1 << sig)) == 0 {
        return 0
    }
    // Counters: the Add happens before PollWake, and the poller loop's
    // SignalCount load after its wake sees it (all sequentially
    // consistent); nothing else is published by them.
    if sig == 0 {
        // Edge: each Add is before the handler's PollWake; nothing else is
        // published by it.
        _ = Signals.interrupt.Add(1)
    } else {
        _ = Signals.terminate.Add(1)
    }
    PollWake()
    if ctrl >= 2 {
        // Close, logoff, shutdown: the process ends when this returns.
        unsafe "a kernel32 call that passes no memory, only values and handles" {
            Sleep(0xFFFF_FFFF)
        }
    }
    return 1
}

// Fatal is the terminal-restore registry's function (TermFatalInstall), 0
// before; read by TermCtrlHandler.
var Fatal: raw.Atomic[u64] = raw.Atomic[u64].New(0)

/// TermFatalInstall makes Ctrl+C, Ctrl+Break, the console's close, logoff
/// and shutdown put back the terminal-restore registry's modes before the
/// default handler ends the process: restore is std.rt's
/// TermRestoreAll. The handler goes before every other (the last one added
/// runs first) and returns FALSE, so a watched Interrupt or Terminate still
/// reaches MkCtrlHandler, which handles it; then nothing is restored, since
/// the process is not ending. Called once, at the registry's first record.
/// O(1) system calls; allocates nothing.
export TermFatalInstall(restore: fn()) {
    var w: [1]fn() = [restore]
    unsafe "w holds one function value, read as its address; kernel32" {
        // Edge: the Store publishes restore before SetConsoleCtrlHandler (a
        // system call) makes the handler reachable.
        Fatal.Store(raw.Read(raw.PtrCast[fn(), u64](raw.Data(w[..]))))
        _ = SetConsoleCtrlHandler(some(TermCtrlHandler), 1)
    }
}

// TermCtrlHandler runs on a thread the system creates. A watched event is
// MkCtrlHandler's (FALSE passes it on); any other ends the process, so the
// modes go back first.
TermCtrlHandler(ctrl: u32): i32 {
    sig: u32 = if ctrl == 0 { 0 } else { 1 }
    // Edge: SignalWatch's Cas set the bit before MkCtrlHandler was installed.
    if (Signals.watched.Load() & (1 << sig)) != 0 {
        return 0
    }
    // Edge: TermFatalInstall's Store, before this handler was added.
    var w: [1]u64 = [Fatal.Load()]
    if w[0] != 0 {
        unsafe "w holds the address TermFatalInstall stored, a function value" {
            restore := raw.Read(raw.PtrCast[u64, fn()](raw.Data(w[..])))
            restore()
        }
    }
    return 0
}

/// RaiseFatal runs the console control handler's fatal path for sig (0
/// Ctrl+C, else the console's close) as the system's thread would, then
/// ends the process with 143, the code Linux's shell gives a death by
/// SIGTERM, for the registry's tests. false for sig above 2.
/// O(1); allocates nothing.
export RaiseFatal(sig: u32): bool {
    if sig > 2 {
        return false
    }
    ctrl: u32 = if sig == 0 { 0 } else { 2 }
    _ = TermCtrlHandler(ctrl)
    unsafe "a kernel32 call that passes no memory, only values and handles" {
        ExitProcess(143)
    }
}
