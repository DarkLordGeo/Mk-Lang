// verdict: ok
// note: the precision control of B's keep of the arena's loan: the arena-reset benchmark's sequence (tests/bench/idiomatic/arena-reset), Build putting linked nodes into a reused arena, Walk over them, then Arena.Reset, round after round. Nothing read back out of the arena outlives the Reset, so the keep refuses nothing here; P5-K4-p8readback, which keeps a node past the Reset, is the hole beside it. A rejection means the keep or a later rule over-refuses the benchmark
Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

struct Node {
    val: u64
    next: ?&Node
}

Build(a: &Arena, n: int, st: mut u64): OutOfMemory!?&Node from a {
    var head: ?&Node = none
    for _ in 0..n {
        *st = *st +% 0x9e3779b97f4a7c15
        node := try a.Put(Node(val: Mix(*st), next: head))
        head = some(&*node)
    }
    return head
}

Walk(head: ?&Node): u64 {
    var sum: u64 = 0
    var i: u64 = 0
    var p := head
    for node := p {
        i = i +% 1
        sum = sum +% node.val *% i
        p = node.next
    }
    return sum +% i
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var st: u64 = 1
    var a := try Arena.New(sys.heap)
    defer a.Free()
    var total: u64 = 0
    for _ in 0..3 {
        head := try Build(&a, 4, mut st)
        total = total +% Walk(head)
        a.Reset()
    }
    try sys.Print("{total}\n", (total))
}
