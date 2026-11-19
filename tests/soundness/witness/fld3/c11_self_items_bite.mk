// verdict: reject E0402 E0503 E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. self.items[i] through &self is a shallow str, so no `*self into self` is owed; the E0604 that remains asks for `*self into a`, since out holds a loan of a. Main frees ar2 while cp holds its loan through d: E0503; E0402 is Main's early return. The method Doc.Copy shares its name with std.core's free function Copy (this witness is only checked, not run)
// before FLD-3: reject E0402 E0503 E0604 at 11 E0604, 20 E0402, 25 E0503 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
struct Doc {
    items: List[str]
}

Doc.Copy(&self, a: &Arena): OutOfMemory!List[str] from a, self {
    var out := List[str].New(a)
    for i in 0..self.items.Len() {
        try out.Push(self.items[i]) // <- E0604
    }
    return out
}

export Main(sys: &System): WriteError!void {
    var o := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    var ar2 := Arena.New(sys.heap) else { return } // <- E0402
    s := ar2.Dup("secret-old") else { return }
    var d := Doc(items: List[str].New(&ar))
    d.items.Push(s) else { return }
    cp := d.Copy(&ar) else { return }
    ar2.Free() // <- E0503
    v := cp[0]
    try Print(mut o, "v={v}\n", (v,))
}
