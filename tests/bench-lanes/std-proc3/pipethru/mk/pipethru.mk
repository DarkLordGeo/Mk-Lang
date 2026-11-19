// pipethru, Mk, std.proc: this program runs itself with "child n", which
// writes n bytes of a splitmix64 stream (each word's 8 bytes, little
// endian, from state 0) to its standard output in 64 KiB writes; the
// parent reads the child's stdout from the pipe Spawn made, with
// PipeReader.Read into a 64 KiB buffer, and prints the bytes read, their
// sum and the child's exit code. stdin and stderr are the null device.
// Usage: pipethru <n>.

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

// Child writes n bytes of the stream to standard output.
Child(sys: &System, n: int): OutOfMemory|WriteError!void {
    b := try sys.arena.Slice[u8](Chunk)
    var out := sys.Stdout()
    _ = Fill(mut b[..], 0)
    var left := n
    for left > 0 {
        k := if left < Chunk { left } else { Chunk }
        try out.Bytes(b[0..k])
        left -= k
    }
    try out.Flush()
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    if sys.args[1] == "child" {
        try Child(sys, Cast[int](try sys.args[2].ParseInt()))
        return
    }
    io := sys.io
    b := try sys.arena.Slice[u8](Chunk)
    var args: [3]str = [sys.args[0], "child", sys.args[1]]
    var c := proc.DefaultCommand
    c.args = args[..]
    c.stdin = proc.Stdio.Null
    c.stdout = proc.Stdio.Pipe
    c.stderr = proc.Stdio.Null
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c) else {
        proc.Exit(3)
    }
    if w := stdin {
        w.Close()
    }
    if e := stderr {
        e.Close()
    }
    r := stdout.Must()
    var total: u64 = 0
    var sum: u64 = 0
    for {
        k := r.Read(io, mut b[..]) else {
            proc.Exit(4)
        }
        if k == 0 {
            break
        }
        total += Cast[u64](k)
        for v in b[0..k] {
            sum = sum +% Cast[u64](v)
        }
    }
    r.Close()
    st := child.Wait(io) else {
        proc.Exit(5)
    }
    code := st.code else { -1 }
    var out := sys.Stdout()
    try Print(mut out, "{total} {sum} {code}\n", (total, sum, code))
}
