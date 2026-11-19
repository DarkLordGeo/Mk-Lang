// json-write, Mk: std.json.Parse of a 1 MB document (tools/bench gen kind
// json) once, then Value's Format of the tree as compact JSON into one Text
// through its Writer, reps times, the Text cleared before each. Usage:
// json-write <file> <reps>. Prints the checksum: the sum over reps of the
// output's byte length (README.md).

import std.fs
import std.json

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError|json.Error!void {
    text := try fs.ReadText(sys.arena, sys.io, sys.args[1])
    reps := Cast[int](try sys.args[2].ParseInt())
    v := try json.Parse(sys.arena, text)
    var t := Text.New(sys.arena)
    var total: u64 = 0
    for _ in 0..reps {
        t.Clear()
        var w := t.Writer()
        try v.Format(mut w)
        total = total +% Cast[u64](t.Str().Len())
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total: total))
}
