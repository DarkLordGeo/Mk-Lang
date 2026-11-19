// compress-gunzip, Mk: the one-shot form of the task of
// tests/bench/idiomatic/gzip, with std.compress.Gunzip. The file is read
// once and one arena is made before the loop; each round gunzips the file
// from memory in one call into the arena (Gunzip sizes its output from the
// trailer's ISIZE and checks the CRC-32 and length), adds every
// decompressed byte and the length to the checksum, and resets the arena.
// Reset keeps the arena's ordinary chunks but returns a chunk of over 512
// KiB to the heap, so the 100 MB output is a fresh heap allocation, zero
// filled by Arena.Slice, every round: a cost of Mk's API that the Go and
// Zig columns, which reuse one buffer, do not pay.
// Usage: compress-gunzip <file> <rounds>.

import std.compress
import std.fs

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError|compress.Error!void {
    gz := try fs.ReadBytes(sys.arena, sys.io, sys.args[1])
    rounds := Cast[int](try sys.args[2].ParseInt())
    var total: u64 = 0
    var a := try Arena.New(sys.heap)
    for _ in 0..rounds {
        match compress.Gunzip(&a, gz, 1 << 40) {
            ok(out) {
                for c in out {
                    total = total +% u64(c)
                }
                total = total +% Cast[u64](out.Len())
            }
            err(e) {
                a.Free()
                fail e
            }
        }
        a.Reset()
    }
    a.Free()
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total: total))
}
