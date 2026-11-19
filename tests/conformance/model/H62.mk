// verdict: reject E0601
// case: H62
export struct Tokens2 {
    export src: str
    buf: [8]u8
}

export Tokens2.Word(&self): str { // <- E0601
    return self.src
}
