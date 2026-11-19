// verdict: reject E0505
// note: field-level loan sets, shape 14: under ?T a binding is (_t as some).0.f, so the field key is the first field after the payload pair; v holds the set of field s, the loan on y. Keyed on the payload slot .0, v would get the set of field r (M-F8-FR5-payload-key)
struct Pair {
    r: &i32
    s: &i32
}

O(a: &i32, b: &i32): ?Pair from (r: a, s: b) {
    return some(Pair(r: a, s: b))
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        match O(&x, &y) {
            some(Pair(r: u, s: v)) {
                Use(u)
                keep = v // <- E0505
            }
            none {
            }
        }
    }
    Use(keep)
}
