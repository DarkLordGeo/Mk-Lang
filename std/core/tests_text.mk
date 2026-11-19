// Why: Text.Done hands a text's bytes to its arena with no copy, and
// str.Replace builds on Text; both must give exact bytes and grow sanely.
// What: test blocks for Text.Done, str.Replace and Text.PushReplaced against
// the naive oracle TtNaive, their growth, and failure under a heap budget.
// How: run by `mk test --std=std.core` with the counting heap; random texts
// come from TtRng. Helper names start with Tt, because every file of std/core
// is one module.

import std.raw

// TtJoin builds the words joined by sep in a and returns them through
// Text.Done: a result from a, with no copy (E0603 with t.Str()).
TtJoin(a: &Arena, words: []str, sep: str): OutOfMemory!str from a {
    var t := Text.New(a)
    for i, w in words {
        if i > 0 {
            try t.Push(sep)
        }
        try t.Push(w)
    }
    return t.Done()
}

// TtFormatted writes through the text's Writer and then ends the text.
TtFormatted(a: &Arena, n: int): OutOfMemory!str from a {
    var t := Text.New(a)
    var w := t.Writer()
    Print(mut w, "n={n};", (n)) else {
        fail OutOfMemory
    }
    return t.Done()
}

test "Text.Done gives the text's bytes as a str in its arena" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    words := ["alpha", "\u{E9}t\u{E9}", "", "omega"]
    s := try TtJoin(&ar, words[..], ", ")
    Assert(s == "alpha, \u{E9}t\u{E9}, , omega")
    Assert(s.Len() == 21)
    Assert(try TtJoin(&ar, words[0..0], ", ") == "")
    Assert(try TtFormatted(&ar, 42) == "n=42;")
}

test "Text.Done allocates nothing and keeps the text's buffer" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var t := Text.New(&ar)
    try t.Push("abc")
    try t.PushChar('\u{1F600}')
    p := raw.StrData(t.Str())
    s := t.Done()
    Assert(s == "abc\u{1F600}" && s.Len() == 7)
    Assert(raw.Same(raw.StrData(s), p))
}

test "Text.Done of an empty text is the empty str" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    t := Text.New(&ar)
    s := t.Done()
    Assert(s.Len() == 0 && s == "")
}

test "Text.Done keeps its bytes past a later text in the arena" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    pair := ["one", "two"]
    first := try TtJoin(&ar, pair[..], "+")
    var u := Text.New(&ar)
    for _ in 0..1000 {
        try u.Push("xyz")
    }
    second := u.Done()
    Assert(first == "one+two")
    Assert(second.Len() == 3000 && second[2997..] == "xyz")
}

// str.Replace and Text.PushReplaced. TtNaive is the oracle: at each
// offset, old's bytes compared one by one, so a match is the leftmost one
// that does not overlap the last; an empty old puts new between scalars.
TtNaive(a: &Arena, s: str, old: str, new: str): OutOfMemory!str from a {
    var t := Text.New(a)
    b := s.AsBytes()
    p := old.AsBytes()
    n := b.Len()
    m := p.Len()
    if m == 0 {
        try t.Push(new)
        var i := 0
        for i < n {
            k := ScalarLen(b[i])
            try t.PushBytes(b[i..i + k])
            try t.Push(new)
            i += k
        }
        return t.Done()
    }
    var i := 0
    for i < n {
        var same := i + m <= n
        var j := 0
        for same && j < m {
            if b[i + j] != p[j] {
                same = false
            }
            j += 1
        }
        if same {
            try t.Push(new)
            i += m
        } else {
            try t.PushBytes(b[i..i + 1])
            i += 1
        }
    }
    return t.Done()
}

// TtRng is xorshift64 (Marsaglia, 2003): a fixed sequence, so a failure
// repeats.
struct TtRng {
    s: u64
}

TtRng.Below(mut self, n: int): int {
    var x := self.s
    x = x ^ (x << 13)
    x = x ^ (x >> 7)
    x = x ^ (x << 17)
    self.s = x
    return Cast[int](x % Cast[u64](n))
}

