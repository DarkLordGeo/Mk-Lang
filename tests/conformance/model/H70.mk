// verdict: ok
// case: H70
struct Index {
    words: List[str]
}

Index.First(&self): str from self.words {
    return self.words[0]
}

Index.Add(mut self, word: str into self): OutOfMemory!void {
    try self.words.Push(word)
}

Caller(ix: mut Index): OutOfMemory!void {
    a := ix.First()
    try ix.Add("b")
    Use(&a)
}
