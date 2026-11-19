// verdict: reject E0505
// note: the rest of a view's split shape, its own witness: H keeps a raw alias of n beside its carrier field tag. The literal gives tag n's loan and the rest every operand's loans; the strong write h.tag = t empties tag, and the rest keeps n, so n's end while h is live (it is returned) is E0505. Without the rest (mutant M-P4-view-rest: a view split with nothing kept for what lies outside its fields) the program checks clean, and `mk run` prints `v=4242`, h.Get reading Inner's dead n through the raw pointer where the live value was 7. This package's pkg.mk lists `.` in unsafe_modules
import std.raw

view struct H {
    p: *i32
    tag: &i32
}

H.Get(&self): i32 {
    unsafe "probe: the view reads its raw storage" {
        return raw.Read(self.p)
    }
}

Inner(t: &i32, c: bool): H from t {
    var h := H(p: raw.Null[i32](), tag: t)
    if c {
        n: i32 = 7
        h = H(p: raw.Of(&n), tag: &n) // <- E0505
        h.tag = t
    }
    return h
}

Clobber(n: i32): i32 {
    var buf: [64]i32 = [n; 64]
    var acc: i32 = 0
    for i in 0..64 {
        buf[i] = buf[i] + 4237
        acc = acc + buf[i]
    }
    return acc
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    t: i32 = 1
    h := Inner(&t, true)
    k := Clobber(5)
    v := h.Get()
    try Print(mut out, "v={v} k={k}\n", (v, k))
}
