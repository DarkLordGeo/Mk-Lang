// verdict: reject E0706
// rule: D-262
// note: N is a power of two from 2 to 64, a requires checked at each call of the instance
import std.simd

Three(x: i32): i32 {
    v := simd.Vec[i32, 3].Splat(x) // <- E0706
    return v.ReduceSumWrap()       // <- E0706
}
