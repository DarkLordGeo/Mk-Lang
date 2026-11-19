// Why: elections and replication must settle on small fixed clusters.
// What: tests of Node without a network.
// How: Settle passes messages between nodes until none is left.

import std.coll

const Timing: Params = Params(election_min: 150, election_spread: 150, heartbeat: 50, stale_reads: false)

// Delivers every message in out to its node, in order, until the network is
// quiet; messages to clients are kept in replies.
Settle(a: &Alloc, nodes: mut []Node, out: mut coll.HeapDeque[Msg], replies: mut coll.HeapDeque[Msg], now: i64): OutOfMemory!void {
    for m := out.PopFront() {
        to := int(m.to)
        if to >= nodes.Len() {
            try replies.Push(a, m)
            continue
        }
        try nodes[to].Handle(a, out, &m, now)
    }
}

test "the first node to time out becomes the only leader of its term" (sys: &System) {
    a := sys.heap.Alloc()
    var out := coll.HeapDeque[Msg].New(a)
    defer out.Free(a)
    var replies := coll.HeapDeque[Msg].New(a)
    defer replies.Free(a)
    var nodes := List[Node].New(sys.arena)
    for i in 0..3 {
        try nodes.Push(try Node.New(sys.arena, i, 3, Timing, Cast[u64](i + 1), 0))
    }
    try nodes[1].Tick(a, mut out, 1000)
    try Settle(a, nodes.ItemsMut(), mut out, mut replies, 1000)
    var leaders := 0
    for n in &nodes {
        if n.CurrentRole() == Role.Leader {
            leaders += 1
            Assert(n.Number() == 1 && n.CurrentTerm() == 1)
        }
    }
    Assert(leaders == 1)
}

test "a committed command is answered once, and a retry gets the same answer" (sys: &System) {
    a := sys.heap.Alloc()
    var out := coll.HeapDeque[Msg].New(a)
    defer out.Free(a)
    var replies := coll.HeapDeque[Msg].New(a)
    defer replies.Free(a)
    var nodes := List[Node].New(sys.arena)
    for i in 0..3 {
        try nodes.Push(try Node.New(sys.arena, i, 3, Timing, Cast[u64](i + 1), 0))
    }
    try nodes[0].Tick(a, mut out, 1000)
    try Settle(a, nodes.ItemsMut(), mut out, mut replies, 1000)
    var req := Msg.New(Kind.Request, 3, 0, 0)
    req.cmd = Command(client: 0, seq: 1, op: Op.Put, key: 2, arg: 77, arg2: 0)
    try nodes[0].Handle(a, mut out, &req, 1001)
    try Settle(a, nodes.ItemsMut(), mut out, mut replies, 1001)
    try nodes[0].Handle(a, mut out, &req, 1002)
    try Settle(a, nodes.ItemsMut(), mut out, mut replies, 1002)
    Assert(replies.Len() == 2)
    first := replies.PopFront().Must()
    second := replies.PopFront().Must()
    Assert(first.served && second.served && first.cmd.seq == 1 && second.value == first.value)
    Assert(nodes[0].CommitIndex() == 2)
}

test "a follower refuses a request and names the leader" (sys: &System) {
    a := sys.heap.Alloc()
    var out := coll.HeapDeque[Msg].New(a)
    defer out.Free(a)
    var replies := coll.HeapDeque[Msg].New(a)
    defer replies.Free(a)
    var nodes := List[Node].New(sys.arena)
    for i in 0..3 {
        try nodes.Push(try Node.New(sys.arena, i, 3, Timing, Cast[u64](i + 1), 0))
    }
    try nodes[2].Tick(a, mut out, 1000)
    try Settle(a, nodes.ItemsMut(), mut out, mut replies, 1000)
    var req := Msg.New(Kind.Request, 4, 0, 0)
    req.cmd = Command(client: 1, seq: 1, op: Op.Get, key: 0, arg: 0, arg2: 0)
    try nodes[0].Handle(a, mut out, &req, 1001)
    try Settle(a, nodes.ItemsMut(), mut out, mut replies, 1001)
    r := replies.PopFront().Must()
    Assert(!r.served && r.leader == 2 && int(r.to) == 4)
}
