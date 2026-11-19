// Why: binary formats, hashing and NaN boxing need a float's exact bits,
// and a conversion through arithmetic would round, quiet or canonicalize a
// NaN on the way.
//
// What: f64.Bits, f64.FromBits, f32.Bits and f32.FromBits.
//
// How: each is one raw intrinsic: a bitcast in codegen, Go's
// math.Float64bits and back in the compile-time evaluator. Total and exact
// on every target, NaN payloads and the sign of zero included. O(1), no
// allocation, no error.

import std.raw

/// The IEEE 754 binary64 encoding of self, bit for bit, NaN payloads and the
/// sign of zero included, on every target and at compile time. O(1).
export f64.Bits(self): u64 {
    return raw.F64Bits(self)
}

/// The f64 whose binary64 encoding is b, bit for bit: every u64 is one, a
/// signalling NaN's payload included, on every target and at compile time.
/// O(1).
export f64.FromBits(b: u64): f64 {
    return raw.F64From(b)
}

/// The IEEE 754 binary32 encoding of self, bit for bit, NaN payloads and the
/// sign of zero included, on every target and at compile time. O(1).
export f32.Bits(self): u32 {
    return raw.F32Bits(self)
}

/// The f32 whose binary32 encoding is b, bit for bit: every u32 is one, a
/// signalling NaN's payload included, on every target and at compile time.
/// O(1).
export f32.FromBits(b: u32): f32 {
    return raw.F32From(b)
}
