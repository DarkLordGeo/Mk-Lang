// Source: Panic with a long message (RT-F11-LongPanicMessage): 2,000 bytes with no arguments,
// 100 bytes (one write call) with one argument. The report is "panic: " + the message + the site.
export Main(sys: &System): OutOfMemory!void {
    var n := 2000
    if sys.args.Len() > 1 {
        n = 100
    }
    var t := Text.New(sys.arena)
    var k := 0
    for k < n {
        try t.Push("a")
        k += 1
    }
    Panic(t.Str())
}
