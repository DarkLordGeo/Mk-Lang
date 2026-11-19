// csv-write, Mk: write R records of 8 fields (generated once with
// splitmix64 from seed 42: each field 0 to 11 bytes over a 40-byte
// alphabet of letters, digits, space, comma, quote and LF, so about a
// third of the fields need quotes) with csv.WriteRecord into one Text
// through its Writer, cleared before each of reps reps; then read the last
// output back with csv.Records (through a file) and print the records, the fields, the
// field bytes and the sum of the field bytes (values, not output
// bytes). Usage: csv-write <records> <reps>.

import std.csv
import std.fs

// Rng is splitmix64.
struct Rng {
    s: u64
}

Rng.Next(mut self): u64 {
    self.s = self.s +% 0x9E3779B97F4A7C15
    var z := self.s
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

export Main(sys: &System): OutOfMemory|ParseError|Utf8Error|WriteError|fs.Error|csv.Error!void {
    records := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    alpha := "abcdefghijklmnopqrstuvwxyz0123456789 ,\"\n".AsBytes()
    var rng := Rng(s: 42)
    var data := List[u8].New(sys.arena)
    var ends := List[int].New(sys.arena)
    for _ in 0..records * 8 {
        n := rng.Next() % 12
        for _ in 0..n {
            try data.Push(alpha[Cast[int](rng.Next() % 40)])
        }
        try ends.Push(data.Len())
    }
    // Done gives the bytes and the ends as slices in the arena, so the
    // fields made from them may live in the arena's list too (Items
    // borrows the local list, which the arena outlives: E0505).
    bs := data.Done()
    es := ends.Done()
    var vals := List[str].New(sys.arena)
    var start := 0
    for k in 0..es.Len() {
        try vals.Push(try str.From(bs[start..es[k]]))
        start = es[k]
    }
    vs := vals.Items()
    o := csv.DefaultOptions
    var t := Text.New(sys.arena)
    for _ in 0..reps {
        t.Clear()
        var w := t.Writer()
        for r in 0..records {
            try csv.WriteRecord(mut w, vs[r * 8..r * 8 + 8], &o)
        }
    }
    // The read-back goes through a file, as Go's does: std has no Reader
    // over bytes in memory outside std.core.
    try fs.WriteAll(sys.io, "csv-write-mk.tmp", t.Str().AsBytes())
    var file := try fs.Open(sys.io, "csv-write-mk.tmp")
    onerr file.Abandon()
    rb := try sys.arena.Slice[u8](0)
    var rd := file.Reader(sys.io, mut rb[..])
    buf := try sys.arena.Slice[u8](1 << 16)
    var rs := csv.Records.New(mut rd, mut buf[..], &o)
    var nr := 0
    var nf := 0
    var nb := 0
    var sum := 0
    for rec := try rs.Next() {
        nr += 1
        for i in 0..rec.Len() {
            f := rec.Field(i) else { "" }
            nf += 1
            nb += f.Len()
            for c in f.AsBytes() {
                sum += Cast[int](c)
            }
        }
    }
    try file.Close(sys.io)
    try fs.Remove(sys.io, "csv-write-mk.tmp")
    try sys.Print("{nr} {nf} {nb} {sum}\n", (nr, nf, nb, sum))
}
