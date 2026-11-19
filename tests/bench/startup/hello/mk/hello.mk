// hello, Mk: startup only. Writes "1\n" to standard output with one
// Writer.Str call (a process stream has no buffer, so it is one write) and
// exits.

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try out.Str("1\n")
}
