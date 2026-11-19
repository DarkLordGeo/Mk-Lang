// verdict: reject E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. Precision, not soundness. Push2(mut out, &s) with Push2(l, q: &str) *q into l: that store judges q's referent, str, which is shallow, so the loan of xs's contents that s holds lands shallow in out and the next iteration's walk does not go on through it. Its E0604 at the Push2 call asks for `*xs into a`, since out holds a loan of a (p-star-q.mk writes it). Judged by q's own type, &str (a carrier referent), the loan would stay deep and `*xs into xs` would be owed too: a precision loss, never an unsound acceptance
// before FLD-3: reject E0604 at 12 E0604 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Push2(l: mut List[str], q: &str) *q into l {
    l.Push(*q) else { return }
}

CopyAll(a: &Arena, xs: &List[str]): OutOfMemory!List[str] from a, xs {
    var out := List[str].New(a)
    for i in 0..xs.Len() {
        s := xs[i]
        Push2(mut out, &s) // <- E0604
    }
    return out
}

export Main(sys: &System) {
}
