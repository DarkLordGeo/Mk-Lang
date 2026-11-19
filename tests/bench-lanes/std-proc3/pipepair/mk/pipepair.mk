// pipepair, Mk, std.proc.Pipe: one process, one pipe; a task writes n
// bytes of a splitmix64 stream (each word's 8 bytes, little endian, from
// state 0) to the write end with PipeWriter.WriteAll, 64 KiB at a time,
// then closes it; the main task reads the read end with PipeReader.Read
// into a 64 KiB buffer and prints the bytes read and their sum. Both ends
// are proc.Pipe's (blocking-pool ends). Usage: pipepair <n>.

import std.proc

const Chunk: int = 65536

// Fill writes the splitmix64 words after state x into b, whose length is
// a multiple of 8: the last state.
Fill(b: mut []u8, x0: u64): u64 {
    var x := x0
    var i := 0
    for i < b.Len() {
        x = x +% 0x9e3779b97f4a7c15
        var z := x
        z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
        z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
        z = z ^ (z >> 31)
        for k in 0..8 {
            b[i + k] = Trunc[u8](z >> Cast[u64](8 * k))
        }
        i += 8
    }
    return x
}

// Pump writes n bytes of the stream to w through b, then closes w.
Pump(io: Io, w: proc.PipeWriter, b: mut []u8, n: int) {
    _ = Fill(b, 0)
    var left := n
    for left > 0 {
        k := if left < Chunk { left } else { Chunk }
        w.WriteAll(io, b[0..k]) else {
            proc.Exit(4)
        }
        left -= k
    }
    w.Close()
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    io := sys.io
    wb := try sys.arena.Slice[u8](Chunk)
    b := try sys.arena.Slice[u8](Chunk)
    (r, w) := proc.Pipe(io) else {
        proc.Exit(3)
    }
    var total: u64 = 0
    var sum: u64 = 0
    scope g := io.Scope() {
        g.go Pump(io, w, mut wb[..], n)
        for {
            k := r.Read(io, mut b[..]) else {
                proc.Exit(5)
            }
            if k == 0 {
                break
            }
            total += Cast[u64](k)
            for v in b[0..k] {
                sum = sum +% Cast[u64](v)
            }
        }
    }
    r.Close()
    var out := sys.Stdout()
    try Print(mut out, "{total} {sum}\n", (total, sum))
}
