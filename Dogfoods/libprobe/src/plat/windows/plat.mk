// Why: the per-target `plat` module for Windows: the system's library
// directory and file versions.
// What: Name, SystemDir and FileVersion, with NotHere and NoVersion.
// How: the version resource is read through version.dll, which is not among
// the ten DLLs mk searches by itself, so pkg.mk names it in `links`.

import std.raw

/// Why the system cannot answer.
export error NotHere

/// The file holds no version resource, or it could not be read.
export error NoVersion

/// The name of this system, for messages.
export const Name: str = "windows"

// Reads how many bytes the version resource of the NUL-terminated UTF-16
// name `name` takes, 0 when it has none; `handle` is set to 0.
extern GetFileVersionInfoSizeW(name: *u16, handle: *u32): u32

// Copies the version resource of `name` into `data`, `len` bytes at most;
// nonzero on success.
extern GetFileVersionInfoW(name: *u16, handle: u32, len: u32, data: *u8): i32

// The signature that opens VS_FIXEDFILEINFO inside a version resource.
const FixedSignature: u32 = 0xFEEF04BD

// The most bytes of a version resource libprobe reads.
const MaxResource: int = 1 << 20

/// The directory that holds the system's own libraries: `%SystemRoot%`'s
/// System32, or none when SystemRoot is not set.
export SystemDir(sys: &System): ?str {
    root := sys.Env("SystemRoot") else { return none }
    if root.Len() == 0 {
        return none
    }
    dir := sys.arena.Fmt("{root}/System32", (root)) else { return none }
    return some(dir)
}

// `s` as NUL-terminated UTF-16, in `a`.
Wide(a: &Arena, s: str): OutOfMemory!mut []u16 {
    var n := 1
    for c in s.Scalars() {
        n += if u32(c) > 0xFFFF { 2 } else { 1 }
    }
    out := try a.Slice[u16](n)
    var i := 0
    for c in s.Scalars() {
        v := u32(c)
        if v > 0xFFFF {
            w := v - 0x10000
            out[i] = Cast[u16](0xD800 + (w >> 10))
            out[i + 1] = Cast[u16](0xDC00 + (w & 0x3FF))
            i += 2
        } else {
            out[i] = Cast[u16](v)
            i += 1
        }
    }
    out[i] = 0
    return out
}

// The little-endian word at `at`.
Word(b: []u8, at: int): u32 {
    return u32(b[at]) | u32(b[at + 1]) << 8 | u32(b[at + 2]) << 16 | u32(b[at + 3]) << 24
}

/// The file version of `path` as four numbers, major first, read from the
/// fixed part of its version resource. Fails with `NoVersion` when the file
/// is missing, unreadable or has no resource.
export FileVersion(a: &Arena, path: str): NotHere|NoVersion|OutOfMemory![4]int {
    name := try Wide(a, path)
    var handle: u32 = 0
    var size: u32 = 0
    unsafe "name is NUL-terminated UTF-16 and handle is one word" {
        size = GetFileVersionInfoSizeW(raw.OfMut(mut name[0]), raw.OfMut(mut handle))
    }
    if size == 0 || int(size) > MaxResource {
        fail NoVersion
    }
    data := try a.Slice[u8](int(size))
    var ok: i32 = 0
    unsafe "data holds size bytes and name is NUL-terminated UTF-16" {
        ok = GetFileVersionInfoW(raw.OfMut(mut name[0]), 0, size, raw.OfMut(mut data[0]))
    }
    if ok == 0 {
        fail NoVersion
    }
    // VS_FIXEDFILEINFO starts on a 4-byte boundary with its signature, then
    // the structure version, then the file version's high and low words.
    var at := 0
    for at + 16 <= data.Len() {
        if Word(data[..], at) == FixedSignature {
            hi := Word(data[..], at + 8)
            lo := Word(data[..], at + 12)
            return [int(hi >> 16), int(hi & 0xFFFF), int(lo >> 16), int(lo & 0xFFFF)]
        }
        at += 4
    }
    fail NoVersion
}
