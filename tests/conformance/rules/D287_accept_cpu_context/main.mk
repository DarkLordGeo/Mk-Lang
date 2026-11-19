// verdict: ok
// rule: D-287
// note: a function whose clause implies the callee's features calls it with no unsafe (x86-64-v3 implies avx2), a guarded call sits in unsafe, and a call through the value needs nothing; the module that writes unsafe is listed in pkg.mk
import k
import std.simd

Run(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] {
    f := k.Pick()
    return f(k.Double(v))
}
