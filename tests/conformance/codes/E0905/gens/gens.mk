export Broken(g: mut Gen, t: Type) {
    g.Emit(quote {
        $(t.Name()).Zero(): i32 {
            return "zero"
        }
    })
}
