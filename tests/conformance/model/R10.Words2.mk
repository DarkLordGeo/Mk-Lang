// verdict: ok
// case: R10.Words2
view struct Words2 {
    text: str
    pos: int
}

Words2.Next(mut self): ?str from self.text {
    start := self.pos
    self.pos = self.text.Len()
    return some(self.text[start..])
}

Two(w: mut Words2) {
    a := w.Next()
    b := w.Next()
    Use(&a)
    Use(&b)
}

struct Index {
    words: List[str]
}

Index.First(&self): str from self.words {
    return self.words[0]
}
