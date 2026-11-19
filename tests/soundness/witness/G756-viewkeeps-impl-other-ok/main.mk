// verdict: ok
// note: a precision witness for the impl scan: `impl Src[&Mutex[?&i32]] for V[Mutex[?&i32]]` does not apply to V[i32], whose For it is not (implArgs), so the source-less MakeV's V[i32] keeps nothing and the declared store of a local into it is no store into global memory. It is accepted and prints `k 12`. With every impl taken to apply (mutant M-D756-viewkeeps-impl-match) it is refused (E0605)
import std.raw

var GM: Mutex[?&i32] = Mutex[?&i32].New(none)
var GI: i32 = 7

view struct V[T] {
    p: *T
}

V[T].Put(&self, x: &i32 into self) {
    _ = self
    _ = x
}

interface Src[R] {
    Get(&self): R
}

impl Src[&Mutex[?&i32]] for V[Mutex[?&i32]] {
    Get(&self): &Mutex[?&i32] from self.p {
        unsafe "probe: p was set from GM" {
            return &*self.p
        }
    }
}

MakeV(): V[i32] {
    return V[i32](p: raw.Of(&GI))
}

Work(): i32 {
    var n: i32 = 5
    v := MakeV()
    v.Put(&n)
    var k: i32 = 0
    unsafe "probe: p was set by MakeV from GI" {
        k = *v.p + n
    }
    return k
}

export Main(sys: &System): WriteError!void {
    k := Work()
    try sys.Print("k {k}\n", (k))
}
