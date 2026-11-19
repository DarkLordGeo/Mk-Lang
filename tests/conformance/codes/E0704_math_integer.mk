// verdict: reject E0704
// note: math.Integer is computed by the compiler, the integer types, and never implemented, as simd.Lane is not
import std.math

struct Big {
    limbs: [4]u64
}

impl math.Integer for Big { // <- E0704
}
