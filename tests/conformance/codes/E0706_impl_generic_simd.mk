// verdict: reject E0706
// note: a method of an interface impl over impl[T] cannot call a callee whose compile-time clause reads T, std.simd's widening forms over T included: the clause of `self.v.WidenLo[T, 4]()` reads `SizeOf[T]()` of the impl's T, which the method cannot state, so the call is E0706 "cannot be judged here"
import std.simd

interface Widen {
    Lo(&self): int
}

struct Px[T: simd.Lane + Plain] {
    v: simd.Vec[u8, 8]
}

Use[T: simd.Lane + Plain](w: simd.Vec[T, 4]): int {
    return 5
}

impl[T: simd.Lane + Plain] Widen for Px[T] {
    Lo(&self): int {
        w := self.v.WidenLo[T, 4]() // <- E0706
        return Use(w)
    }
}

Call[X: Widen](x: &X): int {
    return x.Lo()
}

export Main(sys: &System): int {
    p := Px[u16](v: simd.Vec[u8, 8].Splat(9))
    return Call(&p)
}
