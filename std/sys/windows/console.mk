// Why: a Windows console shows UTF-8 bytes correctly only when they are
// written as UTF-16 through WriteConsoleW, and a write may end in the middle
// of a UTF-8 sequence.
//
// What: WriteConsole (with SeqLen, Decode8, Units and FlushUnits) and
// ConsoleTestType.
//
// How: UTF-8 is converted to UTF-16 and written with WriteConsoleW; an
// incomplete sequence at the end of a write is carried in the caller's
// ConsoleCarry to the next write. Any other handle gets the bytes exactly;
// \n is never translated.

import std.raw

// SeqLen is the length a UTF-8 sequence led by b has, or 0 for a byte that
// cannot lead one.
SeqLen(b: u32): int {
    if b < 0x80 {
        return 1
    }
    if b >= 0xC2 && b < 0xE0 {
        return 2
    }
    if b >= 0xE0 && b < 0xF0 {
        return 3
    }
    return if b >= 0xF0 && b < 0xF5 { 4 } else { 0 }
}

// ContOk reports whether c may follow lead at position k (1-based) of its
// sequence (the ranges of the Unicode table 3-7).
ContOk(lead: u32, k: int, c: u32): bool {
    if k == 1 {
        if lead == 0xE0 {
            return c >= 0xA0 && c < 0xC0
        }
        if lead == 0xED {
            return c >= 0x80 && c < 0xA0
        }
        if lead == 0xF0 {
            return c >= 0x90 && c < 0xC0
        }
        if lead == 0xF4 {
            return c >= 0x80 && c < 0x90
        }
    }
    return c >= 0x80 && c < 0xC0
}

// Decode8 decodes the scalar at b[i..n] with maximal-subpart repair. w is
// the bytes consumed; w 0 means b[i..n] is a valid but incomplete sequence.
Decode8(b: *u8, n: int, i: int): (c: u32, w: int) {
    unsafe "b holds n bytes" {
        lead := u32(raw.Read(raw.Offset(b, i)))
        need := SeqLen(lead)
        if need == 1 {
            return (c: lead, w: 1)
        }
        if need == 0 {
            return (c: 0xFFFD, w: 1)
        }
        var c := lead & (0x7F >> Trunc[u32](need))
        for k in 1..need {
            if i + k >= n {
                return (c: 0, w: 0)
            }
            x := u32(raw.Read(raw.Offset(b, i + k)))
            if !ContOk(lead, k, x) {
                return (c: 0xFFFD, w: k)
            }
            c = (c << 6) | (x & 0x3F)
        }
        return (c: c, w: need)
    }
}

// Units appends c as UTF-16 to buf at n and returns the new count.
Units(buf: mut []u16, n: int, c: u32): int {
    if c >= 0x10000 {
        d := c - 0x10000
        buf[n] = Trunc[u16](0xD800 + (d >> 10))
        buf[n + 1] = Trunc[u16](0xDC00 + (d & 0x3FF))
        return n + 2
    }
    buf[n] = Trunc[u16](c)
    return n + 1
}

// FlushUnits writes buf[0..n] to the console; false on failure.
FlushUnits(h: uint, buf: []u16, n: int): bool {
    var off := 0
    for off < n {
        var done: u32 = 0
        unsafe "kernel32; buf holds n units" {
            if WriteConsoleW(h, raw.Offset(raw.Data(buf), off), Trunc[u32](n - off), raw.OfMut(mut done),
                raw.Null[u8]()) == 0 {
                return false
            }
        }
        if done == 0 {
            return false
        }
        off += int(done)
    }
    return true
}

