// verdict: reject E0505
// note: the direct write rule reaches t through w's two loans, on t.a and on t.b (it walks from w's union), so the store lands in every field of t (store.go directWrite's merge to -1). Mutant M-RV2-direct-merge (the first field reached wins) puts y in t.a only and accepts it, while `*w.q = &y` wrote t.b, and Use(t.b) would read y after its scope
struct Two {
    a: &i32
    b: &i32
}

struct Refs {
    p: mut &i32
    q: mut &i32
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        w := Refs(p: mut t.a, q: mut t.b)
        *w.q = &y // <- E0505
    }
    Use(t.b)
}
