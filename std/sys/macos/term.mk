// Why: a program that put the terminal in raw mode must leave it usable when
// it ends, even by a fatal signal, and must set raw mode again after a
// SIGCONT; this is the std.sys half of std/rt/term.mk's terminal-restore
// registry.
//
// What: TermRestoreMode, TermRawAgain, TermContinueInstall, TermVt,
// TermVtOn, TermVtBack and the record kinds TermKindTermios,
// TermKindConsole and TermKindTest.
//
// How: ioctl and getpgrp only, no lock and no allocation, so the code is
// async-signal-safe and the fatal-signal path and the SIGCONT handler may
// call it. Darwin's struct termios is 72 bytes (<sys/termios.h>, Go's
// ztypes_darwin_*.go, the same on arm64 and x86_64): c_iflag, c_oflag,
// c_cflag and c_lflag as u64 at 0, 8, 16 and 24, c_cc[20] at 32 (VMIN 16,
// VTIME 17), then c_ispeed at 56 and c_ospeed at 64; TIOCGETA reads it and
// TIOCSETA sets it now. The registry keeps at most 64 bytes of a mode, so a
// saved mode may stop before c_ospeed: TermRestoreMode lays the saved
// bytes over the terminal's current termios. Raw mode is cfmakeraw's bits
// (TermMakeRaw), set only when this process's group is the terminal's
// foreground group.

import std.raw

/// TermKindTermios: mode holds a struct termios (its first 64 bytes or
/// all 72), restored with TIOCSETA.
export const TermKindTermios: u32 = 1
/// TermKindConsole: mode holds the Windows console's input and output modes.
export const TermKindConsole: u32 = 2
/// TermKindTest: mode's bytes are written to h (the registry's tests).
export const TermKindTest: u32 = 3

// TIOCGETA, TIOCSETA and TIOCGPGRP of Darwin's <sys/ttycom.h>.
const TermTiocgeta: uint = 0x4048_7413
const TermTiocseta: uint = 0x8048_7414
const TermTiocgpgrp: uint = 0x4004_7477
// TermiosBytes is Darwin's sizeof(struct termios).
const TermiosBytes: int = 72
const TermEintr: i64 = 4

// AltLeave is the sequence that leaves the alternate screen (xterm's 1049).
const AltLeave: str = "\x1b[?1049l"

// TermSet is TIOCSETA of the termios in t on h, made again on EINTR; true
// when it was set. Async-signal-safe: ioctl only, and errno read on the
// same thread.
TermSet(h: uint, t: &[9]u64): bool {
    for {
        unsafe "ioctl reads the 72-byte termios in t" {
            if ioctl(Trunc[i32](h), TermTiocseta, raw.PtrCast[u64, u8](raw.Data(t[..]))) == 0 {
                return true
            }
        }
        if Errno() != TermEintr {
            return false
        }
    }
}

// TermGet is TIOCGETA of h into t; true when it was read.
TermGet(h: uint, t: mut [9]u64): bool {
    unsafe "ioctl writes the 72-byte termios into t" {
        return ioctl(Trunc[i32](h), TermTiocgeta, raw.PtrCast[u64, u8](raw.DataMut(mut t[..]))) == 0
    }
}

/// TermRestoreMode puts back one saved mode: for TermKindTermios, the
/// terminal h's current termios (TIOCGETA) with mode's bytes laid over its
/// start, set with TIOCSETA (made again on EINTR), so a mode of 64 bytes
/// keeps the current output speed; mode written to h for TermKindTest;
/// then, when alt, the leave-alternate-screen sequence written to out.
/// Failures are ignored: the process is ending and has nowhere to report
/// them. A console mode (TermKindConsole) has no meaning here and is
/// skipped. O(1) system calls; allocates nothing.
export TermRestoreMode(kind: u32, h: uint, out: uint, mode: []u8, alt: bool) {
    if kind == TermKindTermios && mode.Len() > 0 {
        var t: [9]u64 = [0; 9]
        if TermGet(h, mut t) {
            n := if mode.Len() < TermiosBytes { mode.Len() } else { TermiosBytes }
            unsafe "t holds TermiosBytes bytes and n is at most that" {
                q := raw.PtrCast[u64, u8](raw.DataMut(mut t[..]))
                for i in 0..n {
                    raw.Write(raw.Offset(q, i), mode[i])
                }
            }
            _ = TermSet(h, &t)
        }
    } else if kind == TermKindTest {
        _ = WriteSync(h, mode, -1)
    }
    if alt {
        _ = WriteSync(out, AltLeave.AsBytes(), -1)
    }
}

// TermRawTestMark is what TermRawAgain writes to h for a TermKindTest
// record (the registry's tests count it; every OS writes the same).
const TermRawTestMark: str = "TERMRAW\n"

