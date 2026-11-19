// The first program of doc/reference.md, and the "hello world" of the binary-size budget: built --release on both targets.
// sys.args holds the program's own name, so with no arguments it prints "1 args".

export Main(sys: &System): WriteError!void {
    try sys.Print("hello {name}, {n} args\n", (name: "world", n: sys.args.Len()))
}
