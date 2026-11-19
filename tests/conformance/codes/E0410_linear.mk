// verdict: reject E0410
// note: the private `t` makes `Holder` linear (a struct's kind is the worst of its fields), so an exported declaration must say `linear`
linear struct Token {
    id: int
}

export struct Holder { // <- E0410
    t: Token
}
