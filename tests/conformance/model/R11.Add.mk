// verdict: ok
// case: R11.Add
// note: C7-2: SwapRefs, not Swap: a module-level `Swap` hides the prelude's and its body would call itself
struct Index {
    words: List[str]
}

Index.Add(mut self, word: str into self): OutOfMemory!void {
    try self.words.Push(word)
}

SwapRefs[T](a: mut T, b: mut T) *a into b, *b into a {
    Swap(a, b)
}

struct Deep {
    a: &i32
    b: mut &i32
}

Deep.Mix(mut self) *self into self {
    *self.b = self.a
}
