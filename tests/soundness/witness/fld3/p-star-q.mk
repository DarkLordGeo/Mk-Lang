// verdict: ok
// note: a precision witness: twin-star-q.mk with the clause `*xs into a` written. Push2's `*q into l` judges q's referent, str, which is shallow, so the stored loans are shallow in out and the next call does not walk through them into xs. Witness of the precision row M-F3-star-q-by-q (judged by q's own type, &str, whose referent str is a carrier, the loans are deep and `*xs into xs` is owed)
// before FLD-3: reject E0604 at 12 E0604 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Push2(l: mut List[str], q: &str) *q into l {
    l.Push(*q) else { return }
}

CopyAll(a: &Arena, xs: &List[str]): OutOfMemory!List[str] from a, xs *xs into a {
    var out := List[str].New(a)
    for i in 0..xs.Len() {
        s := xs[i]
        Push2(mut out, &s)
    }
    return out
}

export Main(sys: &System) {
}
