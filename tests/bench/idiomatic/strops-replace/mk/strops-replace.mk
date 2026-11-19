// strops-replace, Mk, idiomatic std: the task of ../README.md with std's
// replace-all, str.Replace (non-overlapping, left to right, `new` never
// searched; a new copy in its arena on every call, a text with no match
// included), three in turn on each line of str.Lines. The copies go
// into one scratch Arena.New(sys.heap), made before the rounds and reset
// after each line, as the twins free theirs. The file is read once with
// fs.ReadBytes and checked as UTF-8 by str.From. Usage: strops-replace
// <file> <rounds>. Prints the checksum.

import std.fs

Round(s: mut Arena, text: str): OutOfMemory!u64 {
    var h: u64 = 0xcbf29ce484222325
    var n: u64 = 0
    var lines := text.Lines()
    for line := lines.Next() {
        a := try line.Replace(&*s, "e", "EEE")
        b := try a.Replace(&*s, "s ", "S")
        c := try b.Replace(&*s, " a", "_A")
        for x in c.AsBytes() {
            h = (h ^ Cast[u64](x)) *% 0x100000001b3
        }
        n = n +% Cast[u64](c.Len())
        s.Reset()
    }
    return h +% n
}

export Main(sys: &System): fs.Error|Utf8Error|OutOfMemory|ParseError|WriteError!void {
    b := try fs.ReadBytes(sys.arena, sys.io, sys.args[1])
    text := try str.From(b)
    rounds := Cast[int](try sys.args[2].ParseInt())
    var s := try Arena.New(sys.heap)
    defer s.Free()
    var total: u64 = 0
    for _ in 0..rounds {
        total = total +% try Round(mut s, text)
    }
    try sys.Print("{total}\n", (total))
}
