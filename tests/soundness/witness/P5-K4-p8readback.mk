// verdict: reject E0501
// note: the arena-reset benchmark's Build, which checks clean because a store into an arena parameter's own memory drops the arena's own loans. A node's next, read back out of the arena, is kept past a.Reset(); canary nodes are put into the reset arena and the kept reference is walked. B keeps the arena's loan, so the Reset conflicts with the shared loan second holds: E0501, the code a `mut self` call gives. If accepted, `mk run` prints `after 23130`, the canaries' sum (a dead read). Mutant M-D791-keep accepts it
// The arena-reset benchmark's Build, which
// checks clean because a store into an arena parameter's own
// memory drops the arena's own loans. A node's `next`, read back out of the
// arena, is kept past a.Reset(); new nodes with a canary value are then put
// into the reset arena, and the kept reference is walked. Expected: refused
// at a.Reset() (second views a's memory). If accepted, the walk prints the
// canary's sum instead of the first build's values: a dead read (the class
// of P5-K4-w7put, which B's keep of the arena's loan closes).

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

Canary(a: &Arena, n: int): OutOfMemory!void {
    for _ in 0..n {
        _ = try a.Put(Node(val: 0x5A5A, next: none))
    }
}

Walk(head: ?&Node): u64 {
    var sum: u64 = 0
    var p := head
    for node := p {
        sum = sum +% node.val
        p = node.next
    }
    return sum
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    var st: u64 = 1
    var a := try Arena.New(sys.heap)
    defer a.Free()
    head := try Build(&a, 4, mut st)
    before := Walk(head)
    second := head.Must().next
    a.Reset() // <- E0501
    try Canary(&a, 4)
    after := Walk(second)
    try sys.Print("before {before} after {after}\n", (before, after))
}
