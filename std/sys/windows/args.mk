// Why: Windows hands a program one UTF-16 command line and one UTF-16
// environment block; std needs them as UTF-8 arguments and variables, and
// needs the conversions both ways for every path it passes to the OS.
//
// What: ReadArgs, ReadEnv, GetEnvRaw and ExePath, and the conversions Narrow
// (UTF-16 to UTF-8) and Wide (WTF-8 to UTF-16) that the rest of std.sys uses.
//
// How: the command line is split by CommandLineToArgvW's rules without
// importing shell32. Every unpaired surrogate becomes U+FFFD on the way to
// UTF-8; Wide accepts WTF-8, so a name Windows lists with an unpaired
// surrogate opens again.

import std.raw

// PutByte stores b at out[n] when it fits; callers count on regardless, so
// a short buffer reports the size it needed (ArgsInfo).
PutByte(out: mut []u8, n: int, b: u8) {
    if n < out.Len() {
        out[n] = b
    }
}

// PutScalar writes c as UTF-8 at out[n..] and returns the new count.
PutScalar(out: mut []u8, n: int, c: u32): int {
    if c < 0x80 {
        PutByte(out, n, Trunc[u8](c))
        return n + 1
    }
    if c < 0x800 {
        PutByte(out, n, Trunc[u8](0xC0 | (c >> 6)))
        PutByte(out, n + 1, Trunc[u8](0x80 | (c & 0x3F)))
        return n + 2
    }
    if c < 0x10000 {
        PutByte(out, n, Trunc[u8](0xE0 | (c >> 12)))
        PutByte(out, n + 1, Trunc[u8](0x80 | ((c >> 6) & 0x3F)))
        PutByte(out, n + 2, Trunc[u8](0x80 | (c & 0x3F)))
        return n + 3
    }
    PutByte(out, n, Trunc[u8](0xF0 | (c >> 18)))
    PutByte(out, n + 1, Trunc[u8](0x80 | ((c >> 12) & 0x3F)))
    PutByte(out, n + 2, Trunc[u8](0x80 | ((c >> 6) & 0x3F)))
    PutByte(out, n + 3, Trunc[u8](0x80 | (c & 0x3F)))
    return n + 4
}

// Unit reads the UTF-16 unit at p[i].
Unit(p: *u16, i: int): u32 {
    unsafe "p points at a NUL-terminated UTF-16 string that i does not pass" {
        return u32(raw.Read(raw.Offset(p, i)))
    }
}

// Decode16 decodes the scalar at p[i]: a surrogate pair, or one unit; an
// unpaired surrogate is U+FFFD. w is the units consumed.
Decode16(p: *u16, i: int): (c: u32, w: int) {
    u := Unit(p, i)
    if u >= 0xD800 && u < 0xDC00 {
        v := Unit(p, i + 1)
        if v >= 0xDC00 && v < 0xE000 {
            return (c: 0x10000 + ((u - 0xD800) << 10) + (v - 0xDC00), w: 2)
        }
        return (c: 0xFFFD, w: 1)
    }
    return if u >= 0xDC00 && u < 0xE000 { (c: 0xFFFD, w: 1) } else { (c: u, w: 1) }
}

// Narrow converts the NUL-terminated UTF-16 string p (at most limit units)
// to UTF-8 at out[n..]; it returns the new count.
Narrow(p: *u16, limit: int, out: mut []u8, var n: int): int {
    var i := 0
    for i < limit && Unit(p, i) != 0 {
        (c, w) := Decode16(p, i)
        n = PutScalar(out, n, c)
        i += w
    }
    return n
}

// WideBad is Wide's answer for bytes that are not WTF-8.
const WideBad: int = -2

// Cont: b[i] exists and is a continuation byte in lo..hi (Unicode's table
// 3-7 ranges).
Cont(b: []u8, i: int, lo: u8, hi: u8): bool {
    return i < b.Len() && b[i] >= lo && b[i] <= hi
}

