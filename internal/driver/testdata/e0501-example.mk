// doc/tools.md section 3: Pager is a view of its text, so
// the result of Next stays borrowed from the whole pager (no from clause).

struct Pager {
    text: str
    buf: mut []u8
    pos: int
}

// Next returns only what self.text holds; the fix engine (deferred,
// DG142) would add `from self.text` after the result type.
Pager.Next(mut self): ?str {
    if self.pos >= self.text.Len() {
        return none
    }
    self.pos += 1
    return some(self.text)
}

Pager.Skip(mut self) {
    self.pos += 1
}

Use(s: &?str) {
}













Lex(pager: mut Pager) {
    a := pager.Next()
    pager.Skip()
    // a is still live here
    Use(&a)
}
