// compress-adler32, Mk: std.compress.Adler32 of a 50 MB log text, rounds
// times, round r over the text from byte r % 64 on, so no round repeats
// another's work and no cell writes to its buffer (CMP2-8: fs.ReadBytes
// gives a read-only slice, and a mutating round would have charged Mk
// alone a 50 MB copy). The checksum adds the rounds' values.
// Usage: compress-adler32 <file> <rounds>.

import std.compress
import std.fs

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError!void {
    text := try fs.ReadBytes(sys.arena, sys.io, sys.args[1])
    rounds := Cast[int](try sys.args[2].ParseInt())
    var total: u64 = 0
    for r in 0..rounds {
        total = total +% u64(compress.Adler32(text[r % 64..text.Len()]))
    }
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total: total))
}
