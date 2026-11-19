// strops-find, Mk, idiomatic std: the task of ../README.md with std's
// substring search, str.Find on line[p..] (the byte offset of the first
// match), over the lines of str.Lines. The file is read once with
// fs.ReadBytes and checked as UTF-8 by str.From. Usage: strops-find <file>
// <rounds>. Prints the checksum.

import std.fs

const Needles: [8]str = ["the", "ing", "qzx", "e a", "tion", "s an d", "abcdefgh", "xylophone quartz"]

Round(text: str): u64 {
    var total: u64 = 0
    var lines := text.Lines()
    for line := lines.Next() {
        for nd in Needles {
            var p := 0
            for q := line[p..].Find(nd) {
                at := p + q
                total = total +% Cast[u64](at + 1)
                p = at + nd.Len()
            }
        }
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