// TtPieces are the scalars random texts are made of: two ASCII letters, so
// matches are frequent and overlap, and scalars of 2, 3 and 4 bytes, some
// sharing a continuation byte (A9 in U+00E9 and U+00A9, U+20AC's 82 AC).
const TtPieces = ["a", "b", "a", "\u{E9}", "\u{A9}", "\u{20AC}", "\u{1F600}", "b"]

TtRandom(a: &Arena, r: mut TtRng, pieces: int): OutOfMemory!str from a {
    var t := Text.New(a)
    for _ in 0..pieces {
        try t.Push(TtPieces[r.Below(TtPieces.Len())])
    }
    return t.Done()
}

test "Replace replaces every match, leftmost first" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    a := &ar
    Assert(try "a-b-c".Replace(a, "-", "+") == "a+b+c")
    Assert(try "one two one".Replace(a, "one", "three") == "three two three")
    Assert(try "one two one".Replace(a, "one", "1") == "1 two 1")
    Assert(try "xxabcxx".Replace(a, "abc", "") == "xxxx")
    // At the start, at the end, the whole text, and longer than the text.
    Assert(try "abcab".Replace(a, "ab", "X") == "XcX")
    Assert(try "abc".Replace(a, "abc", "") == "")
    Assert(try "abc".Replace(a, "abc", "\u{1F600}") == "\u{1F600}")
    Assert(try "ab".Replace(a, "abc", "X") == "ab")
    Assert(try "".Replace(a, "a", "X") == "")
    Assert(try "abc".Replace(a, "x", "y") == "abc")
}

test "matches do not overlap and new is not searched" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    a := &ar
    Assert(try "aaa".Replace(a, "aa", "b") == "ba")
    Assert(try "aaaa".Replace(a, "aa", "b") == "bb")
    Assert(try "abababa".Replace(a, "aba", "X") == "XbX")
    Assert(try "aaaaa".Replace(a, "aaa", "a") == "aaa")
    // new holds old: each match is replaced once.
    Assert(try "aaa".Replace(a, "a", "aa") == "aaaaaa")
    Assert(try "abab".Replace(a, "ab", "aab") == "aabaab")
    // A replacement that forms a new match with the text after it.
    Assert(try "aab".Replace(a, "ab", "a") == "aa")
    Assert(try "a-b--c".Replace(a, "-", "") == "abc")
}

test "an empty old puts new at every scalar boundary" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    a := &ar
    Assert(try "".Replace(a, "", "x") == "x")
    Assert(try "abc".Replace(a, "", "-") == "-a-b-c-")
    Assert(try "h\u{E9}\u{20AC}\u{1F600}".Replace(a, "", "|") == "|h|\u{E9}|\u{20AC}|\u{1F600}|")
    Assert(try "h\u{E9}".Replace(a, "", "") == "h\u{E9}")
    Assert(try "ab".Replace(a, "", "\u{E9}") == "\u{E9}a\u{E9}b\u{E9}")
}

test "UTF-8 text matches only whole scalars" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    a := &ar
    Assert(try "\u{E9}t\u{E9}".Replace(a, "\u{E9}", "e") == "ete")
    Assert(try "a\u{20AC}b\u{20AC}".Replace(a, "\u{20AC}", "\u{1F600}") == "a\u{1F600}b\u{1F600}")
    // U+00A9 is C2 A9 and U+00E9 is C3 A9: the shared A9 is not a match.
    Assert(try "\u{E9}\u{E9}".Replace(a, "\u{A9}", "X") == "\u{E9}\u{E9}")
    Assert(try "\u{E9}\u{A9}\u{E9}".Replace(a, "\u{A9}", "X") == "\u{E9}X\u{E9}")
    r := try "\u{1F600}\u{1F600}".Replace(a, "\u{1F600}", "\u{E9}")
    Assert(r == "\u{E9}\u{E9}" && r.Len() == 4)
}

