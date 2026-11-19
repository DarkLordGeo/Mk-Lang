// verdict: ok
// rule: R9
// rule: R18
// note: each exported struct states the kind its private fields give it, else E0410: a carrier says `view` (`Name`), a unique says `view` (`Counter`), a linear says `linear` (`Holder`)
linear struct Token {
    id: int
}

export view struct Name {
    text: str
}

export view struct Counter {
    n: Cell[int]
}

export linear struct Holder {
    t: Token
}
