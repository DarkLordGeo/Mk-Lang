// verdict: ok
// case: H61
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