// Wide converts s to NUL-terminated UTF-16 in buf. It returns the units
// written with the NUL; -1 when s holds a NUL or does not fit; WideBad
// when the bytes are not WTF-8. WTF-8 is UTF-8 by Unicode's table
// 3-7 (no overlong form, nothing past U+10FFFF, no truncated sequence) plus
// ED A0..BF xx, a surrogate's three bytes, written as that one unit, so a
// name Windows lists with an unpaired surrogate (dir.mk) reopens. A pair
// spelled as two such sequences becomes the pair, as Go's syscall does. A
// str is always UTF-8, so it never answers WideBad; a Path's bytes may
// (call.mk PathOf). O(len); allocates nothing.
Wide(s: str, buf: mut []u16): int {
    b := s.AsBytes()
    var i := 0
    var n := 0
    for i < b.Len() {
        x := u32(b[i])
        var c: u32 = 0
        var w := 1
        if x < 0x80 {
            c = x
        } else if x >= 0xC2 && x <= 0xDF {
            if !Cont(b, i + 1, 0x80, 0xBF) {
                return WideBad
            }
            c = ((x & 0x1F) << 6) | (u32(b[i + 1]) & 0x3F)
            w = 2
        } else if x >= 0xE0 && x <= 0xEF {
            lo: u8 = if x == 0xE0 { 0xA0 } else { 0x80 }
            if !Cont(b, i + 1, lo, 0xBF) || !Cont(b, i + 2, 0x80, 0xBF) {
                return WideBad
            }
            c = ((x & 0x0F) << 12) | ((u32(b[i + 1]) & 0x3F) << 6) | (u32(b[i + 2]) & 0x3F)
            w = 3
        } else if x >= 0xF0 && x <= 0xF4 {
            lo: u8 = if x == 0xF0 { 0x90 } else { 0x80 }
            hi: u8 = if x == 0xF4 { 0x8F } else { 0xBF }
            if !Cont(b, i + 1, lo, hi) || !Cont(b, i + 2, 0x80, 0xBF) || !Cont(b, i + 3, 0x80, 0xBF) {
                return WideBad
            }
            c = ((x & 0x07) << 18) | ((u32(b[i + 1]) & 0x3F) << 12) | ((u32(b[i + 2]) & 0x3F) << 6) | (u32(b[i + 3]) & 0x3F)
            w = 4
        } else {
            return WideBad
        }
        if c == 0 {
            return -1
        }
        if c >= 0x10000 {
            if n + 2 >= buf.Len() {
                return -1
            }
            d := c - 0x10000
            buf[n] = Trunc[u16](0xD800 + (d >> 10))
            buf[n + 1] = Trunc[u16](0xDC00 + (d & 0x3FF))
            n += 2
        } else {
            if n + 1 >= buf.Len() {
                return -1
            }
            buf[n] = Trunc[u16](c)
            n += 1
        }
        i += w
    }
    if n >= buf.Len() {
        return -1
    }
    buf[n] = 0
    return n + 1
}

// EndArg records the end of argument count in ends when it fits.
EndArg(ends: mut []int, count: int, n: int) {
    if count < ends.Len() {
        ends[count] = n
    }
}

// Backslashes counts the backslashes at p[i..].
Backslashes(p: *u16, i: int): int {
    var k := 0
    for Unit(p, i + k) == 0x5C {
        k += 1
    }
    return k
}

