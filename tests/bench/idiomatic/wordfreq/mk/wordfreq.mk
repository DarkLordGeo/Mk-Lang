// wordfreq, Mk, idiomatic std: the task of ../README.md. The file is read
// once with fs.ReadBytes and checked as UTF-8 by str.From; each round
// splits it with str.Words (std's whitespace iterator) and counts every
// word in a Map[str, u64] made by Map.New (the fast fixed hash; no DoS
// resistance, the key is public) with Ensure, the keys views of the text,
// in a sub-arena the round frees. The map grows from empty. Then the
// checksum over the entries: sum of FNV-1a 64 of the word times its count,
// plus the number of distinct words, all wrapping. Usage: wordfreq <file>
// <rounds>. Prints the sum over the rounds.

import std.fs

Fnv(w: str): u64 {
    var h: u64 = 0xcbf29ce484222325
    for c in w.AsBytes() {
        h = (h ^ u64(c)) *% 0x100000001b3
    }
    return h
}

Round(h: &Arena, text: str): OutOfMemory!u64 {
    var a := try h.Sub()
    defer a.Free()
    var m := Map[str, u64].New(&a)
    var words := text.Words()
    for w := words.Next() {
        n := try m.Ensure(w, 0)
        *n += 1
    }
    var sum: u64 = 0
    var it := m.Iter()
    for e := it.Next() {
        sum = sum +% Fnv(*e.key) *% *e.value
    }
    return sum +% Trunc[u64](m.Len())
}

export Main(sys: &System): fs.Error|Utf8Error|OutOfMemory|ParseError|WriteError!void {
    b := try fs.ReadBytes(sys.arena, sys.io, sys.args[1])
    text := try str.From(b)
    rounds := Cast[int](try sys.args[2].ParseInt())
    var total: u64 = 0
    for _ in 0..rounds {
        total = total +% try Round(sys.arena, text)
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
