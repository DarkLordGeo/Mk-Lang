// ws-extend, Mk: std.core's HeapList[u8].Extend, the append
// std.websocket's Recv makes for each read-ahead's worth of a fragmented
// message, on the heap's allocator. n bytes of splitmix64 output (seed 1); per round r a new, empty growable
// byte list gets the n bytes appended in order, piece j of 1 + (r + j) % piece
// bytes (the last cut at n), then is freed; the checksum adds, per round,
// the byte at (r * 7919) % n and the number of pieces. Usage: ws-extend <n>
// <piece> <rounds>.
export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    piece := Cast[int](try sys.args[2].ParseInt())
    rounds := Cast[int](try sys.args[3].ParseInt())
    data := try sys.arena.Slice[u8](n)
    var st: u64 = 1
    var i := 0
    for i < n {
        st = st +% 0x9E3779B97F4A7C15
        var z := st
        z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
        z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
        z = z ^ (z >> 31)
        var k := 0
        for k < 8 && i < n {
            data[i] = Trunc[u8](z >> Trunc[u64](8 * k))
            k += 1
            i += 1
        }
    }
    a := sys.heap.Alloc()
    var total: u64 = 0
    for r in 0..rounds {
        var list := HeapList[u8].New(a)
        var at := 0
        var j := 0
        for at < n {
            var k := 1 + (r + j) % piece
            if k > n - at {
                k = n - at
            }
            list.Extend(a, data[at..at + k]) else e {
                list.Free(a)
                fail e
            }
            at += k
            j += 1
        }
        total = total +% u64(list[(r * 7919) % n]) +% Cast[u64](j)
        list.Free(a)
    }
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total))
}
