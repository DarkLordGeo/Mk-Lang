// json-parse, Mk: std.json.Parse of a 1 MB document (tools/bench gen kind
// json), reps times into one arena that is Reset after each parse, and a
// walk of each tree. Usage: json-parse <file> <reps>. Prints the checksum:
// the sum over reps of the tree's value count plus the decoded bytes of its
// strings and keys (README.md).

import std.fs
import std.json

Walk(v: &json.Value): u64 {
    var n: u64 = 1
    match v {
        .Text(s) {
            n += Cast[u64](s.Len())
        }
        .Array(items) {
            xs := items.Items()
            for k in 0..xs.Len() {
                n += Walk(&xs[k])
            }
        }
        .Object(members) {
            ms := members.Items()
            for k in 0..ms.Len() {
                n += Cast[u64](ms[k].key.Len()) + Walk(&ms[k].value)
            }
        }
        _ {}
    }
    return n
}

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError|Utf8Error|json.Error!void {
    // The bytes are read once; each rep turns them into a str (one UTF-8
    // pass, str.From), since Parse takes a str and the Go and Zig decoders
    // check UTF-8 on every parse too.
    bytes := try fs.ReadBytes(sys.arena, sys.io, sys.args[1])
    reps := Cast[int](try sys.args[2].ParseInt())
    var a := try Arena.New(sys.heap)
    var total: u64 = 0
    for _ in 0..reps {
        text := str.From(bytes) else {
            a.Free()
            fail Utf8Error
        }
        match json.Parse(&a, text) {
            ok(v) {
                total = total +% Walk(&v)
            }
            err(e) {
                a.Free()
                fail e
            }
        }
        a.Reset()
    }
    a.Free()
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total: total))
}
