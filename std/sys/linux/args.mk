// Why: a static Linux executable has no libc to hand it argv and envp; the
// kernel leaves them on the initial stack, and Mk strings must be UTF-8.
//
// What: ReadArgs, ReadEnv and GetEnvRaw over argc, argv and envp at the
// initial stack pointer, ExePath (/proc/self/exe) and ImageBounds (the
// executable's address range from the auxiliary vector).
//
// How: the stack is read as 8-byte words (Word); Repair copies each string
// and replaces every maximal invalid UTF-8 subpart with U+FFFD, following
// Unicode table 3-7. Nothing allocates: callers pass the buffers.

import std.raw

// Word reads the i-th 8-byte word above sp.
Word(sp: *u8, i: int): uint {
    unsafe "sp is the initial stack: argc, argv, NULL, envp, NULL" {
        return raw.Read(raw.PtrCast[u8, uint](raw.Offset(sp, i * 8)))
    }
}

// CLen is the length of the NUL-terminated string at p.
CLen(p: *u8): int {
    var n := 0
    unsafe "p is a NUL-terminated string from the kernel" {
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
    argc := Trunc[int](Word(sp, 0))
    var n := 0
    for i in 0..argc {
        unsafe "argv[i] is a string from the kernel" {
            s := raw.FromAddr[u8](Word(sp, 1 + i))
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
    argc := Trunc[int](Word(sp, 0))
    var n := 0
    var count := 0
    var i := argc + 2
    for Word(sp, i) != 0 {
        unsafe "envp[i] is a string from the kernel" {
            s := raw.FromAddr[u8](Word(sp, i))
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
/// (-1, as on Windows): a NUL in the name would let the compare run past
/// the entry's NUL into the next string, and "A=B" would match
/// the entry "A=B=C".
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
    argc := Trunc[int](Word(sp, 0))
    var i := argc + 2
    for Word(sp, i) != 0 {
        unsafe "envp[i] is a string from the kernel" {
            s := raw.FromAddr[u8](Word(sp, i))
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

/// ExePath reads /proc/self/exe; 0 on failure or when out is too short.
/// O(1) system calls; allocates nothing (out is the caller's).
export ExePath(out: mut []u8): int {
    unsafe "system call; the path is a NUL-terminated constant" {
        r := Syscall(Nrs().readlinkat, AtFdcwd, raw.Addr(raw.StrData("/proc/self/exe\u{0}")), raw.Addr(raw.DataMut(out)),
            Trunc[uint](out.Len()), 0, 0)
        return if IsErr(r) || r >= out.Len() { 0 } else { r }
    }
}

/// ImageBounds is the executable image's address range [lo, hi), from the
/// auxiliary vector after envp on the initial stack at sp: the PT_LOAD
/// segments of the program headers at AT_PHDR (AT_PHNUM of them), moved by
/// the load bias PT_PHDR gives when the image is position-independent (the
/// static ET_EXEC images mk links have none, so the bias is 0). It is
/// __ehdr_start to _end for those images. (0, 0) when sp
/// is null or the vector has no program headers.
/// O(n): one walk of the arguments, the environment, the auxiliary vector
/// and the program headers; allocates nothing.
export ImageBounds(sp: *u8): (lo: uint, hi: uint) {
    if raw.IsNull(sp) {
        return (lo: 0, hi: 0)
    }
    argc := Trunc[int](Word(sp, 0))
    var i := argc + 2
    for Word(sp, i) != 0 {
        i += 1
    }
    i += 1
    var phdr: uint = 0
    var phnum: uint = 0
    for Word(sp, i) != 0 {
        if Word(sp, i) == 3 {
            phdr = Word(sp, i + 1)
        } else if Word(sp, i) == 5 {
            phnum = Word(sp, i + 1)
        }
        i += 2
    }
    if phdr == 0 || phnum == 0 {
        return (lo: 0, hi: 0)
    }
    var bias: uint = 0
    var lo: uint = 0
    var hi: uint = 0
    unsafe "AT_PHDR is the image's program header table, 56-byte Elf64_Phdr rows" {
        for j in 0..Trunc[int](phnum) {
            p := raw.FromAddr[u8](phdr + Trunc[uint](j * 56))
            if raw.Read(raw.PtrCast[u8, u32](p)) == 6 {
                bias = phdr - raw.Read(raw.PtrCast[u8, uint](raw.Offset(p, 16)))
            }
        }
        for j in 0..Trunc[int](phnum) {
            p := raw.FromAddr[u8](phdr + Trunc[uint](j * 56))
            if raw.Read(raw.PtrCast[u8, u32](p)) == 1 {
                a := raw.Read(raw.PtrCast[u8, uint](raw.Offset(p, 16))) + bias
                e := a + raw.Read(raw.PtrCast[u8, uint](raw.Offset(p, 40)))
                if lo == 0 || a < lo {
                    lo = a
                }
                if e > hi {
                    hi = e
                }
            }
        }
    }
    // The fault handler's ownership test reads them (fault.mk, FaultOwned).
    // Edge: stored before rt.Init returns, before any task or thread std
    // starts; a fault before that is taken as Mk's (lo 0).
    Faults.img_hi.Store(u64(hi))
    Faults.img_lo.Store(u64(lo))
    // The calling thread is the main thread (rt.Init runs there, at the
    // start): carrier 0's, Mk's as the threads ThreadStart makes are
    // (ThreadMade).
    // Edge: as the bounds above.
    Faults.main_tid.Store(Trunc[u32](ThreadId()))
    return (lo: lo, hi: hi)
}
