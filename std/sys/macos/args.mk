// Why: dyld hands a macOS program argc, argv and envp as main's
// arguments, and Mk strings must be UTF-8.
//
// What: ReadArgs, ReadEnv and GetEnvRaw over the entry block, ExePath
// (_NSGetExecutablePath) and ImageBounds (the main image's segments).
//
// How: the support pack's main stores argc, argv, envp and apple in a block
// of four words on the main thread's stack and passes its address as sp;
// argv and envp are NULL-terminated arrays of C strings. Repair copies each
// string and replaces every maximal invalid UTF-8 subpart with U+FFFD,
// following Unicode table 3-7, as on Linux. Nothing allocates: callers pass
// the buffers.

import std.raw

// Word reads the i-th 8-byte word at p: the entry block, or an argv or
// envp array.
Word(p: *u8, i: int): uint {
    unsafe "p is the entry block or a NULL-terminated array of string pointers" {
        return raw.Read(raw.PtrCast[u8, uint](raw.Offset(p, i * 8)))
    }
}

// Argv and Envp are the arrays the entry block at sp points to.
Argv(sp: *u8): *u8 {
    unsafe "word 1 of the entry block is argv" {
        return raw.FromAddr[u8](Word(sp, 1))
    }
}

Envp(sp: *u8): *u8 {
    unsafe "word 2 of the entry block is envp" {
        return raw.FromAddr[u8](Word(sp, 2))
    }
}

// CLen is the length of the NUL-terminated string at p.
CLen(p: *u8): int {
    var n := 0
    unsafe "p is a NUL-terminated string from the kernel or dyld" {
        for raw.Read(raw.Offset(p, n)) != 0 {
            n += 1
        }
    }
    return n
}

PutByte(out: mut []u8, n: int, b: u8) {
    if n < out.Len() {
        out[n] = b
    }
}

// ContOk reports whether c may follow lead at position k of its sequence
// (Unicode table 3-7).
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

// Repair copies p[0..len] to out[n..] as UTF-8, each maximal invalid
// subpart replaced by U+FFFD (EF BF BD); it returns the new count.
Repair(p: *u8, len: int, out: mut []u8, var n: int): int {
    var i := 0
    unsafe "p holds len bytes" {
        for i < len {
            lead := u32(raw.Read(raw.Offset(p, i)))
            need := SeqLen(lead)
            var good := need > 0
            var k := 1
            for good && k < need {
                if i + k >= len || !ContOk(lead, k, u32(raw.Read(raw.Offset(p, i + k)))) {
                    good = false
                } else {
                    k += 1
                }
            }
            if good {
                for j in 0..need {
                    PutByte(out, n + j, raw.Read(raw.Offset(p, i + j)))
                }
                n += need
                i += need
            } else {
                PutByte(out, n, 0xEF)
                PutByte(out, n + 1, 0xBF)
                PutByte(out, n + 2, 0xBD)
                n += 3
                i += k
            }
        }
    }
    return n
}

EndArg(ends: mut []int, count: int, n: int) {
    if count < ends.Len() {
        ends[count] = n
    }
}

/// ReadArgs copies argv in order into bytes, each argument's end into ends,
/// as far as they fit (ArgsInfo says what was needed); sys.args[0] is the
/// name as the OS gave it.
/// O(n) in the bytes of the command line; allocates nothing (bytes and ends are
/// the caller's).
export ReadArgs(sp: *u8, bytes: mut []u8, ends: mut []int): ArgsInfo {
    if raw.IsNull(sp) {
        return ArgsInfo(count: 0, bytes: 0)
    }
    argc := Trunc[int](Word(sp, 0))
    argv := Argv(sp)
    var n := 0
    for i in 0..argc {
        unsafe "argv[i] is a string from dyld" {
            s := raw.FromAddr[u8](Word(argv, i))
            n = Repair(s, CLen(s), bytes, n)
        }
        EndArg(ends, i, n)
    }
    return ArgsInfo(count: argc, bytes: n)
}

/// ReadEnv copies envp as NAME, VALUE entries, split at the first =, into
/// bytes and ends as ReadArgs does.
/// O(n) in the bytes of the environment; allocates nothing (bytes and ends are
/// the caller's).
export ReadEnv(sp: *u8, bytes: mut []u8, ends: mut []int): ArgsInfo {
    if raw.IsNull(sp) {
        return ArgsInfo(count: 0, bytes: 0)
    }
    envp := Envp(sp)
    var n := 0
    var count := 0
    var i := 0
    for Word(envp, i) != 0 {
        unsafe "envp[i] is a string from dyld" {
            s := raw.FromAddr[u8](Word(envp, i))
            len := CLen(s)
            var eq := 0
            for eq < len && raw.Read(raw.Offset(s, eq)) != 0x3D {
                eq += 1
            }
            n = Repair(s, eq, bytes, n)
            EndArg(ends, count, n)
            count += 1
            rest := if eq < len { len - eq - 1 } else { 0 }
            n = Repair(raw.Offset(s, eq + 1), rest, bytes, n)
            EndArg(ends, count, n)
            count += 1
        }
        i += 1
    }
    return ArgsInfo(count: count, bytes: n)
}

