// verdict: reject E0602
// note: a by-value view parameter's rest holds its own loan. Swap copies its by-value view parameter h, rewrites the only carrier field and returns the copy `from t`. The copy's rest still holds h's own loan (locals.go splitEntry fills every carrier field and the rest), so the return is E0602: the result may keep, in raw storage, what h referred to. With the rest empty (mutant M-P4-view-paramrest) the program checks clean, and a dev `mk run` prints a value such as `v=461` where the live value was 7, Get reading Inner's dead n. This package's pkg.mk lists `.` in unsafe_modules
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

Swap(h: H, t: &i32): H from t {
    var h2 := h
    h2.tag = t
    return h2 // <- E0602
}

Inner(t: &i32): H from t {
    n: i32 = 7
    h := H(p: raw.Of(&n), tag: &n)
    return Swap(h, t)
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
    h := Inner(&t)
    k := Clobber(5)
    v := h.Get()
    try Print(mut out, "v={v} k={k}\n", (v, k))
}