test "the result is a copy in the arena, also with no match" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    s := "nothing to see here"
    r := try s.Replace(&ar, "zzz", "y")
    Assert(r == s)
    Assert(!raw.Same(raw.StrData(r), raw.StrData(s)))
}

test "PushReplaced appends, and a cleared text reuses its buffer" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var t := Text.New(&ar)
    try t.Push("[")
    try t.PushReplaced("a.b.c", ".", "::")
    try t.Push("]")
    Assert(t.Str() == "[a::b::c]")
    try t.PushReplaced("", "x", "y")
    try t.PushReplaced("q", "", "_")
    Assert(t.Str() == "[a::b::c]_q_")
    p := raw.StrData(t.Str())
    t.Clear()
    try t.PushReplaced("one two one", "one", "1")
    Assert(t.Str() == "1 two 1")
    Assert(raw.Same(raw.StrData(t.Str()), p))
}

// A text appended to by many PushReplaced calls, new no longer than old,
// with an arena Put between calls so the buffer never grows in place: its
// room grows as List's does, so it moves about log2 times, not once a call.
test "PushReplaced keeps a growing text's amortized growth" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var t := Text.New(&ar)
    var moves := 0
    var p := raw.StrData(t.Str())
    for i in 0..2000 {
        try t.PushReplaced("abcd-efgh", "-", "")
        _ = try ar.Put(i)
        q := raw.StrData(t.Str())
        if !raw.Same(q, p) {
            moves += 1
            p = q
        }
    }
    Assert(t.Str().Len() == 2000 * 8)
    Assert(moves <= 16)
}

test "Replace agrees with the naive oracle on random text" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var r := TtRng(s: 0x9E3779B97F4A7C15)
    for _ in 0..3000 {
        var w := try ar.Sub()
        defer w.Free()
        s := try TtRandom(&w, mut r, r.Below(60))
        old := try TtRandom(&w, mut r, r.Below(5))
        new := try TtRandom(&w, mut r, r.Below(4))
        got := try s.Replace(&w, old, new)
        want := try TtNaive(&w, s, old, new)
        Assert(got == want)
        var t := Text.New(&w)
        try t.Push(old)
        try t.PushReplaced(s, old, new)
        Assert(t.Str().Len() == old.Len() + want.Len())
        Assert(t.Str()[old.Len()..] == want)
    }
}

test "Replace over a large input" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var t := Text.New(&ar)
    line := "the cat sat on the mat, caf\u{E9} \u{20AC}5 \u{1F600}\n"
    for _ in 0..40000 {
        try t.Push(line)
    }
    s := t.Done()
    Assert(s.Len() == 40000 * line.Len() && s.Len() > 1 << 20)
    longer := try s.Replace(&ar, "cat", "elephant")
    Assert(longer.Len() == s.Len() + 40000 * 5)
    Assert(longer == try TtNaive(&ar, s, "cat", "elephant"))
    shorter := try s.Replace(&ar, "the", "a")
    Assert(shorter.Len() == s.Len() - 80000 * 2)
    Assert(shorter == try TtNaive(&ar, s, "the", "a"))
    multi := try s.Replace(&ar, "\u{20AC}5 \u{1F600}", "\u{A9}")
    Assert(multi.Len() == s.Len() - 40000 * 7)
    Assert(multi[0..line.Len() - 7] == "the cat sat on the mat, caf\u{E9} \u{A9}\n")
    // One-byte old (std.simd's IndexByte), and Two-Way's worst case: a
    // periodic text and a needle that almost matches everywhere.
    nl := try s.Replace(&ar, "\n", "")
    Assert(nl.Len() == s.Len() - 40000)
    var u := Text.New(&ar)
    for _ in 0..(1 << 20) {
        try u.Push("a")
    }
    try u.Push("b")
    aaa := u.Done()
    tail := try aaa.Replace(&ar, "aaaaaaab", "X")
    Assert(tail.Len() == aaa.Len() - 7 && tail.EndsWith("aX"))
    Assert(tail[0..tail.Len() - 1] == aaa[0..aaa.Len() - 8])
    halves := try aaa.Replace(&ar, "aa", "b")
    Assert(halves.Len() == (1 << 19) + 1 && halves.StartsWith("bbbb") && halves.EndsWith("bb"))
}

