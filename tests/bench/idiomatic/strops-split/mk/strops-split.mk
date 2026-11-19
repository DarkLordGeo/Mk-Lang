// strops-split, Mk, idiomatic std: the task of ../README.md with std's
// string iterators: str.Split("e") (empty pieces kept), str.Trim on each
// piece, and str.Words over the text. The file is read once with
// fs.ReadBytes and checked as UTF-8 by str.From. Usage: strops-split
// <file> <rounds>. Prints the checksum.

import std.fs

Round(text: str): u64 {
    var total: u64 = 0
    var h: u64 = 0xcbf29ce484222325
    var pieces := text.Split("e")
    for piece := pieces.Next() {
        t := piece.Trim()
        total = total +% 1
        h = (h ^ Cast[u64](t.Len())) *% 0x100000001b3
    }
    total = total +% h
    var words := text.Words()
    for w := words.Next() {
        total = total +% Cast[u64](w.Len() + 1)
    }
    return total
}

export Main(sys: &System): fs.Error|Utf8Error|OutOfMemory|ParseError|WriteError!void {
    b := try fs.ReadBytes(sys.arena, sys.io, sys.args[1])
    text := try str.From(b)
    rounds := Cast[int](try sys.args[2].ParseInt())
    var total: u64 = 0
    for _ in 0..rounds {
        total = total +% Round(text)
    }
    try sys.Print("{total}\n", (total))
}
