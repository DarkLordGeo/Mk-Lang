// arena-reset, Mk, idiomatic std: the task of ../README.md with std's
// Arena: one Arena.New(sys.heap) made before the rounds; per round n nodes
// put into it one at a time with Arena.Put, each linked to the one before
// by a reference, the list walked from its head, then Arena.Reset, which
// keeps some chunks for the next round and returns the rest to the heap
// (std's policy, quoted in ../README.md). Usage: arena-reset <n> <rounds>
// <seed>. Prints the checksum.
//

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

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    var a := try Arena.New(sys.heap)
    defer a.Free()
    var total: u64 = 0
    for _ in 0..rounds {
        head := try Build(&a, n, mut st)
        total = total +% Walk(head)
        a.Reset()
    }
    try sys.Print("{total}\n", (total))
}
