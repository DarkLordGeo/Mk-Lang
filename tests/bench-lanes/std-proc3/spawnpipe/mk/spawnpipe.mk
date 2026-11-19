// spawnpipe, Mk, std.proc.Spawn: n runs of this program with the argument
// "child", which copies its standard input to its standard output; each
// run gets 64 bytes on a stdin pipe, which is then closed, its stdout read
// to the end from a pipe, stderr the null device, and its exit waited for.
// Prints n, the bytes read back and the failed runs: a run fails when a
// call fails, the child's exit code is not 0, or it echoes other than 64
// bytes. Usage: spawnpipe <n>.

import std.proc

const Payload: str = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n"

// Echo copies standard input to standard output (the child).
Echo(sys: &System): ReadError|WriteError!void {
    var inp := sys.Stdin()
    var out := sys.Stdout()
    var b: [4096]u8 = [0; 4096]
    for {
        n := try inp.Read(mut b[..])
        if n == 0 {
            break
        }
        try out.Bytes(b[0..n])
    }
    try out.Flush()
}

// Once runs the child once: the bytes it echoed, or -1 for a failure.
Once(sys: &System, c: &proc.Command): int {
    io := sys.io
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, c) else {
        return -1
    }
    if r := stderr {
        r.Close()
    }
    w := stdin.Must()
    r := stdout.Must()
    w.WriteAll(io, Payload.AsBytes()) else {}
    w.Close()
    var b: [256]u8 = [0; 256]
    var k := 0
    for k < 256 {
        n := r.Read(io, mut b[k..]) else {
            break
        }
        if n == 0 {
            break
        }
        k += n
    }
    r.Close()
    st := child.Wait(io) else {
        child.Detach()
        return -1
    }
    code := st.code else { -1 }
    return if code == 0 { k } else { -1 }
}

export Main(sys: &System): ParseError|ReadError|WriteError!void {
    a1 := sys.args[1]
    if a1 == "child" {
        try Echo(sys)
        return
    }
    n := Cast[int](try a1.ParseInt())
    var args: [2]str = [sys.args[0], "child"]
    var c := proc.DefaultCommand
    c.args = args[..]
    c.stdin = proc.Stdio.Pipe
    c.stdout = proc.Stdio.Pipe
    c.stderr = proc.Stdio.Null
    var bytes := 0
    var fails := 0
    for _ in 0..n {
        k := Once(sys, &c)
        if k != 64 {
            fails += 1
            continue
        }
        bytes += k
    }
    var out := sys.Stdout()
    try Print(mut out, "{n} {bytes} {fails}\n", (n, bytes, fails))
}
