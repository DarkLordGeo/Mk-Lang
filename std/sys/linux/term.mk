// Why: a program that put the terminal in raw mode must leave it usable when
// it ends, even by a fatal signal, and must set raw mode again after a
// SIGCONT; this is the std.sys half of std/rt/term.mk's terminal-restore
// registry.
//
// What: TermRestoreMode, TermRawAgain, TermVt, TermVtOn, TermVtBack and the
// record kinds TermKindTermios, TermKindConsole and TermKindTest.
//
// How: system calls only, no lock and no allocation, so the code is
// async-signal-safe and the fatal-signal path and the SIGCONT handler may
// call it. Raw mode is cfmakeraw's bits (TermMakeRaw), set only when this
// process's group is the terminal's foreground group.

import std.raw

/// TermKindTermios: mode holds a struct termios, restored with TCSETS.
export const TermKindTermios: u32 = 1
/// TermKindConsole: mode holds the Windows console's input and output modes.
export const TermKindConsole: u32 = 2
/// TermKindTest: mode's bytes are written to h (the registry's tests).
export const TermKindTest: u32 = 3

// TCSETS sets a terminal's attributes now.
const Tcsets: uint = 0x5402

// AltLeave is the sequence that leaves the alternate screen (xterm's 1049).
const AltLeave: str = "\x1b[?1049l"

/// TermRestoreMode puts back one saved mode: a termios on h (TCSETS, made
/// again on EINTR) for TermKindTermios, mode written to h for TermKindTest;
/// then, when alt, the leave-alternate-screen sequence written to out.
/// Failures are ignored: the process is ending and has nowhere to report
/// them. A console mode (TermKindConsole) has no meaning here and is
/// skipped. O(1) system calls; allocates nothing.
export TermRestoreMode(kind: u32, h: uint, out: uint, mode: []u8, alt: bool) {
    if kind == TermKindTermios && mode.Len() > 0 {
        unsafe "system call; mode holds a termios the registrant read with TCGETS" {
            for {
                r := Syscall(Nrs().ioctl, h, Tcsets, raw.Addr(raw.Data(mode)), 0, 0, 0)
                if r != -4 {
                    break
                }
            }
        }
    } else if kind == TermKindTest {
        _ = WriteSync(h, mode, -1)
    }
    if alt {
        _ = WriteSync(out, AltLeave.AsBytes(), -1)
    }
}

// TIOCGPGRP reads a terminal's foreground process group.
const Tiocgpgrp: uint = 0x540F

// TermRawTestMark is what TermRawAgain writes to h for a TermKindTest
// record (the registry's tests count it; Windows writes the same).
const TermRawTestMark: str = "TERMRAW\n"

/// TermRawAgain sets raw mode again on the terminal h for the registry's
/// SIGCONT hook, when kind is TermKindTermios and this process's
/// group is h's foreground group: TCGETS, cfmakeraw's bits (TermMakeRaw,
/// the ones TermMode's IoTermRaw sets), then TCSETS only when they change
/// the mode, so a continue that finds raw mode in force sets nothing. A
/// process continued in the background is left as it is, since its TCSETS
/// would stop it again with SIGTTOU. For TermKindTest it writes
/// TermRawTestMark to h. Failures are ignored. Async-signal-safe: system
/// calls only, no lock, no allocation. O(1): at most four system calls.
export TermRawAgain(kind: u32, h: uint) {
    if kind == TermKindTest {
        _ = WriteSync(h, TermRawTestMark.AsBytes(), -1)
        return
    }
    if kind != TermKindTermios || !TermForeground(h) {
        return
    }
    var t: [8]u64 = [0; 8]
    unsafe "system calls; t holds a struct termios" {
        q := raw.PtrCast[u64, u8](raw.DataMut(mut t[..]))
        if IsErr(Syscall(Nrs().ioctl, h, Tcgets, raw.Addr(q), 0, 0, 0)) {
            return
        }
        if !TermMakeRaw(q) {
            return
        }
        for {
            r := Syscall(Nrs().ioctl, h, Tcsets, raw.Addr(q), 0, 0, 0)
            if r != -4 {
                break
            }
        }
    }
}

// TermMakeRaw applies cfmakeraw's bits to the struct termios at q, as
// child.mk's TermMode does for IoTermRaw: IGNBRK BRKINT PARMRK ISTRIP INLCR
// IGNCR ICRNL IXON off; OPOST off; ECHO ECHONL ICANON ISIG IEXTEN off;
// CSIZE PARENB off, CS8 on; VMIN 1, VTIME 0. True when any of them changed
// the termios. O(1); allocates nothing.
TermMakeRaw(q: *u8): bool {
    unsafe "q holds a struct termios (TermiosBytes)" {
        i0 := Get32(q, 0)
        o0 := Get32(q, 4)
        c0 := Get32(q, 8)
        l0 := Get32(q, 12)
        i1 := i0 & ~u32(0x1 | 0x2 | 0x8 | 0x20 | 0x40 | 0x80 | 0x100 | 0x400)
        o1 := o0 & ~u32(0x1)
        c1 := (c0 & ~u32(0x30 | 0x100)) | 0x30
        l1 := l0 & ~u32(0x8 | 0x40 | 0x2 | 0x1 | 0x8000)
        vmin: u8 = raw.Read(raw.Offset(q, 17 + 6))
        vtime: u8 = raw.Read(raw.Offset(q, 17 + 5))
        if i1 == i0 && o1 == o0 && c1 == c0 && l1 == l0 && vmin == 1 && vtime == 0 {
            return false
        }
        Put32(q, 0, i1)
        Put32(q, 4, o1)
        Put32(q, 8, c1)
        Put32(q, 12, l1)
        raw.Write(raw.Offset(q, 17 + 6), 1)
        raw.Write(raw.Offset(q, 17 + 5), 0)
        return true
    }
}

// TermForeground says whether this process's group is the terminal h's
// foreground group (TIOCGPGRP against getpgid(0), whose number std.sys's
// table lacks: 121 on x86_64, 155 on aarch64, as Go's syscall tables
// give). False when h is not a terminal or has no foreground group.
// Async-signal-safe; O(1): two system calls; allocates nothing.
TermForeground(h: uint): bool {
    var pg: [2]i32 = [0; 2]
    getpgid: uint = if Target.arch == Arch.Arm64 { 155 } else { 121 }
    unsafe "system calls; pg receives the foreground group" {
        if IsErr(Syscall(Nrs().ioctl, h, Tiocgpgrp, raw.Addr(raw.DataMut(mut pg[..])), 0, 0, 0)) {
            return false
        }
        own := Syscall(getpgid, 0, 0, 0, 0, 0, 0)
        return !IsErr(own) && own > 0 && own == int(pg[0])
    }
}

/// TermVt answers whether the terminal h reads escape sequences: on Linux
/// every terminal does, so it is IsTerminal's answer, and nothing is
/// written (Windows turns on a console's virtual-terminal processing).
/// O(1): one system call; allocates nothing.
export TermVt(h: uint): bool {
    return IsTerminal(h)
}

/// TermVtOn answers 0: on Linux every terminal reads escape sequences, so
/// there is no output mode to turn on for the registry's writes (Windows
/// turns on a console's virtual-terminal processing for them).
/// O(1); allocates nothing.
export TermVtOn(kind: u32, out: uint): u64 {
    return 0
}

/// TermVtBack has nothing to put back on Linux (TermVtOn answers 0).
/// O(1); allocates nothing.
export TermVtBack(out: uint, held: u64) {
}
