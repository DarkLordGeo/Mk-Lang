// verdict: ok
// case: H62.clause
export struct Tokens2 {
    export src: str
    buf: [8]u8
}

export Tokens2.Word(&self): str from self.src {
    return self.src
}
