// verdict: reject E0501
// rule: REF-099
// note: a mut-receiver call with explicit generic arguments, s.Flip[0](), inside `for x in s.buf` over a reassignable mut []int place is a whole write of s, so the loop holds a shared view and the call conflicts with it. Without the view the loop would read past the swapped-in shorter slice in release (witness CICE2-l5.mk)
struct S {
    buf: mut []int
    alt: mut []int
}

S.Flip[const K: int](mut self) {
    Swap(mut self.buf, mut self.alt)
}

Seen(s: mut S): int {
    var n := 0
    for x in s.buf {
        s.Flip[0]() // <- E0501
        n = n + x
    }
    return n
}