/// GetEnvRaw scans envp from sp as ReadEnv does; the value's length,
/// written to out as far as it fits (no repair: MK_* values are ASCII), or
/// -1 when unset. A name that is empty or holds a NUL or an = is never set
/// (-1, as on Linux and Windows).
/// O(n) in the bytes of the environment; allocates nothing.
export GetEnvRaw(sp: *u8, name: str, out: mut []u8): int {
    if raw.IsNull(sp) {
        return -1
    }
    nb := name.AsBytes()
    if nb.Len() == 0 {
        return -1
    }
    for j in 0..nb.Len() {
        if nb[j] == 0 || nb[j] == 0x3D {
            return -1
        }
    }
    envp := Envp(sp)
    var i := 0
    for Word(envp, i) != 0 {
        unsafe "envp[i] is a string from dyld" {
            s := raw.FromAddr[u8](Word(envp, i))
            var k := 0
            for k < nb.Len() && raw.Read(raw.Offset(s, k)) == nb[k] {
                k += 1
            }
            if k == nb.Len() && raw.Read(raw.Offset(s, k)) == 0x3D {
                v := raw.Offset(s, k + 1)
                len := CLen(v)
                for j in 0..len {
                    PutByte(out, j, raw.Read(raw.Offset(v, j)))
                }
                return len
            }
        }
        i += 1
    }
    return -1
}

/// ExePath is the executable's path from _NSGetExecutablePath, as dyld
/// recorded it (it may hold a symbolic link or a relative part); 0 on
/// failure or when out is too short.
/// O(1); allocates nothing (out is the caller's).
export ExePath(out: mut []u8): int {
    if out.Len() < 2 {
        return 0
    }
    var size: u32 = Trunc[u32](out.Len())
    unsafe "out holds size bytes, which _NSGetExecutablePath fills with a NUL-terminated path" {
        if _NSGetExecutablePath(raw.DataMut(out), raw.OfMut(mut size)) != 0 {
            return 0
        }
        n := CLen(raw.DataMut(out))
        return if n >= out.Len() { 0 } else { n }
    }
}

// LcSegment64 is LC_SEGMENT_64 of <mach-o/loader.h>.
const LcSegment64: u32 = 0x19

/// ImageBounds is the main executable's address range [lo, hi): the
/// segments of its Mach-O header (dyld's image 0) that are mapped (maxprot
/// not 0, so __PAGEZERO is left out), moved by the image's slide (the
/// header's address less __TEXT's vmaddr). (0, 0) when dyld has no image.
/// O(n) in the header's load commands; allocates nothing.
export ImageBounds(sp: *u8): (lo: uint, hi: uint) {
    var lo: uint = 0
    var hi: uint = 0
    unsafe "dyld's image 0 is the executable; its mach_header_64 and load commands are mapped" {
        h := _dyld_get_image_header(0)
        if raw.IsNull(h) {
            return (lo: 0, hi: 0)
        }
        ncmds := Trunc[int](raw.Read(raw.PtrCast[u8, u32](raw.Offset(h, 16))))
        // The slide: the header is the start of __TEXT, whose file offset
        // is 0.
        var text_addr: uint = 0
        var c := raw.Offset(h, 32)
        for _ in 0..ncmds {
            cmd := raw.Read(raw.PtrCast[u8, u32](c))
            size := Trunc[int](raw.Read(raw.PtrCast[u8, u32](raw.Offset(c, 4))))
            if cmd == LcSegment64 && raw.Read(raw.PtrCast[u8, u64](raw.Offset(c, 40))) == 0 &&
                raw.Read(raw.PtrCast[u8, u64](raw.Offset(c, 48))) > 0 {
                text_addr = Trunc[uint](raw.Read(raw.PtrCast[u8, u64](raw.Offset(c, 24))))
            }
            c = raw.Offset(c, size)
        }
        slide := raw.Addr(h) - text_addr
        c = raw.Offset(h, 32)
        for _ in 0..ncmds {
            cmd := raw.Read(raw.PtrCast[u8, u32](c))
            size := Trunc[int](raw.Read(raw.PtrCast[u8, u32](raw.Offset(c, 4))))
            if cmd == LcSegment64 && raw.Read(raw.PtrCast[u8, i32](raw.Offset(c, 56))) != 0 {
                a := Trunc[uint](raw.Read(raw.PtrCast[u8, u64](raw.Offset(c, 24)))) + slide
                e := a + Trunc[uint](raw.Read(raw.PtrCast[u8, u64](raw.Offset(c, 32))))
                if lo == 0 || a < lo {
                    lo = a
                }
                if e > hi {
                    hi = e
                }
            }
            c = raw.Offset(c, size)
        }
    }
    // The fault handler's ownership test reads them (fault.mk).
    // Edge: stored before rt.Init returns, before any task or thread std
    // starts.
    Faults.img_hi.Store(u64(hi))
    Faults.img_lo.Store(u64(lo))
    return (lo: lo, hi: hi)
}
