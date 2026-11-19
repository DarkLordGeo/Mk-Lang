// verdict: reject E0604
// case: R11.NoInto
// also: H36
struct Index {
    words: List[str]
}

Index.Add(mut self, word: str): OutOfMemory!void {
    try self.words.Push(word) // <- E0604
}