// TtBudget is a heap that forwards left more allocations, or growing
// resizes, to inner and refuses every one after: ctx is a TtBudget in the
// test's frame.
struct TtBudget {
    left: int
    inner: *Alloc
}

TtBudgetAlloc(ctx: *u8, size: int, align: int): *u8 {
    unsafe "ctx is a live TtBudget" {
        c := raw.PtrCast[u8, TtBudget](ctx)
        if (*c).left == 0 {
            return raw.Null[u8]()
        }
        (*c).left -= 1
        return AllocBytes(&*(*c).inner, size, align)
    }
}

TtBudgetResize(ctx: *u8, p: *u8, old: int, new: int, align: int): bool {
    unsafe "ctx is a live TtBudget" {
        c := raw.PtrCast[u8, TtBudget](ctx)
        if new > old {
            if (*c).left == 0 {
                return false
            }
            (*c).left -= 1
        }
        return ResizeBytes(&*(*c).inner, p, old, new, align)
    }
}

TtBudgetFree(ctx: *u8, p: *u8, size: int, align: int) {
    unsafe "ctx is a live TtBudget" {
        FreeBytes(&*(*raw.PtrCast[u8, TtBudget](ctx)).inner, p, size, align)
    }
}

const TtBudgetTable = AllocTable(alloc: TtBudgetAlloc, resize: TtBudgetResize, free: TtBudgetFree)

TtBudgetHeap(c: *TtBudget): Heap {
    unsafe "c is the test's TtBudget, live while the heap is used" {
        return MakeHeap(raw.PtrCast[TtBudget, u8](c), raw.Of(&TtBudgetTable))
    }
}

TtSetBudget(c: *TtBudget, n: int) {
    unsafe "c is the test's live TtBudget" {
        (*c).left = n
    }
}

// Each arena gets its first chunk (4 KiB) and nothing more, so an output
// past it (the 8,000-byte text replaced) fails: new longer than old fails after some of the output is
// appended, new no longer than old at the one reservation, an empty old
// part way. Each time the text is as it was, and the retry, with the heap
// serving again, gives the whole output.
test "out of memory leaves the text unchanged" (sys: &System) {
    var b := TtBudget(left: 0, inner: raw.Of(sys.heap.Alloc()))
    bp := raw.OfMut(mut b)
    var hp := TtBudgetHeap(bp)
    var src := Text.New(sys.arena)
    for _ in 0..2000 {
        try src.Push("ab-c")
    }
    s := src.Done()
    olds := ["-", "ab-c", ""]
    news := ["--==", "", "\u{E9}"]
    for k in 0..3 {
        TtSetBudget(bp, 1)
        var ar := try Arena.New(&hp)
        defer ar.Free()
        TtSetBudget(bp, 0)
        var t := Text.New(&ar)
        try t.Push("keep")
        var failed := false
        t.PushReplaced(s, olds[k], news[k]) else {
            failed = true
        }
        Assert(failed && t.Str() == "keep")
        var also := false
        _ = s.Replace(&ar, olds[k], news[k]) else {
            also = true
        }
        Assert(also)
        TtSetBudget(bp, 1000)
        try t.PushReplaced(s, olds[k], news[k])
        want := try TtNaive(sys.arena, s, olds[k], news[k])
        Assert(t.Str().Len() == 4 + want.Len() && t.Str()[4..] == want)
    }
}

test "Text.Len is the byte length of Str" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var t := Text.New(&ar)
    Assert(t.Len() == 0)
    try t.Push("abc")
    Assert(t.Len() == 3)
    try t.PushChar('\u{E9}')
    try t.PushChar('\u{1F600}')
    Assert(t.Len() == 9 && t.Len() == t.Str().Len())
    t.Drain(3)
    Assert(t.Len() == 6 && t.Str() == "\u{E9}\u{1F600}")
    t.Clear()
    Assert(t.Len() == 0)
}
