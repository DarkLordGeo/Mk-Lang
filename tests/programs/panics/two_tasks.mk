// Source: two tasks panic at once (RT-Panic-FirstOnly): exactly one report is printed. Both
// tasks panic at the same site with the same message, so the report's bytes do not depend on
// which task wins.
Boom(n: int) {
    if n > 0 {
        Panic("boom")
    }
}

export Main(sys: &System) {
    scope s := sys.io.Scope() {
        s.go Boom(sys.args.Len())
        s.go Boom(sys.args.Len())
    }
}
