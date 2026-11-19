// Why: Raft's safety must hold over a network that drops, delays,
// duplicates and corrupts datagrams.
// What: Network, every endpoint's UDP socket and what happens to a datagram.
// How: partitions, loss, delay, duplication and corruption are rolled from
// the seed; faults come from the simulator. A per-endpoint count of sends
// keeps a receive from waiting for a datagram that is not coming.

import std.coll
import std.math
import std.net
import std.sim
import src.raft
import src.wire

// Every endpoint's socket, and what the network does to a datagram on the
// way: a partition drops it, loss drops it, delay holds it for a step,
// duplication sends it twice, corruption changes a byte, and a fault fails
// the send or the receive through the simulator. The network counts what it
// sent each endpoint, so a receive never waits for a datagram that is not
// coming.
linear struct Network {
    socks: HeapList[net.Datagram]
    // messages the network holds back until the next step
    held: coll.HeapDeque[raft.Msg]
    addrs: [MaxEndpoints]net.Addr
    pending: [MaxEndpoints]int
    // endpoints in different groups cannot reach each other
    groups: [MaxEndpoints]u8
    count: int
    nodes: int
    // the messages one endpoint keeps from one step; past this the newest
    // are dropped, as a full socket buffer drops them
    inbox: int
    loss: u64
    delay: u64
    duplicate: u64
    corrupt: u64
    faults: bool
    rng: math.Rng
    buf: [wire.MaxSize]u8
}

impl Free for Network {
    Free(self, a: &Alloc) {
        Network(socks, held, ..) := self
        socks.FreeEach(a, fn(d: net.Datagram) { d.Close() })
        held.Free(a)
    }
}

/// Binds a socket for every endpoint of cfg; an address with port 0 takes
/// the port the system gives. The network's choices are drawn from io's
/// random source, so a simulator's seed fixes them and a replay repeats
/// them. Fails as net.Bind does, with the sockets bound so far closed, with
/// Canceled when io gives no random bytes, and with OutOfMemory when the
/// socket list cannot grow. O(endpoints).
Network.Open(a: &Arena, h: &Alloc, io: Io, cfg: &Config, s: &Settings): net.Error|OutOfMemory!Network from h {
    var socks := HeapList[net.Datagram].New(h)
    onerr socks.FreeEach(h, fn(d: net.Datagram) { d.Close() })
    var addrs := cfg.addrs
    for i in 0..cfg.count {
        text := try a.Fmt("{addr}", (addr: addrs[i]))
        d := try net.Bind(io, text)
        onerr d.Close()
        addrs[i].port = Trunc[u16](d.Port())
        try socks.Push(h, d)
    }
    rng := try math.Rng.FromIo(io)
    return Network(
        socks: socks,
        held: coll.HeapDeque[raft.Msg].New(h),
        addrs: addrs,
        pending: [0; MaxEndpoints],
        groups: [0; MaxEndpoints],
        count: cfg.count,
        nodes: s.nodes,
        inbox: s.inbox,
        loss: Cast[u64](s.loss),
        delay: Cast[u64](s.delay),
        duplicate: Cast[u64](s.duplicate),
        corrupt: Cast[u64](s.corrupt),
        faults: s.faults,
        rng: rng,
        buf: [0; wire.MaxSize],
    )
}

/// Closes every socket; a message still held is lost, and counted.
Network.Close(self, a: &Alloc, c: &Counters) {
    Network(socks, held, ..) := self
    socks.FreeEach(a, fn(d: net.Datagram) { d.Close() })
    held.FreeEach(a, fn(m: raft.Msg) { Count(&c.lost) })
}

Network.Roll(mut self, percent: u64): bool {
    return percent > 0 && self.rng.Below(100) < percent
}

/// Draws the next partition: healed (four in ten), one node cut off (three
/// in ten), or the nodes split at random; each client lands on a random
/// side. O(endpoints).
Network.Partition(mut self) {
    for i in 0..self.count {
        self.groups[i] = 0
    }
    kind := self.rng.Float()
    if kind < 0.4 {
        return
    }
    if kind < 0.7 {
        lone := Cast[int](self.rng.Below(Cast[u64](self.nodes)))
        self.groups[lone] = 1
    } else {
        for i in 0..self.nodes {
            self.groups[i] = Trunc[u8](self.rng.Below(2))
        }
    }
    for i in self.nodes..self.count {
        self.groups[i] = Trunc[u8](self.rng.Below(2))
    }
}

