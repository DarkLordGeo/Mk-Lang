// verdict: reject E0501
// case: X.FromP
// note: R10: `from p` keeps p borrowed whatever the table says
struct Lexer {
    src: str
    pos: int
}

Lexer.Next(mut self): ?str from self {
    return some(self.src)
}

Two(lx: mut Lexer) {
    a := lx.Next()
    b := lx.Next() // <- E0501
    Use(&a)
    Use(&b)
}
