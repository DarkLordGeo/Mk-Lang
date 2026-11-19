// Why: a Mk library runs inside a host process that owns the executable,
// the signal handlers and the start stack, so it must not act as if it were
// the program.
//
// What: LibraryImage (the library's own image bounds), FaultUnhook,
// LibraryEnv (a start stack built from /proc/self/environ),
// LibraryOwnerHook, and Chain, which hands faults on to the host.
//
// How: the image bounds come from the library's own ELF header
// (MkImageBase, __ehdr_start), not the auxiliary vector. FaultHook saves the
// handlers it replaces, and a fault Mk does not own is chained to them
// instead of reported; FaultUnhook puts them back at the library's stop.

import std.raw

// MkImageBase is the address of the library's own ELF header
// (__ehdr_start), defined by the support pack of a library build only.
extern MkImageBase(): uint

// LibFaultState is a library's chaining state: on is 1 once LibraryImage
// ran (FaultHook then saves what it replaces), and old holds the kernel
// sigactions of SIGSEGV, SIGBUS, SIGILL and SIGPIPE it replaced, four
// words each (handler, flags, restorer, mask).
struct LibFaultState {
    on: raw.Atomic[u32]
    old: [16]raw.Atomic[u64]
}

var LibFaults: LibFaultState = LibFaultState(on: raw.Atomic[u32].New(0), old: [raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0)])

// LibSigs are the signals a library's FaultHook replaces, in old's order.
LibSigIndex(sig: uint): int {
    return match sig {
        11 { 0 }
        7 { 1 }
        4 { 2 }
        13 { 3 }
        _ { -1 }
    }
}

/// LibraryImage takes the calling library's image bounds from its own ELF
/// header, not the process's auxiliary vector, stores them for the fault
/// handler's ownership test, and marks the process's handlers as a
/// library's (FaultHook saves the handlers it replaces, and a fault Mk does
/// not own goes to them). Called once, by std.task.StartLibrary, before
/// FaultHook. O(program headers); allocates nothing.
export LibraryImage(): (lo: uint, hi: uint) {
    var lo: uint = 0
    var hi: uint = 0
    var soname := false
    unsafe "the library's own ELF header and program headers, mapped with it" {
        base := MkImageBase()
        b := raw.FromAddr[u8](base)
        phoff := raw.ReadUnaligned(raw.PtrCast[u8, u64](raw.Offset(b, 0x20)))
        phnum := int(raw.ReadUnaligned(raw.PtrCast[u8, u16](raw.Offset(b, 0x38))))
        // The bias: the header is in the first PT_LOAD, at offset 0.
        var bias: uint = base
        for j in 0..phnum {
            p := raw.Offset(b, Trunc[int](phoff) + j * 56)
            if raw.ReadUnaligned(raw.PtrCast[u8, u32](p)) == 1 && raw.ReadUnaligned(raw.PtrCast[u8, u64](raw.Offset(p, 8))) == 0 {
                bias = base - Trunc[uint](raw.ReadUnaligned(raw.PtrCast[u8, u64](raw.Offset(p, 16))))
                break
            }
        }
        for j in 0..phnum {
            p := raw.Offset(b, Trunc[int](phoff) + j * 56)
            ty := raw.ReadUnaligned(raw.PtrCast[u8, u32](p))
            if ty == 2 {
                // PT_DYNAMIC: a DT_SONAME (14) says the image is the
                // library's own shared object (its link names one); a host
                // executable that linked a static library has none.
                var d := raw.FromAddr[u8](Trunc[uint](raw.ReadUnaligned(raw.PtrCast[u8, u64](raw.Offset(p, 16)))) + bias)
                for {
                    tag := raw.ReadUnaligned(raw.PtrCast[u8, u64](d))
                    if tag == 0 {
                        break
                    }
                    if tag == 14 {
                        soname = true
                    }
                    d = raw.Offset(d, 16)
                }
            }
            if ty == 1 {
                a := Trunc[uint](raw.ReadUnaligned(raw.PtrCast[u8, u64](raw.Offset(p, 16)))) + bias
                e := a + Trunc[uint](raw.ReadUnaligned(raw.PtrCast[u8, u64](raw.Offset(p, 40))))
                if lo == 0 || a < lo {
                    lo = a
                }
                if e > hi {
                    hi = e
                }
            }
        }
    }
    // A static library's image is the host executable's, whose code is
    // not Mk's: the ownership bounds are then the empty
    // [1, 1), so no pc is Mk's by place and the thread and stack tests
    // decide (FaultOwned); 0 would mean unknown, where every fault is
    // Mk's. rt still gets the image, for fault sites.
    own_lo: uint = if soname { lo } else { 1 }
    own_hi: uint = if soname { hi } else { 1 }
    // Edge: stored before FaultHook installs the handler that reads them.
    Faults.img_hi.Store(u64(own_hi))
    // Edge: as img_hi's.
    Faults.img_lo.Store(u64(own_lo))
    // The calling thread is the host's, not carrier 0's (rt.Init, which ran
    // on it, recorded it): only the threads std starts are Mk's.
    Faults.main_tid.Store(0)
    // Edge: before FaultHook, which saves the old handlers once it reads 1.
    LibFaults.on.Store(1)
    return (lo: lo, hi: hi)
}

