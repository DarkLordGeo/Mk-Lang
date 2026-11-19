export Make(g: mut Gen) {
    g.Emit(quote {
        struct Made {
            n: int
        }
    })
}

export Look(g: mut Gen, t: Type) {
}
