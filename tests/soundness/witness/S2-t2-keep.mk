// verdict: ok
// note: in Pl.Keep, `SetStr(mut self.text, self.text)` stays in one field, and its store needs no `*self into self`: R11's first-level exemption covers the callee's store, so the program is clean
struct Pl {
    text: &i32
    sep: &i32
}

SetStr(x: mut &i32, v: &i32 into x) {
    *x = v
}

Pl.Keep(mut self) {
    SetStr(mut self.text, self.text)
}