/// TermRawAgain sets raw mode again on the terminal h for the registry's
/// SIGCONT hook, when kind is TermKindTermios and this process's group is
/// h's foreground group: TIOCGETA, cfmakeraw's bits (TermMakeRaw, the ones
/// TermMode's IoTermRaw sets), then TIOCSETA only when they change the
/// mode, so a continue that finds raw mode in force sets nothing. A
/// process continued in the background is left as it is, since its
/// TIOCSETA would stop it again with SIGTTOU. For TermKindTest it writes
/// TermRawTestMark to h. Failures are ignored. Async-signal-safe: ioctl and
/// getpgrp only, no lock, no allocation. O(1): at most four system calls.
export TermRawAgain(kind: u32, h: uint) {
    if kind == TermKindTest {
        _ = WriteSync(h, TermRawTestMark.AsBytes(), -1)
        return
    }
    if kind != TermKindTermios || !TermForeground(h) {
        return
    }
    var t: [9]u64 = [0; 9]
    if !TermGet(h, mut t) {
        return
    }
    if TermMakeRaw(mut t) {
        _ = TermSet(h, &t)
    }
}

// TermVmin and TermVtime are the byte offsets of c_cc[VMIN] and
// c_cc[VTIME] (c_cc at 32, VMIN 16, VTIME 17).
const TermVmin: int = 48
const TermVtime: int = 49

// TermMakeRaw applies cfmakeraw's bits to the termios in t, with Darwin's
// values (<sys/termios.h>): IGNBRK BRKINT PARMRK ISTRIP INLCR IGNCR ICRNL
// IXON off; OPOST off; ECHO ECHONL ICANON ISIG IEXTEN off; CSIZE PARENB
// off, CS8 on; VMIN 1, VTIME 0. True when any of them changed the termios.
// O(1); allocates nothing.
TermMakeRaw(t: mut [9]u64): bool {
    i1 := t[0] & ~u64(0x1 | 0x2 | 0x8 | 0x20 | 0x40 | 0x80 | 0x100 | 0x200)
    o1 := t[1] & ~u64(0x1)
    c1 := (t[2] & ~u64(0x300 | 0x1000)) | 0x300
    l1 := t[3] & ~u64(0x8 | 0x10 | 0x100 | 0x80 | 0x400)
    // c_cc[VMIN] and c_cc[VTIME] are bytes 0 and 1 of the little-endian
    // word t[6] (bytes 48 to 55).
    cc := t[TermVmin / 8]
    vmin := cc & 0xFF
    vtime := (cc >> 8) & 0xFF
    if i1 == t[0] && o1 == t[1] && c1 == t[2] && l1 == t[3] && vmin == 1 && vtime == 0 {
        return false
    }
    t[0] = i1
    t[1] = o1
    t[2] = c1
    t[3] = l1
    t[TermVmin / 8] = (cc & ~u64(0xFFFF)) | 1
    return true
}

// TermForeground says whether this process's group is the terminal h's
// foreground group (TIOCGPGRP against getpgrp). False when h is not a
// terminal or has no foreground group. Async-signal-safe; O(1): two system
// calls; allocates nothing.
TermForeground(h: uint): bool {
    var pg: [2]i32 = [0; 2]
    unsafe "ioctl writes one pid_t into pg; getpgrp passes no memory" {
        if ioctl(Trunc[i32](h), TermTiocgpgrp, raw.PtrCast[i32, u8](raw.DataMut(mut pg[..]))) != 0 {
            return false
        }
        own := getpgrp()
        return own > 0 && own == pg[0]
    }
}

/// TermContinueInstall makes SIGCONT call again, std.rt's TermContinue,
/// which sets raw mode again on the registry's raw records, before the
/// signal is counted: SIGCONT has one handler, signal.mk's SigCount,
/// whether std.term's Read watch (SignalWatch(4)) or this call installs it
/// first, and SigCount calls the hook once it is stored. Called once, at
/// the registry's first record, so a program that registers none installs
/// no SIGCONT handler. O(1) system calls; allocates nothing.
export TermContinueInstall(again: fn()) {
    SignalContinueInstall(again)
}

/// TermVt answers whether the terminal h reads escape sequences: on macOS
/// every terminal does, so it is IsTerminal's answer, and nothing is
/// written (Windows turns on a console's virtual-terminal processing).
/// O(1): one system call; allocates nothing.
export TermVt(h: uint): bool {
    return IsTerminal(h)
}

/// TermVtOn answers 0: on macOS every terminal reads escape sequences, so
/// there is no output mode to turn on for the registry's writes (Windows
/// turns on a console's virtual-terminal processing for them).
/// O(1); allocates nothing.
export TermVtOn(kind: u32, out: uint): u64 {
    return 0
}

/// TermVtBack has nothing to put back on macOS (TermVtOn answers 0).
/// O(1); allocates nothing.
export TermVtBack(out: uint, held: u64) {
}