/// ReadArgs splits GetCommandLineW: the program name runs to the first
/// space or tab, or between quotes with no escapes; then arguments separated
/// by spaces and tabs, where 2n backslashes before a quote give n and toggle
/// quoting, 2n+1 give n and a literal quote, "" inside quotes is a quote, and
/// other backslashes are literal. The arguments go into bytes, each one's
/// end into ends, as far as they fit (ArgsInfo says what was needed). sp is
/// unused on Windows.
/// O(n) in the bytes of the command line; allocates nothing (bytes and ends are
/// the caller's).
export ReadArgs(sp: *u8, bytes: mut []u8, ends: mut []int): ArgsInfo {
    unsafe "kernel32: the command line lives for the whole process" {
        p := GetCommandLineW()
        var i := 0
        var n := 0
        var count := 0
        if Unit(p, 0) == 0x22 {
            i = 1
            for Unit(p, i) != 0 && Unit(p, i) != 0x22 {
                (c, w) := Decode16(p, i)
                n = PutScalar(bytes, n, c)
                i += w
            }
            if Unit(p, i) == 0x22 {
                i += 1
            }
        } else {
            for Unit(p, i) != 0 && Unit(p, i) != 0x20 && Unit(p, i) != 0x09 {
                (c, w) := Decode16(p, i)
                n = PutScalar(bytes, n, c)
                i += w
            }
        }
        EndArg(ends, count, n)
        count += 1
        for {
            for Unit(p, i) == 0x20 || Unit(p, i) == 0x09 {
                i += 1
            }
            if Unit(p, i) == 0 {
                break
            }
            var quoted := false
            for {
                u := Unit(p, i)
                if u == 0 || (!quoted && (u == 0x20 || u == 0x09)) {
                    break
                }
                if u == 0x5C {
                    k := Backslashes(p, i)
                    if Unit(p, i + k) == 0x22 {
                        for _ in 0..k / 2 {
                            n = PutScalar(bytes, n, 0x5C)
                        }
                        if k % 2 == 1 {
                            n = PutScalar(bytes, n, 0x22)
                            i += k + 1
                        } else {
                            i += k
                        }
                    } else {
                        for _ in 0..k {
                            n = PutScalar(bytes, n, 0x5C)
                        }
                        i += k
                    }
                } else if u == 0x22 {
                    if quoted && Unit(p, i + 1) == 0x22 {
                        n = PutScalar(bytes, n, 0x22)
                        i += 2
                    } else {
                        quoted = !quoted
                        i += 1
                    }
                } else {
                    (c, w) := Decode16(p, i)
                    n = PutScalar(bytes, n, c)
                    i += w
                }
            }
            EndArg(ends, count, n)
            count += 1
        }
        return ArgsInfo(count: count, bytes: n)
    }
}

/// ReadEnv splits GetEnvironmentStringsW into NAME, VALUE entries (count is
/// twice the variables), into bytes and ends as ReadArgs does; a leading =
/// belongs to the name, as in =C:.
/// O(n) in the bytes of the environment; allocates nothing (bytes and ends are
/// the caller's).
export ReadEnv(sp: *u8, bytes: mut []u8, ends: mut []int): ArgsInfo {
    unsafe "kernel32: the block is freed after the copy" {
        block := GetEnvironmentStringsW()
        if raw.IsNull(block) {
            return ArgsInfo(count: 0, bytes: 0)
        }
        var i := 0
        var n := 0
        var count := 0
        for Unit(block, i) != 0 {
            var j := i + 1
            for Unit(block, j) != 0 && Unit(block, j) != 0x3D {
                j += 1
            }
            for i < j {
                (c, w) := Decode16(block, i)
                n = PutScalar(bytes, n, c)
                i += w
            }
            EndArg(ends, count, n)
            count += 1
            if Unit(block, i) == 0x3D {
                i += 1
            }
            for Unit(block, i) != 0 {
                (c, w) := Decode16(block, i)
                n = PutScalar(bytes, n, c)
                i += w
            }
            EndArg(ends, count, n)
            count += 1
            i += 1
        }
        _ = FreeEnvironmentStringsW(block)
        return ArgsInfo(count: count, bytes: n)
    }
}

