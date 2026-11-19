// Why: a cluster is exercised by clients that retry when a leader fails.
// What: Client, which sends register operations and records their results.
// How: a client that hears nothing for RetryAfter asks the next node.

import std.coll
import std.math
import src.history
import src.raft

// A client that heard nothing for this long asks the next node.
const RetryAfter: i64 = 200_000_000

// After an answer a client waits up to this long before its next operation.
const ThinkTime: u64 = 50_000_000

/// A client of the register store: it invokes one operation at a time,
/// records each in the history when it invokes it and again when it is
/// answered, and retries with the same sequence number until it is.
export struct Client {
    id: int
    nodes: int
    keys: int
    left: int
    seq: i64
    busy: bool
    // the history entry of the operation in flight
    slot: int
    cmd: raft.Command
    target: int
    // when to invoke the next operation or, while busy, to retry
    due: i64
    rng: math.Rng
    // the last value this client saw in each register, for its next Cas
    seen: [raft.MaxKeys]i64
}

/// A client that invokes ops operations on keys registers. O(1).
export Client.New(id: int, nodes: int, keys: int, ops: int, seed: u64): Client {
    return Client(
        id: id,
        nodes: nodes,
        keys: keys,
        left: ops,
        seq: 0,
        busy: false,
        slot: -1,
        cmd: raft.NoCommand,
        target: 0,
        due: 0,
        rng: math.Rng.New(seed),
        seen: [0; raft.MaxKeys],
    )
}

/// Whether every operation was invoked and answered.
export Client.Finished(&self): bool {
    return self.left == 0 && !self.busy
}

Client.Endpoint(&self): int {
    return self.nodes + self.id
}

/// Invokes the next operation when it is due, or retries the one in flight
/// at the next node when it waited too long. hint is the node the client
/// believes leads, or -1. Fails with OutOfMemory when the history or the
/// outbox cannot grow. O(1).
export Client.Tick(mut self, a: &Alloc, out: mut coll.HeapDeque[raft.Msg], h: mut history.History, now: i64, hint: int): OutOfMemory!void {
    if now < self.due {
        return
    }
    if self.busy {
        self.target = (self.target + 1) % self.nodes
        try self.Send(a, out, now)
        return
    }
    if self.left == 0 {
        return
    }
    self.seq += 1
    key := Cast[int](self.rng.Below(Cast[u64](self.keys)))
    unique := i64(self.id + 1) * 1_000_000 + self.seq
    var op := history.Op(client: self.id, call: now, ret: -1, kind: history.Kind.Get, key: key, arg: 0, arg2: 0, value: 0, ok: false)
    var code := raft.Op.Get
    match self.rng.Below(3) {
        0 {
        }
        1 {
            code = raft.Op.Put
            op.kind = history.Kind.Put
            op.arg = unique
        }
        _ {
            code = raft.Op.Cas
            op.kind = history.Kind.Cas
            op.arg = self.seen[key]
            op.arg2 = unique
        }
    }
    self.cmd = raft.Command(client: Trunc[i32](self.id), seq: self.seq, op: code, key: Trunc[u8](key), arg: op.arg, arg2: op.arg2)
    self.slot = h.ops.Len()
    try h.ops.Push(op)
    self.busy = true
    self.target = if hint >= 0 { hint } else { Cast[int](self.rng.Below(Cast[u64](self.nodes))) }
    try self.Send(a, out, now)
}

Client.Send(mut self, a: &Alloc, out: mut coll.HeapDeque[raft.Msg], now: i64): OutOfMemory!void {
    var m := raft.Msg.New(raft.Kind.Request, self.Endpoint(), self.target, 0)
    m.cmd = self.cmd
    try out.Push(a, m)
    self.due = now + RetryAfter
}

/// Takes a reply: the answer completes the operation in flight, and a
/// refusal sends it again at once to the node the refusal names. Fails with
/// OutOfMemory when the outbox cannot grow. O(1).
export Client.Handle(mut self, a: &Alloc, out: mut coll.HeapDeque[raft.Msg], h: mut history.History, m: &raft.Msg, now: i64): OutOfMemory!void {
    if !self.busy || m.kind != raft.Kind.Reply || m.cmd.seq != self.seq {
        return
    }
    if !m.served {
        self.target = if m.leader >= 0 && int(m.leader) < self.nodes { int(m.leader) } else { (self.target + 1) % self.nodes }
        try self.Send(a, out, now)
        return
    }
    var op := h.ops.GetMut(self.slot) else {
        return
    }
    op.ret = now
    op.value = m.value
    op.ok = m.ok
    k := int(self.cmd.key)
    self.seen[k] = match self.cmd.op {
        .Put { self.cmd.arg }
        .Cas {
            if m.ok { self.cmd.arg2 } else { m.value }
        }
        _ { m.value }
    }
    self.busy = false
    self.left -= 1
    self.due = now + Cast[i64](self.rng.Below(ThinkTime))
}
