// verdict: ok
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. A callee alone: c into a, a: str into b, b: i32 (by value, holds nothing) into l. Clean with or without shallow loans: the marks change no callee verdict here, and there is no caller
// before FLD-3: ok (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
F(l: mut List[&Cell[str]], c: &Cell[str] into a, a: str into b, b: i32 into l): OutOfMemory!void {
    try l.Push(c)
}

export Main(sys: &System) {
}
