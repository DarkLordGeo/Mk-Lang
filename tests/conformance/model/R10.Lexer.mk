// verdict: ok
// case: R10.Lexer
// also: H08
struct Lexer {
    src: str
    pos: int
}

Lexer.Next(mut self): ?str {
    start := self.pos
    self.pos += 1
    return some(self.src[start..self.pos])
}

Two(lx: mut Lexer) {
    a := lx.Next()
    b := lx.Next()
    Use(&a)
    Use(&b)
}
