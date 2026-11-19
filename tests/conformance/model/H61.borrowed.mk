// verdict: reject E0502
// case: H61.borrowed
struct Lexer {
    src: str
    pos: int
}

struct Tok {
    text: str
    lx: &Lexer
}

Lexer.Tok(&self): Tok {
    return Tok(text: self.src, lx: self)
}

Two(lx: mut Lexer) {
    t := lx.Tok()
    lx.pos = 1 // <- E0502
    Use(&t)
}
