// verdict: reject E0604
// case: X.Ref8.TokensAll
// note: the List's storage lives in the arena, so a store into the list is a store into the arena; the accepted form declares `*self into a`
struct Tokens {
    src: str
    pos: int
}

Tokens.Rest(mut self): str {
    start := self.pos
    self.pos = self.src.Len()
    return self.src[start..]
}

Tokens.All(mut self, a: &Arena): OutOfMemory!List[str] from a, self.src {
    var out := List[str].New(a)
    try out.Push(self.Rest()) // <- E0604
    return out
}
