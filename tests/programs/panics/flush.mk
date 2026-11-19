// Source: a Panic after buffered output (RT-Stdout-FlushOnPanic): the bytes written to the
// buffered Stdout before the panic reach standard output before the report reaches standard error.
export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try Print(mut out, "before the panic\n")
    if sys.args.Len() > 0 {
        Panic("after output")
    }
}