/// How many held messages are addressed to a node.
Network.HeldForNodes(&self): int {
    var n := 0
    for i in 0..self.held.Len() {
        if m := self.held.Get(i) {
            if int(m.to) < self.nodes {
                n += 1
            }
        }
    }
    return n
}

/// Sends what the network held from the last step, then outbox in order.
/// Each message may be dropped, held for the next step or duplicated, the
/// copy going out next and meeting the same chances. Fails with Canceled,
/// which ends the run, and with OutOfMemory when a queue cannot grow.
/// O(messages).
Network.Flush(mut self, a: &Alloc, io: Io, sm: mut sim.Sim, c: &Counters, outbox: mut coll.HeapDeque[raft.Msg]): net.Error|OutOfMemory!void {
    for _ in 0..self.held.Len() {
        m := self.held.PopFront() else {
            break
        }
        try self.Transmit(io, sm, c, &m)
    }
    for m := outbox.PopFront() {
        Count(&c.sent)
        if self.groups[int(m.from)] != self.groups[int(m.to)] || self.Roll(self.loss) {
            Count(&c.dropped)
            continue
        }
        if self.Roll(self.delay) {
            try self.held.Push(a, m)
            Count(&c.delayed)
            continue
        }
        if self.Roll(self.duplicate) {
            try outbox.PushFront(a, m)
            Count(&c.duplicated)
        }
        try self.Transmit(io, sm, c, &m)
    }
}

// Encodes m and sends it from its sender's socket to its receiver. A
// failed send counts as a fault and loses the message; only Canceled, which
// ends the run, is returned. O(size of m).
Network.Transmit(mut self, io: Io, sm: mut sim.Sim, c: &Counters, m: &raft.Msg): net.Error!void {
    n := wire.Encode(m, mut self.buf[..])
    if self.Roll(self.corrupt) {
        at := Cast[int](self.rng.Below(Cast[u64](n)))
        var noise: [1]u8 = [0]
        self.rng.Fill(mut noise[..])
        self.buf[at] = self.buf[at] ^ (noise[0] | 1)
        Count(&c.corrupted)
    }
    if self.faults && self.Roll(2) {
        sm.FailNext(sim.Op.Write, sim.Fault.Reset)
    }
    to := int(m.to)
    self.socks[int(m.from)].Send(io, self.buf[..n], self.addrs[to]) else e {
        match e {
            Canceled {
                fail e
            }
            _ {
                Count(&c.faults)
                return
            }
        }
    }
    self.pending[to] += 1
}

/// Receives every datagram sent to endpoint i, decodes it and appends it to
/// inbox. One that does not decode, or names another receiver, is counted
/// and dropped; a failed receive leaves the rest for the next step. Fails
/// with Canceled, which ends the run, and OutOfMemory when the inbox
/// cannot grow. O(datagrams).
Network.Receive(mut self, a: &Alloc, io: Io, sm: mut sim.Sim, c: &Counters, i: int, inbox: mut coll.HeapDeque[raft.Msg]): net.Error|OutOfMemory!void {
    for self.pending[i] > 0 {
        if self.faults && self.Roll(2) {
            sm.FailNext(sim.Op.Read, sim.Fault.Reset)
        }
        got := self.socks[i].Recv(io, mut self.buf[..]) else e {
            match e {
                Canceled {
                    fail e
                }
                _ {
                    Count(&c.faults)
                    return
                }
            }
        }
        self.pending[i] -= 1
        m := wire.Decode(self.buf[..got.n]) else {
            Count(&c.rejected)
            continue
        }
        if int(m.to) != i {
            Count(&c.rejected)
            continue
        }
        Count(&c.delivered)
        try inbox.Push(a, m)
        if inbox.Len() > self.inbox {
            _ = inbox.Pop()
            Count(&c.overflowed)
        }
    }
}

/// Lets every datagram in flight arrive, and receives what each endpoint
/// still has, unhandled: the run is over. Fails as Receive does.
Network.Drain(mut self, a: &Alloc, io: Io, sm: mut sim.Sim, c: &Counters, inbox: mut coll.HeapDeque[raft.Msg]): net.Error|OutOfMemory!void {
    sm.Run()
    for i in 0..self.count {
        try self.Receive(a, io, sm, c, i, inbox)
        for inbox.Len() > 0 {
            _ = inbox.PopFront()
            Count(&c.late)
        }
    }
}