/// WriteConsole converts data, after the bytes carried from the last write,
/// and writes it with WriteConsoleW. value is data.Len() on success.
/// O(len(data)); allocates nothing (the conversion goes through a stack
/// buffer).
export WriteConsole(h: uint, data: []u8, carry: mut ConsoleCarry): OsResult {
    var units: [1024]u16 = [0; 1024]
    var n := 0
    var i := 0
    // Complete the carried sequence with the first bytes of data.
    if carry.n > 0 {
        var seq: [4]u8 = [0; 4]
        var k := int(carry.n)
        for j in 0..k {
            seq[j] = carry.bytes[j]
        }
        for k < 4 && i < data.Len() {
            seq[k] = data[i]
            k += 1
            i += 1
            (c, w) := Decode8(raw.Data(seq[..]), k, 0)
            if w != 0 {
                n = Units(mut units[..], n, c)
                i -= k - w
                k = 0
                break
            }
        }
        carry.n = 0
        if k > 0 && i >= data.Len() {
            // Still incomplete: keep it for the next write.
            for j in 0..k {
                carry.bytes[j] = seq[j]
            }
            carry.n = Trunc[u32](k)
            return Ok(i64(data.Len()))
        }
    }
    for i < data.Len() {
        if n >= 1020 {
            if !FlushUnits(h, units[..], n) {
                unsafe "a kernel32 call that passes no memory, only values and handles" {
                    return LastError()
                }
            }
            n = 0
        }
        (c, w) := Decode8(raw.Data(data), data.Len(), i)
        if w == 0 {
            k := data.Len() - i
            for j in 0..k {
                carry.bytes[j] = data[i + j]
            }
            carry.n = Trunc[u32](k)
            break
        }
        n = Units(mut units[..], n, c)
        i += w
    }
    if n > 0 && !FlushUnits(h, units[..], n) {
        unsafe "a kernel32 call that passes no memory, only values and handles" {
            return LastError()
        }
    }
    return Ok(i64(data.Len()))
}

/// ConsoleTestType types units into this process's console input buffer as
/// key presses and returns how many were written, -1 when refused (tests
/// only: ConsoleRead's test, std/rt-io_waitedges_windows_test.go). A unit
/// 13 is the Enter key; any other unit is a key press of that character, so
/// a lone surrogate can be typed too. It refuses unless the console is this
/// process's alone (GetConsoleProcessList answers 1, as for a process
/// started with CREATE_NO_WINDOW), so it never types into a console a
/// person uses. Both entries are found at run time, so no executable
/// imports them; CONIN$ is opened read-write for the call and closed.
/// O(n) in units; allocates nothing.
export ConsoleTestType(units: []u16): int {
    if units.Len() == 0 || units.Len() > 64 {
        return -1
    }
    var name: [16]u16 = [0; 16]
    _ = Wide("kernel32.dll", mut name[..])
    var conin: [8]u16 = [0; 8]
    _ = Wide("CONIN$", mut conin[..])
    unsafe "kernel32; the entries' types are their documented signatures" {
        m := GetModuleHandleW(raw.Data(name[..]))
        if m == 0 {
            return -1
        }
        lp := GetProcAddress(m, raw.StrData("GetConsoleProcessList\u{0}"))
        wp := GetProcAddress(m, raw.StrData("WriteConsoleInputW\u{0}"))
        if raw.IsNull(lp) || raw.IsNull(wp) {
            return -1
        }
        var lw := u64(raw.Addr(lp))
        list := raw.Read(raw.PtrCast[u64, fn(*u32, u32): u32](raw.OfMut(mut lw)))
        var pids: [4]u32 = [0; 4]
        if list(raw.DataMut(mut pids[..]), 4) != 1 {
            return -1
        }
        h := CreateFileW(raw.Data(conin[..]), GenericRead | GenericWrite, ShareAll, raw.Null[u8](), OpenExisting, 0, 0)
        if h == InvalidHandle {
            return -1
        }
        // INPUT_RECORD: EventType 1 (KEY_EVENT) at 0, then KEY_EVENT_RECORD:
        // bKeyDown at 4, wRepeatCount at 8, wVirtualKeyCode at 10,
        // wVirtualScanCode at 12, UnicodeChar at 14, dwControlKeyState at
        // 16; 20 bytes.
        var recs: [320]u32 = [0; 320]
        r8 := raw.PtrCast[u32, u8](raw.DataMut(mut recs[..]))
        for i in 0..units.Len() {
            e := raw.Offset(r8, i * 20)
            Put16(e, 0, 1)
            Put32(e, 4, 1)
            Put16(e, 8, 1)
            if units[i] == 13 {
                Put16(e, 10, 0x0D)
            }
            Put16(e, 14, units[i])
        }
        var ww := u64(raw.Addr(wp))
        write := raw.Read(raw.PtrCast[u64, fn(uint, *u8, u32, *u32): i32](raw.OfMut(mut ww)))
        var done: u32 = 0
        ok := write(h, r8, Trunc[u32](units.Len()), raw.OfMut(mut done))
        _ = CloseHandle(h)
        return if ok == 0 { -1 } else { int(done) }
    }
}
