// Why: std modules above std.core (fs.ReadText) need to turn whole buffers
// into str values, and must not be able to hand unchecked bytes to a str.
//
// What: StrFrom, the one std-internal constructor of a `str`.
//
// How: the bytes must pass std.simd.ValidUtf8 (Keiser and Lemire's lookup
// validator, the vector form chosen at run time by simd.Cpu()), and the str
// is a view of their memory. `str.From` runs the same validator from 256
// bytes on a hosted target and std.core's word forms on shorter text and
// on x86_64-freestanding, which accept exactly the same bytes (Unicode
// table 3-7; std.simd's differential tests against Go's unicode/utf8 hold
// the validators to the same answers), and fails with the same Utf8Error.
// std.rt is std-internal (E0202), so no user module can call it; the
// validation is inside, so no caller can hand it unchecked bytes.

import std.raw
import std.simd

/// StrFrom views bytes as text when they are valid UTF-8, else fails with
/// Utf8Error. O(n); allocates nothing.
export StrFrom(bytes: []u8): Utf8Error!str {
    if !simd.ValidUtf8(bytes) {
        fail Utf8Error
    }
    unsafe "bytes is valid UTF-8, checked above by simd.ValidUtf8; the view shares its memory, as str.From's does" {
        return raw.Str(raw.Data(bytes), bytes.Len())
    }
}
