// Why: a program that never uses sys.io has no task, and Main runs on carrier 0's system stack
// with no slot header; its error-return trace must still be printed (doc/reference.md: "Dev
// builds record try sites as an error return trace").
// What: Main fails Bad through Outer and Inner.
// How: a dev build prints the `fail` (11:9), then the `try` in Outer (17:12) and in Main
// (21:9); release prints the error line only (tracenotask.cases).
error Bad

Inner(n: int): Bad!int {
    if n > 0 {
        fail Bad
    }
    return n
}

Outer(n: int): Bad!int {
    return try Inner(n)
}

export Main(sys: &System): Bad!void {
    _ = try Outer(sys.args.Len())
}