// ActionSave is Action that keeps the sigaction it replaces in old's slot
// for sig (a library's FaultHook).
ActionSave(sig: uint, handler: u64, flags: u64): bool {
    var act: [4]u64 = [0; 4]
    var prev: [4]u64 = [0; 4]
    act[0] = handler
    act[1] = flags | SaRestorer
    i := LibSigIndex(sig)
    unsafe "system call; act and prev are kernel sigactions" {
        act[2] = u64(raw.Addr(Restorer()))
        ok := !IsErr(Syscall(Nrs().rt_sigaction, sig, raw.Addr(raw.Data(act[..])), raw.Addr(raw.Data(prev[..])), 8, 0, 0))
        if ok && i >= 0 {
            for k in 0..4 {
                // Edge: stored before any fault the new handler takes reads
                // them (the system call above orders them for this thread;
                // other threads' faults follow the installation).
                LibFaults.old[i * 4 + k].Store(prev[k])
            }
        }
        return ok
    }
}

// Chain hands a fault Mk does not own to the handler the library replaced:
// a function handler is called, with siginfo and ucontext when it
// took SA_SIGINFO, and the fault then returns to the faulting code, so a
// handler that fixed the cause resumes it and one that did not sees it
// again; SIG_IGN returns; SIG_DFL, or no saved handler, gets the default
// action at once (Redeliver). Async-signal-safe.
Chain(sig: i32, info: *u8, uc: *u8) {
    i := LibSigIndex(Trunc[uint](sig))
    // Edge: LibraryImage's store and ActionSave's, both before the
    // handler that runs this was installed.
    if LibFaults.on.Load() == 0 || i < 0 {
        Redeliver(sig)
        return
    }
    // Edge: as on's.
    h := LibFaults.old[i * 4].Load()
    flags := LibFaults.old[i * 4 + 1].Load()
    if h == 1 {
        return
    }
    if h == 0 {
        Redeliver(sig)
        return
    }
    unsafe "h is the handler's code address the kernel gave back" {
        var w := h
        if (flags & SaSigInfo) != 0 {
            f := raw.Read(raw.PtrCast[u64, fn(i32, *u8, *u8)](raw.Of(&w)))
            f(sig, info, uc)
        } else {
            f := raw.Read(raw.PtrCast[u64, fn(i32)](raw.Of(&w)))
            f(sig)
        }
    }
}

/// FaultUnhook puts back the handlers a library's FaultHook replaced, at
/// the library's stop, each only while the installed one is still Mk's
/// (SigFault's, or SIG_IGN for SIGPIPE): a handler the host installed
/// after the start stays. After it no Mk handler runs. Outside
/// a library it does nothing. A host that changes a disposition while the
/// stop runs races it, as two sigaction callers do. O(1) system calls;
/// allocates nothing.
export FaultUnhook() {
    // Edge: LibraryImage's store, on the start path before the stop.
    if LibFaults.on.Load() == 0 {
        return
    }
    mine := HandlerWord(SigFault)
    for sig in [11, 7, 4, 13] {
        i := LibSigIndex(Trunc[uint](sig))
        var cur: [4]u64 = [0; 4]
        var act: [4]u64 = [0; 4]
        for k in 0..4 {
            // Edge: ActionSave stored them before the handler existed.
            act[k] = LibFaults.old[i * 4 + k].Load()
        }
        unsafe "system calls; cur and act are kernel sigactions" {
            if !IsErr(Syscall(Nrs().rt_sigaction, Trunc[uint](sig), 0, raw.Addr(raw.DataMut(mut cur[..])), 8, 0, 0)) {
                ours := if sig == 13 { cur[0] == 1 } else { cur[0] == mine }
                if ours {
                    _ = Syscall(Nrs().rt_sigaction, Trunc[uint](sig), raw.Addr(raw.Data(act[..])), 0, 8, 0, 0)
                }
            }
        }
    }
    // Edge: a later FaultHook installs again (a restart is not supported).
    Faults.set.Store(0)
}