/// GetEnvRaw reads one variable before rt's decoding exists: the
/// UTF-8 length of its value, written to out as far as it fits, or -1 when
/// unset. A name that is empty or holds a NUL or an = is never set (-1;
/// Linux gives the same answer). A value of 1,024 units or more is read
/// again into pages mapped for the call (EnvLong), never answered -1 as if
/// unset. sp is unused on Windows.
/// O(n) in the value's length: one GetEnvironmentVariableW, two for a long
/// value; allocates nothing but the pages a long value is read into, given
/// back before it returns.
export GetEnvRaw(sp: *u8, name: str, out: mut []u8): int {
    if !EnvName(name) {
        return -1
    }
    var wname: [256]u16 = [0; 256]
    if Wide(name, mut wname[..]) < 0 {
        return -1
    }
    var value: [1024]u16 = [0; 1024]
    unsafe "kernel32; wname is NUL-terminated and value holds 1,024 units, both in this frame" {
        got := GetEnvironmentVariableW(raw.Data(wname[..]), raw.DataMut(mut value[..]), 1024)
        if got == 0 {
            return if GetLastError() == 203 { -1 } else { 0 }
        }
        if got < 1024 {
            return Narrow(raw.Data(value[..]), int(got), out, 0)
        }
        return EnvLong(raw.Data(wname[..]), got, out)
    }
}

// EnvName says whether name can name a variable: not empty, no NUL, no =.
EnvName(name: str): bool {
    b := name.AsBytes()
    if b.Len() == 0 {
        return false
    }
    for i in 0..b.Len() {
        if b[i] == 0 || b[i] == 0x3D {
            return false
        }
    }
    return true
}

// EnvLong reads the variable wname whose value needs size units with its
// NUL (GetEnvironmentVariableW's answer to a short buffer) into pages mapped
// for the call, and gives them back on every path. Another thread may set
// the variable between two calls; a value that grew again is read again,
// four tries in all, then -1. Windows bounds a variable at 32,767 units, so
// size is at most 32,768 here and the mapping at most 64 KiB; a larger
// size is refused (-1) before anything is mapped.
EnvLong(wname: *u16, var size: u32, out: mut []u8): int {
    for _ in 0..4 {
        if size > 32768 {
            return -1
        }
        bytes := PageRound(int(size) * 2)
        p := Reserve(bytes, 4096)
        if raw.IsNull(p) || !Commit(p, bytes) {
            FreeMap(p, bytes)
            return -1
        }
        var n := -2
        unsafe "kernel32; p holds size units, mapped above" {
            w := raw.PtrCast[u8, u16](p)
            got := GetEnvironmentVariableW(wname, w, size)
            if got == 0 {
                // ERROR_ENVVAR_NOT_FOUND: unset (-1), else empty (0).
                n = if GetLastError() == 203 { -1 } else { 0 }
            } else if got < size {
                n = Narrow(w, int(got), out, 0)
            } else {
                size = got
            }
        }
        Release(p, bytes)
        if n != -2 {
            return n
        }
    }
    return -1
}

/// ExePath writes the executable's path in UTF-8; 0 on failure or when out
/// is too short. A path of 1,024 units or more (a long path) is read again
/// into 64 KiB mapped for the call, the 32,768 units of the longest path
/// Windows has, never 0.
/// O(1) system calls; allocates nothing from the Heap (out is the caller's;
/// a long path's 64 KiB are given back before it returns).
export ExePath(out: mut []u8): int {
    var path: [1024]u16 = [0; 1024]
    unsafe "kernel32; path holds 1,024 units in this frame, and p 32,768 units it committed" {
        got := GetModuleFileNameW(0, raw.DataMut(mut path[..]), 1024)
        if got == 0 {
            return 0
        }
        if got < 1024 {
            return ExeNarrow(raw.Data(path[..]), got, out)
        }
        p := Reserve(65536, 4096)
        if raw.IsNull(p) || !Commit(p, 65536) {
            FreeMap(p, 65536)
            return 0
        }
        w := raw.PtrCast[u8, u16](p)
        long := GetModuleFileNameW(0, w, 32768)
        n := if long != 0 && long < 32768 { ExeNarrow(w, long, out) } else { 0 }
        Release(p, 65536)
        return n
    }
}

// ExeNarrow converts the got units of path to UTF-8 in out: the length, or
// 0 when out is too short.
ExeNarrow(path: *u16, got: u32, out: mut []u8): int {
    n := Narrow(path, int(got), out, 0)
    return if n > out.Len() { 0 } else { n }
}
