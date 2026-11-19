// wordfreq, Mk keyed, idiomatic std: the mk column's program (../mk) with
// one change, the map: Map.NewKeyed under a key from HashKey.New, made once
// per process (SipHash-1-3 under 16 bytes of Io.Random), the
// DoS-resistant peer of Rust's RandomState. Usage: wordfreq <file>
// <rounds>. Prints the sum over the rounds.

import std.fs

Fnv(w: str): u64 {
    var h: u64 = 0xcbf29ce484222325
    for c in w.AsBytes() {
        h = (h ^ u64(c)) *% 0x100000001b3
    }
    return h
}

Round(h: &Arena, text: str, key: HashKey): OutOfMemory!u64 {
    var a := try h.Sub()
    defer a.Free()
    var m := Map[str, u64].NewKeyed(&a, key)
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

export Main(sys: &System): fs.Error|Utf8Error|Canceled|OutOfMemory|ParseError|WriteError!void {
    b := try fs.ReadBytes(sys.arena, sys.io, sys.args[1])
    text := try str.From(b)
    rounds := Cast[int](try sys.args[2].ParseInt())
    key := try HashKey.New(sys.io)
    var total: u64 = 0
    for _ in 0..rounds {
        total = total +% try Round(sys.arena, text, key)
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
