// verdict: reject E0402 E0503 E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. Map.Set's key is words[i], a shallow str, so the walk does not go on through it; the E0604 that remains asks for `*words into a`, since m holds a loan of a. Main frees ar2 while m holds its loan through src: E0503; E0402 is Main's early return
// before FLD-3: reject E0402 E0503 E0604 at 7 E0604, 16 E0402, 21 E0503 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Index(a: &Arena, words: &List[str]): OutOfMemory!Map[str, int] from a, words {
    var m := Map[str, int].New(a)
    for i in 0..words.Len() {
        try m.Set(words[i], i) // <- E0604
    }
    return m
}

export Main(sys: &System): WriteError!void {
    var o := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    var ar2 := Arena.New(sys.heap) else { return } // <- E0402
    s := ar2.Dup("secret-old") else { return }
    var src := List[str].New(&ar)
    src.Push(s) else { return }
    m := Index(&ar, &src) else { return }
    ar2.Free() // <- E0503
    n := m.Len()
    try Print(mut o, "n={n}\n", (n,))
}
