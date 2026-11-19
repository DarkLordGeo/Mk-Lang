// verdict: reject E0308
// rule: D-262
// note: on integer lanes + - * and unary - are E0308 (naming +% and AddSat), and / and % are E0308 (naming DivTrunc); float lanes have no %
import std.simd

Plus(v: simd.Vec[i32, 4]): simd.Vec[i32, 4] {
    return v + v // <- E0308
}

Minus(v: simd.Vec[u8, 16]): simd.Vec[u8, 16] {
    return v - v // <- E0308
}

Times(v: simd.Vec[i64, 2]): simd.Vec[i64, 2] {
    return v * v // <- E0308
}

Negate(v: simd.Vec[i16, 8]): simd.Vec[i16, 8] {
    return -v // <- E0308
}

Divide(v: simd.Vec[u32, 4]): simd.Vec[u32, 4] {
    return v / v // <- E0308
}

Rem(v: simd.Vec[f64, 2]): simd.Vec[f64, 2] {
    return v % v // <- E0308
}
