// Why: the arguments and the environment arrive in the platform's encoding;
// Mk code wants them as UTF-8 str values that live for the whole process.
//
// What: DecodeArgs and DecodeEnv, filling RtState.args and Process.env, with
// the helpers ReadAll, FreeLists and Entry over the Strings list.
//
// How: std.sys decodes once (UTF-8 with U+FFFD repair) into two heap blocks,
// the bytes and the end offset of each entry, sized by a first read and
// filled by a second, retried if the source grew in between.

import std.core
import std.raw
import std.sys

// Strings is a decoded list: its bytes, and the end offset of each entry.
struct Strings {
    bytes: *u8
    ends: *int
    count: int
}

// ReadAll calls read twice: once to size the buffers, once to fill them
// (up to three retries if the source grew between the calls; each retry
// frees the blocks that were too small). env picks ReadEnv. O(bytes +
// count) per attempt; the two blocks live for the whole process.
ReadAll(sp: *u8, env: bool): ?Strings {
    var b0: [1]u8 = [0; 1]
    var e0: [1]int = [0; 1]
    var info := sys.ArgsInfo(count: 0, bytes: 0)
    if env {
        info = sys.ReadEnv(sp, mut b0[..0], mut e0[..0])
    } else {
        info = sys.ReadArgs(sp, mut b0[..0], mut e0[..0])
    }
    for _ in 0..4 {
        nb := info.bytes + 16
        nc := info.count + 4
        bytes := HeapAlloc(nb, 1)
        ends := HeapAlloc(nc * SizeOf[int](), 8)
        if raw.IsNull(bytes) || raw.IsNull(ends) {
            FreeLists(bytes, nb, ends, nc)
            return none
        }
        unsafe "fresh heap blocks of nb bytes and nc ints" {
            b := raw.SliceMut(bytes, nb)
            e := raw.SliceMut(raw.PtrCast[u8, int](ends), nc)
            got := if env { sys.ReadEnv(sp, b, e) } else { sys.ReadArgs(sp, b, e) }
            if got.bytes <= nb && got.count <= nc {
                return some(Strings(bytes: bytes, ends: raw.PtrCast[u8, int](ends), count: got.count))
            }
            info = got
        }
        // The source grew between the calls: these blocks are too small.
        FreeLists(bytes, nb, ends, nc)
    }
    return none
}

// FreeLists frees ReadAll's two blocks (either may be null).
FreeLists(bytes: *u8, nb: int, ends: *u8, nc: int) {
    if !raw.IsNull(bytes) {
        HeapFree(bytes, nb, 1)
    }
    if !raw.IsNull(ends) {
        HeapFree(ends, nc * SizeOf[int](), 8)
    }
}

// Entry is string i of a decoded list.
Entry(s: &Strings, i: int): str {
    unsafe "s's buffers hold count entries of valid UTF-8" {
        start := if i > 0 { raw.Read(raw.Offset(s.ends, i - 1)) } else { 0 }
        end := raw.Read(raw.Offset(s.ends, i))
        return raw.Str(raw.Offset(s.bytes, start), end - start)
    }
}

/// DecodeArgs fills RtState.args. False when the heap is exhausted, or when
/// the source changed under four reads in a row (ReadAll).
/// O(n) in the bytes of the arguments; allocates from the Heap the decoded
/// bytes, their end offsets and one array of n strs, kept until exit.
export DecodeArgs(sp: *u8): bool {
    s := ReadAll(sp, false) else {
        return false
    }
    p := HeapAlloc(s.count * SizeOf[str]() + 1, 8)
    if raw.IsNull(p) {
        return false
    }
    unsafe "p is a fresh heap block of count strs; Init ran, so R() is the page it mapped, which lives until exit" {
        a := raw.PtrCast[u8, str](p)
        for i in 0..s.count {
            raw.Write(raw.Offset(a, i), Entry(&s, i))
        }
        (*R()).args = raw.Slice(a, s.count)
    }
    return true
}

/// DecodeEnv fills Process.env with NAME, VALUE pairs. False when the heap
/// is exhausted, or when the environment changed under four reads in a row.
/// O(n) in the bytes of the environment; allocates from the Heap the decoded
/// bytes, their end offsets and one array of EnvVars, kept until exit.
export DecodeEnv(sp: *u8): bool {
    s := ReadAll(sp, true) else {
        return false
    }
    n := s.count / 2
    p := HeapAlloc(n * SizeOf[core.EnvVar]() + 1, 8)
    if raw.IsNull(p) {
        return false
    }
    unsafe "p is a fresh heap block of n EnvVars; Init ran, so Proc() is the page it mapped, which lives until exit" {
        a := raw.PtrCast[u8, core.EnvVar](p)
        for i in 0..n {
            raw.Write(raw.Offset(a, i), core.EnvVar(name: Entry(&s, 2 * i), value: Entry(&s, 2 * i + 1)))
        }
        (*Proc()).env = raw.Slice(a, n)
    }
    return true
}