/// LibraryEnv is a start stack for a library, which has none of its own
/// (Start's steps but argv): argc 0,
/// no arguments, the process's environment as envp, read from
/// /proc/self/environ, and an empty auxiliary vector, in a fresh mapping
/// kept until the process ends, for rt.Init, DecodeEnv and GetEnvRaw
/// (MK_CARRIERS and the other MK_ variables). The environment is the one
/// the process started with: a variable the host's C library set later is
/// not seen; with no procfs there is none. Null only when the mapping
/// cannot be had. O(n) in the
/// environment's bytes: two reads of the file; maps them once.
export LibraryEnv(): *u8 {
    var buf: [4096]u8 = [0; 4096]
    var total := 0
    var count := 0
    var last: u8 = 0
    unsafe "system calls; buf is this frame's" {
        // With no procfs the block is empty: no environment.
        r := Syscall(Nrs().openat, AtFdcwd, raw.Addr(raw.StrData("/proc/self/environ\u{0}")), ORdOnly | OCloExec, 0, 0, 0)
        if !IsErr(r) {
            fd := Trunc[uint](r)
            for {
                n := Syscall(Nrs().read, fd, raw.Addr(raw.DataMut(mut buf[..])), 4096, 0, 0, 0)
                if IsErr(n) || n == 0 {
                    break
                }
                k := Trunc[int](n)
                for j in 0..k {
                    if buf[j] == 0 {
                        count += 1
                    }
                }
                last = buf[k - 1]
                total += k
            }
            _ = Syscall(Nrs().close, fd, 0, 0, 0, 0, 0)
        }
    }
    if total > 0 && last != 0 {
        count += 1
    }
    // Words: argc, argv's null, count pointers, envp's null, AT_NULL's pair.
    words := count + 5
    size := (words * 8 + total + 1 + 4095) / 4096 * 4096
    p := Reserve(size, 4096)
    if raw.IsNull(p) || !Commit(p, size) {
        return raw.Null[u8]()
    }
    var got := 0
    unsafe "system calls into the fresh mapping, at most total bytes past its words" {
        data := raw.Offset(p, words * 8)
        r := Syscall(Nrs().openat, AtFdcwd, raw.Addr(raw.StrData("/proc/self/environ\u{0}")), ORdOnly | OCloExec, 0, 0, 0)
        if !IsErr(r) {
            fd := Trunc[uint](r)
            for got < total {
                n := Syscall(Nrs().read, fd, raw.Addr(raw.Offset(data, got)), Trunc[uint](total - got), 0, 0, 0)
                if IsErr(n) || n == 0 {
                    break
                }
                got += Trunc[int](n)
            }
            _ = Syscall(Nrs().close, fd, 0, 0, 0, 0, 0)
        }
        // The mapping is zero filled: a last string read short or without
        // its NUL ends at data[got], and argc, argv's null, envp's null and
        // AT_NULL are the fill's zeros.
        w := raw.PtrCast[u8, u64](p)
        var i := 2
        var at := 0
        for at < got && i < 2 + count {
            raw.Write(raw.Offset(w, i), u64(raw.Addr(raw.Offset(data, at))))
            i += 1
            for at < got && raw.Read(raw.Offset(data, at)) != 0 {
                at += 1
            }
            at += 1
        }
    }
    return p
}

/// LibraryOwnerHook has std/sys/windows's contract; here FaultOwnerHook's
/// hook already serves the handler (FaultOwned), so it does nothing. O(1);
/// allocates nothing.
export LibraryOwnerHook(f: fn(uint): bool) {
}
