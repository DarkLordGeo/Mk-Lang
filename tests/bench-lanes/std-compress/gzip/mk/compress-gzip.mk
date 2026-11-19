// compress-gzip, Mk: std.compress.Gzip (its one level, Go 1.27's level 6
// matcher) of a 50 MB log text, rounds times, into one arena made before
// the loop and reset after each round (SC-2: every column reuses what it
// can). Reset returns a chunk of over 512 KiB to the heap, so Gzip's
// output (one allocation of the stored-form bound, zero filled by
// Arena.Slice) is a fresh heap allocation every round, and its scratch is
// a sub-arena it frees: costs of Mk's API. The checksum adds every input
// byte and the input length once per round, so it does not depend on the
// compressor; the compressed size goes to stderr.
// Usage: compress-gzip <file> <rounds>.

import std.compress
import std.fs

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError!void {
    text := try fs.ReadBytes(sys.arena, sys.io, sys.args[1])
    rounds := Cast[int](try sys.args[2].ParseInt())
    var total: u64 = 0
    var size := 0
    var a := try Arena.New(sys.heap)
    for _ in 0..rounds {
        match compress.Gzip(&a, text) {
            ok(out) {
                size = out.Len()
            }
            err(e) {
                a.Free()
                fail e
            }
        }
        a.Reset()
        for c in text {
            total = total +% u64(c)
        }
        total = total +% Cast[u64](text.Len())
    }
    a.Free()
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total: total))
    var ew := sys.Stderr()
    try Print(mut ew, "compressed {size}\n", (size: size))
}
