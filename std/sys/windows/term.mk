// Why: a program that changes the console's modes (raw input, the
// alternate screen) must leave the console as it found it, even when it
// ends by Ctrl+C or a panic.
//
// What: the std.sys half of the terminal-restore registry (std/rt/term.mk)
// on Windows: TermRestoreMode, TermRawAgain, TermContinueInstall, and
// TermVt, TermVtOn and TermVtBack for virtual-terminal processing.
//
// How: putting back the saved console modes as a process ends, with no
// lock and no allocation, so the console control handler's thread may call
// it. TermVt turns on virtual-terminal processing for term.UseColor.

import std.raw

/// TermKindTermios: a Linux termios; skipped here.
export const TermKindTermios: u32 = 1
/// TermKindConsole: mode holds the console input handle's mode (bytes 0
/// to 3) and the output handle's (bytes 4 to 7), little-endian.
export const TermKindConsole: u32 = 2
/// TermKindTest: mode's bytes are written to h (the registry's tests).
export const TermKindTest: u32 = 3

// AltLeave is the sequence that leaves the alternate screen (xterm's 1049,
// which the console honours with virtual-terminal processing on).
const AltLeave: str = "\x1b[?1049l"

/// TermRestoreMode puts back one saved mode: for TermKindConsole,
/// SetConsoleMode on h and on out with the two saved modes; for
/// TermKindTest, mode written to h; then, when alt, the
/// leave-alternate-screen sequence written to out (before the output mode
/// is restored, while virtual-terminal processing is still on). Failures
/// are ignored: the process is ending. O(1) system calls; allocates
/// nothing.
export TermRestoreMode(kind: u32, h: uint, out: uint, mode: []u8, alt: bool) {
    if alt {
        _ = WriteSync(out, AltLeave.AsBytes(), -1)
    }
    if kind == TermKindConsole && mode.Len() >= 8 {
        m0 := u32(mode[0]) | (u32(mode[1]) << 8) | (u32(mode[2]) << 16) | (u32(mode[3]) << 24)
        m1 := u32(mode[4]) | (u32(mode[5]) << 8) | (u32(mode[6]) << 16) | (u32(mode[7]) << 24)
        unsafe "system calls on the console handles the registrant gave" {
            _ = SetConsoleMode(h, m0)
            _ = SetConsoleMode(out, m1)
        }
    } else if kind == TermKindTest {
        _ = WriteSync(h, mode, -1)
    }
}

// TermRawTestMark is what TermRawAgain writes to h for a TermKindTest
// record, as on Linux (the registry's tests count it).
const TermRawTestMark: str = "TERMRAW\n"

/// TermRawAgain has std/sys/linux's contract: Windows sends no
/// SIGCONT, so a console record is left as it is; a TermKindTest record
/// gets TermRawTestMark written to h, so the registry's tests run here
/// too. O(1); allocates nothing.
export TermRawAgain(kind: u32, h: uint) {
    if kind == TermKindTest {
        _ = WriteSync(h, TermRawTestMark.AsBytes(), -1)
    }
}

/// TermContinueInstall has std/sys/linux's contract; Windows has no
/// SIGCONT, so there is nothing to install. O(1); allocates nothing.
export TermContinueInstall(again: fn()) {
}

// OutputVt is ENABLE_VIRTUAL_TERMINAL_PROCESSING, the output mode bit with
// which a console reads escape sequences rather than printing them. On an
// input handle the same bit is ENABLE_ECHO_INPUT, so TermVt writes it only
// on a screen buffer.
const OutputVt: u32 = 0x4

/// TermVt answers whether the console screen buffer h reads escape
/// sequences (ENABLE_VIRTUAL_TERMINAL_PROCESSING in its output mode),
/// turning the bit on when it is off, as Rust's anstyle-query and
/// crossterm do. False when h is no console, when the bit is off
/// and h is no screen buffer (GetConsoleScreenBufferInfo refuses a console
/// input handle, which is then never written; one whose echo is on answers
/// true, since 0x4 is ENABLE_ECHO_INPUT there: a standard output stream
/// that is the console's input buffer answers by its echo bit, and StdVt
/// cannot tell), or when SetConsoleMode refuses the bit (a classic console
/// without virtual-terminal support, or a handle without write access).
/// Nothing is written when the bit is already on, and a bit TermVt turned
/// on is left on: nothing puts it back as the process ends. The mode is
/// read and written back with the bit (the console API has no
/// compare-and-set), so a mode another thread sets between the two calls
/// is overwritten.
/// O(1): one system call when the bit is on, three when it is off (on a
/// console that refuses the bit, three on every call); allocates nothing.
export TermVt(h: uint): bool {
    var mode: u32 = 0
    var info: [3]u64 = [0; 3]
    unsafe "kernel32; mode and info are in this frame (info holds a CONSOLE_SCREEN_BUFFER_INFO, 22 bytes)" {
        if GetConsoleMode(h, raw.OfMut(mut mode)) == 0 {
            return false
        }
        if mode & OutputVt != 0 {
            return true
        }
        if GetConsoleScreenBufferInfo(h, raw.PtrCast[u64, u8](raw.DataMut(mut info[..]))) == 0 {
            return false
        }
        return SetConsoleMode(h, mode | OutputVt) != 0
    }
}

/// TermVtOn turns on virtual-terminal processing on the console screen
/// buffer out of a TermKindConsole record for the writes that follow (the
/// registry's and Term.Leave's mouse-off, kitty pops and alternate-screen
/// leave), when its output mode lacks the bit: a record whose mode
/// Restore already put back, or one with no saved mode, writes in whatever
/// mode is in force, and without the bit the sequences print as text and
/// pop nothing. It answers what TermVtBack needs: 0 when nothing changed
/// (another kind, no console, no screen buffer, the bit on already, or
/// SetConsoleMode refused it), else the old mode with bit 32 set. Untested:
/// no block drives a console whose output mode lacks the bit. No lock and no allocation, so the console control
/// handler's thread may call it. O(1): one system call when the bit is on,
/// three when it is off.
export TermVtOn(kind: u32, out: uint): u64 {
    if kind != TermKindConsole {
        return 0
    }
    var mode: u32 = 0
    var info: [3]u64 = [0; 3]
    unsafe "kernel32 on the output handle the registrant gave; mode and info are in this frame (info holds a CONSOLE_SCREEN_BUFFER_INFO, 22 bytes)" {
        if GetConsoleMode(out, raw.OfMut(mut mode)) == 0 {
            return 0
        }
        if mode & OutputVt != 0 {
            return 0
        }
        if GetConsoleScreenBufferInfo(out, raw.PtrCast[u64, u8](raw.DataMut(mut info[..]))) == 0 {
            return 0
        }
        if SetConsoleMode(out, mode | OutputVt) == 0 {
            return 0
        }
    }
    return (u64(1) << 32) | u64(mode)
}

/// TermVtBack puts back the output mode TermVtOn changed: held is
/// TermVtOn's answer for out, and 0 does nothing. A failure is ignored.
/// O(1): at most one system call; allocates nothing.
export TermVtBack(out: uint, held: u64) {
    if (held >> 32) == 0 {
        return
    }
    unsafe "kernel32 on the output handle TermVtOn changed" {
        _ = SetConsoleMode(out, Trunc[u32](held))
    }
}
