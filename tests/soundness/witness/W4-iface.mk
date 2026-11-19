// verdict: reject E0608
// note: through an interface (R9): an impl method with a whole clause does not fit an interface method with a per-field one unless the whole clause is covered in every field; a generic caller reads field r through the interface's clause, so it would hold the loan on y unseen (M-F8-W4-cover-flat)
struct Pair {
    r: &i32
    s: &i32
}

interface Mk {
    Make(&self, a: &i32, b: &i32): Pair from (r: a, s: b)
}

struct T {
    n: i32
}

impl Mk for T {
    Make(&self, a: &i32, b: &i32): Pair from a, b { // <- E0608
        return Pair(r: b, s: a)
    }
}

Run[M: Mk](m: &M) {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        Pair(r, s) := m.Make(&x, &y)
        Use(s)
        keep = r
    }
    Use(keep)
}

Caller() {
    t := T(n: 1)
    Run(&t)
}
