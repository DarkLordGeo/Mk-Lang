// PGO program (internal/corpus's TestPGOChangesWeights): the `else` branch runs 999 times in 1000, so a profile gives the `if` skewed branch weights.
export Main(sys: &System): WriteError!void {
    var hot := 0
    var cold := 0
    for i in 0..1000000 {
        if i % 1000 == 999 {
            cold += 1
        } else {
            hot += 1
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{h} {c}\n", (h: hot, c: cold))
}
