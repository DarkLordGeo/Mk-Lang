// verdict: reject E0501 E0604
// note: the row allows E0501 or E0502; this is the E0501 form. A result read from a `List` may point into its memory, so the list stays borrowed while `h` lives (R10). The E0604s come from the arena: the List's storage lives in it, so each Push stores v into the arena a
Head(list: &List[&i32]): &i32 {
    return list[0]
}

Caller(a: &Arena, v: &i32): OutOfMemory!void {
    var list := List[&i32].New(a)
    try list.Push(v) // <- E0604
    h := Head(&list)
    try list.Push(v) // <- E0501 E0604
    Use(h)
}
