// verdict: reject E0505
// note: the writable walk reaches t through two loans that name different fields (w.p on t.a, w.q on t.b), so a store of the call's `x into p` step lands in every field (store.go writableSkip's merge to -1); Set2 writes v into t.b through w.q. Mutant M-RV2-walk-merge (the first field reached wins) puts y in t.a only and accepts it, and Use(t.b) would read y after its scope
struct Two {
    a: &i32
    b: &i32
}

struct Refs {
    p: mut &i32
    q: mut &i32
}

Set2(v: &i32 into w, w: mut Refs) {
    *w.q = v
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        var w := Refs(p: mut t.a, q: mut t.b)
        Set2(&y, mut w) // <- E0505
    }
    Use(t.b)
}
