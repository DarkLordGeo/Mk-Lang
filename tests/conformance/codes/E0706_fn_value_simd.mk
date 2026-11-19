// verdict: reject E0706
// note: std.simd's lane-count requires is evaluated where a method is named as a fn value, as at its call: 10 lanes is no power of two from 2 to 64
import std.simd

const Widen = simd.Vec[i8, 10].WidenLo[i16, 5] // <- E0706
