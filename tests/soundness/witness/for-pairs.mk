// verdict: reject E0505
// note: R9 at the caller: `for (r, s) in it` takes the payload of the Next result (a record: a for head destructures records) into a hidden temporary and destructures it. Next has one parameter, self, so every field's sources are paths of self, which give the union at a call: r keeps the loan on y (E0505 is anchored where y's loan enters it), and the per-field split cannot be observed here
struct It {
    a: &i32
    b: &i32
    n: i32
}

It.Next(mut self): ?(r: &i32, s: &i32) from (r: self.a, s: self.b) {
    if self.n == 0 {
        return none
    }
    self.n -= 1
    return some((r: self.a, s: self.b))
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        it := It(a: &x, b: &y, n: 1) // <- E0505
        for (r, s) in it {
            Use(s)
            keep = r
        }
    }
    Use(keep)
}
