// task-pingpong, Mk: two tasks of one scope pass a value back
// and forth over two rendezvous channels (Chan capacity 0). Each of n
// rounds: the main task sends i on ping, the partner receives it and sends
// i * 3 + 1 on pong, the main task receives it; sum = 31 sum + the answer
// (wrapping). Every Send and Recv that waits parks its task (std.task's
// WaitWord through Io.wait). Prints n, sum and the failed calls. Usage:
// taskpingpong <n>.

Partner(io: Io, ping: &Chan[u64], pong: &Chan[u64], n: int) {
    for _ in 0..n {
        v := ping.Recv(io) else {
            return
        }
        pong.Send(io, v * 3 + 1) else {
            return
        }
    }
}

export Main(sys: &System): ParseError|WriteError|OutOfMemory!void {
    n := Cast[int](try sys.args[1].ParseInt())
    a := sys.heap.Alloc()
    ping := try Chan[u64].New(a, 0)
    pong := Chan[u64].New(a, 0) else e {
        ping.Free(a)
        fail e
    }
    var sum: u64 = 0
    var fails := 0
    scope s := sys.io.Scope() {
        s.go Partner(sys.io, &ping, &pong, n)
        for i in 0..n {
            ping.Send(sys.io, Cast[u64](i)) else {
                fails += 1
                break
            }
            r := pong.Recv(sys.io) else {
                fails += 1
                break
            }
            sum = sum *% 31 +% r
        }
    }
    ping.Free(a)
    pong.Free(a)
    var out := sys.Stdout()
    try Print(mut out, "{n} {sum} {fails}\n", (n, sum, fails))
}
