// verdict: reject E0505
// note: the rest of a view's split shape for a per-field result: Make's body puts x's loan in raw storage and its clause gives x to tag, so the call writes tag {n, t} and the rest the clause's union. The strong write h.tag = t empties tag; the rest keeps n, so n's end while h is live is E0505. With the rest of a per-field result left empty (mutant M-P4-view-clause-rest) or with no rest (M-P4-view-rest) the program checks clean, and `mk run` prints `v=4242`, reading Inner's dead n where the live value was 7. Make's signature is sound: its clause names x, which its raw storage holds. This package's pkg.mk lists `.` in unsafe_modules
import std.raw

view struct H {
    p: *i32
    tag: &i32
}

H.Get(&self): i32 {
    unsafe "witness: the view reads its raw storage" {
        return raw.Read(self.p)
    }
}

Make(x: &i32, t: &i32): H from (tag: t, x) {
    return H(p: raw.Of(x), tag: t)
}

Inner(t: &i32, c: bool): H from t {
    var h := H(p: raw.Null[i32](), tag: t)
    if c {
        n: i32 = 7
        h = Make(&n, t) // <- E0505
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
