// Why: a str must hold well-formed UTF-8, so every conversion from bytes is
// checked once, here, and fast enough to use on every input.
//
// What: str.From, str.FromC and ValidUtf8.
//
// How: validation follows the table of Unicode §3.9: no overlong forms, no
// surrogates D800..DFFF, nothing above 10FFFF, no truncated sequence. From
// 256 bytes it is std.simd's vector validator; below that, and at every
// length on x86_64-freestanding, a SWAR form that reads ASCII 8 bytes at a
// time.

import std.raw
import std.simd

/// The bytes as a str that shares their memory. Fails with Utf8Error unless
/// they are well-formed UTF-8: no overlong form, no surrogate, no value
/// above 10FFFF and no truncated sequence (Unicode section 3.9). O(n), 8
/// bytes or more per step on ASCII; allocates nothing. From 256 bytes it
/// validates with std.simd's vector validator, except on
/// x86_64-freestanding.
export str.From(bytes: []u8): Utf8Error!str {
    if !ValidUtf8(bytes) {
        fail Utf8Error
    }
    unsafe "bytes is valid UTF-8, checked above; the view shares its memory" {
        return raw.Str(raw.Data(bytes), bytes.Len())
    }
}

/// The NUL-terminated string at p, as a str sharing its memory. O(n); allocates
/// nothing.
///
/// A call needs an unsafe context: p points at a NUL-terminated string the
/// caller vouches for. Bytes before the NUL that are not UTF-8 panic (code
/// 8). The check is str.From's, vectorized from 256 bytes.
export str.FromC(p: *u8): str {
    var n := 0
    unsafe "the caller vouches p is NUL-terminated and readable up to the NUL" {
        for raw.Read(raw.Offset(p, n)) != 0 {
            n += 1
        }
        b := raw.Slice(p, n)
        if !ValidUtf8(b) {
            raw.Fail(8, "str.FromC: invalid UTF-8")
        }
        return raw.Str(p, n)
    }
}

// ValidUtf8 reports whether b is well-formed UTF-8 (Unicode §3.9, table
// 3-7), with Go's utf8.Valid's per-lead ranges. O(n), no allocation.
//
// From 256 bytes it is std.simd's ValidUtf8, the lookup algorithm
// of Keiser and Lemire, 16 bytes per vector at baseline and wider where
// Cpu() reports avx2 or avx512bw. Below 256 bytes, and at every length on
// x86_64-freestanding, it is std.core's own SWAR form: ASCII 8 bytes per word (LoadWord, one
// align 1 load), a word holding a non-ASCII byte jumping straight to it,
// then one scalar per step. Below 256 bytes std.simd's form costs more
// steps than this one on mostly-ASCII text, since the evaluator runs its
// lanes one by one; on x86_64-freestanding std.simd would run its own
// SWAR word form, which costs more steps than this one at every
// length. Both accept exactly the same
// inputs (TestCore_BytePathsMatchScalar and tests_bytes.mk's blocks).
ValidUtf8(b: []u8): bool {
    n := b.Len()
    if Target.os != Os.Freestanding {
        if n >= 256 {
            return simd.ValidUtf8(b)
        }
    }
    var i := 0
    for i < n {
        if i + 8 <= n {
            w := LoadWord(b, i) & 0x8080808080808080
            if w == 0 {
                i += 8
                continue
            }
            i += raw.Ctz64(w) / 8
        }
        c := b[i]
        if c < 0x80 {
            i += 1
            continue
        }
        var need := 0
        var lo: u8 = 0x80
        var hi: u8 = 0xBF
        if c >= 0xC2 && c <= 0xDF {
            need = 1
        } else if c == 0xE0 {
            need = 2
            lo = 0xA0
        } else if c >= 0xE1 && c <= 0xEC || c == 0xEE || c == 0xEF {
            need = 2
        } else if c == 0xED {
            need = 2
            hi = 0x9F
        } else if c == 0xF0 {
            need = 3
            lo = 0x90
        } else if c >= 0xF1 && c <= 0xF3 {
            need = 3
        } else if c == 0xF4 {
            need = 3
            hi = 0x8F
        } else {
            return false
        }
        if i + need >= n {
            return false // truncated
        }
        d := b[i + 1]
        if d < lo || d > hi {
            return false
        }
        for k in 2..need + 1 {
            e := b[i + k]
            if e < 0x80 || e > 0xBF {
                return false
            }
        }
        i += need + 1
    }
    return true
}
