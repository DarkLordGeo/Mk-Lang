// bufwrite, Mk, idiomatic std: the task of ../README.md with std's
// formatter and buffered file writer: fs.Create in fs.TempDir, File.Writer
// over a 64 KiB array, Print of each line ("{u} {h:x} {k}\n"), Flush and
// Close; then fs.ReadBytes of the file, FNV-1a over its bytes, and
// fs.Remove. Usage: bufwrite <n>. Prints the checksum.

import std.fs

Fnv(b: []u8): u64 {
    var h: u64 = 0xcbf29ce484222325
    for c in b {
        h = (h ^ u64(c)) *% 0x100000001b3
    }
    return h
}

Write(io: Io, p: str, n: int): fs.Error|WriteError!void {
    var f := try fs.Create(io, p)
    onerr f.Abandon()
    var buf: [65536]u8 = [0; 65536]
    var w := f.Writer(io, mut buf[..])
    for i in 0..n {
        u := Cast[u64](i)
        h := u *% 0x9e3779b97f4a7c15
        try Print(mut w, "{u} {h:x} {k}\n", (u, h, k: u * 7))
    }
    try w.Flush()
    try f.Close(io)
}

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    tmp := try fs.TempDir(sys.arena, sys.io)
    p := try sys.arena.Fmt("{tmp}/mkbench-bufwrite-mk.txt", (tmp))
    try Write(sys.io, p, n)
    b := try fs.ReadBytes(sys.arena, sys.io, p)
    total := Cast[u64](b.Len()) +% Fnv(b)
    try fs.Remove(sys.io, p)
    try sys.Print("{total}\n", (total))
}
