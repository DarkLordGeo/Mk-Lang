import std.simd

Double8(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] cpu "avx2" {
    return v + v
}

Quad8(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] cpu "x86-64-v3" {
    return Double8(Double8(v))
}

export Double(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] {
    if simd.Cpu().Has("x86-64-v3") {
        unsafe "Cpu() reports x86-64-v3, which Quad8 needs" {
            return Quad8(v)
        }
    }
    return v + v
}

export Pick(): fn(simd.Vec[f32, 8]): simd.Vec[f32, 8] {
    if simd.Cpu().Has("avx2") {
        unsafe "Cpu() reports avx2, which Double8 needs" {
            return Double8
        }
    }
    return Same
}

Same(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] {
    return v
}
