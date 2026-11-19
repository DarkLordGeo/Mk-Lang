// verdict: ok
// rule: D-252
// note: a literal in a const argument reads a parameter and a local, borrows one with &, and calls a &self method on it
struct Origin {
    x: i32
}

Origin.Dist(&self, v: i32): i32 {
    d := v - self.x
    return d * d
}

ByDistance(xs: mut []i32, x: i32) {
    o := Origin(x: x)
    scale: i32 = 2
    xs.Sort(fn(a: &i32, b: &i32): bool {
        r := &o
        return r.Dist(*a) * scale < o.Dist(*b) * scale
    })
}
