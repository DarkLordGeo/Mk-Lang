// verdict: ok
// note: a precision witness for the impl scan: a generic view's generic impl, `impl[T] Src[&T] for V[T]`, judged at V[i32] with T bound to i32 by matching its For against the view type (implArgs), so its Get hands out &i32, which takes no store, and the declared store of a local into the source-less MakeV's result (`x into self`) is no store into global memory. It is accepted and prints `k 12`. With the impl's parameters left unbound (mutant M-D756-viewkeeps-impl-bind) it is refused (E0605)
import std.raw

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

impl[T] Src[&T] for V[T] {
    Get(&self): &T from self.p {
        unsafe "probe: p was set by MakeV from GI" {
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
    m := v.Get()
    return *m + n
}

export Main(sys: &System): WriteError!void {
    k := Work()
    try sys.Print("k {k}\n", (k))
}
