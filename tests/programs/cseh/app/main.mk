import ext

// With no argument, prints the C source's cases 0 to 5 on one line: no
// fault, a faulting memcpy of 4096 bytes, a faulting memset of 4096 bytes,
// a faulting memcpy of 1024 bytes, each handled by the C source's own
// __except with its live values intact, then the count of wrong bytes of
// the equal-range and memset value checks, then a faulting memcmp handled
// the same way.
// Want "16775 1016775 1016775 1016775 0 1016775" and exit 0. Cases 2 and 5
// check the unwind entries: on Win64 the support pack's memset saves rsi
// and rdi and its memcmp rsi, and without their unwind entries the __except
// would resume with the wrong values. With an argument 0
// to 5, runs only that case.
export Main(sys: &System): WriteError!void {
    if sys.args.Len() > 1 {
        w := i32(sys.args[1].AsBytes()[0]) - 48
        r := ext.Run(w)
        try sys.Print("case {w}: {r}\n", (w: w, r: r))
        return
    }
    a := ext.Run(0)
    b := ext.Run(1)
    s := ext.Run(2)
    c := ext.Run(3)
    d := ext.Run(4)
    m := ext.Run(5)
    try sys.Print("{a} {b} {s} {c} {d} {m}\n", (a: a, b: b, s: s, c: c, d: d, m: m))
}
