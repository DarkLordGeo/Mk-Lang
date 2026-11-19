// verdict: reject E0505
// case: R11.Fill
struct Index {
    words: List[str]
}

Index.Add(mut self, word: str into self): OutOfMemory!void {
    try self.words.Push(word)
}

Fill(index: mut Index, a: &Arena): OutOfMemory!void {
    var t := Text.New(a)
    try t.Push("word")
    try index.Add(t.Str()) // <- E0505
}
