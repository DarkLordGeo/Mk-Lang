// Why: one run puts a cluster through partitions and crashes and keeps the
// history the checker judges.
// What: Run, Stats and the NodeFailed error.
// How: steps of StepMillis; every PartitionEvery steps the network is cut
// again and nodes go down for Downtime. Node tasks run in a local scope.

import std.atomic
import std.coll
import std.fs
import std.math
import std.net
import std.sim
import std.sync
import std.time
import src.history
import src.raft

/// What a run did to the network and the protocol.
export struct Stats {
    export sent: i64
    export dropped: i64
    export delayed: i64
    export duplicated: i64
    export corrupted: i64
    export rejected: i64
    export faults: i64
    export delivered: i64
    export overflowed: i64
    export handled: i64
    export late: i64
    export lost: i64
    /// Messages still held for a node when the run ended.
    export held: int
    export elections: int
    export term: i64
    export commit: i64
    export partitions: int
    export steps: int
    /// Times the clients' leader hint moved to another node.
    export repoints: int
    /// The nodes that believed they led at the end, one bit each.
    export leaders: u64
    /// A digest of every message the nodes handled.
    export checksum: u64
    /// Whether the node tasks made the shared configuration.
    export configured: bool
    /// Saves of a node's state, those that failed, and messages held back
    /// for them; nodes that restarted from a saved state.
    export saved: i64
    export unsaved: i64
    export muted: i64
    export restored: int
}

/// Every count zero. O(1).
export Stats.Zero(): Stats {
    return Stats(sent: 0, dropped: 0, delayed: 0, duplicated: 0, corrupted: 0, rejected: 0, faults: 0, delivered: 0, overflowed: 0, handled: 0, late: 0, lost: 0, held: 0, elections: 0, term: 0, commit: 0, partitions: 0, steps: 0, repoints: 0, leaders: 0, checksum: 0, configured: false, saved: 0, unsaved: 0, muted: 0, restored: 0)
}

// Reads the shared counters once every task has joined; the join already
// ordered every relaxed count before these loads.
Stats.Gather(mut self, c: &Counters) {
    self.sent = Read(&c.sent, atomic.Acquire)
    self.dropped = c.dropped.Load(atomic.Acquire)
    self.delayed = c.delayed.Load(atomic.Acquire)
    self.duplicated = c.duplicated.Load(atomic.Acquire)
    self.corrupted = c.corrupted.Load(atomic.Acquire)
    self.rejected = c.rejected.Load(atomic.Acquire)
    self.faults = c.faults.Load(atomic.Acquire)
    self.delivered = c.delivered.Load(atomic.Acquire)
    self.overflowed = c.overflowed.Load(atomic.Acquire)
    self.handled = c.handled.Load(atomic.Acquire)
    self.late = c.late.Load(atomic.Acquire)
    self.lost = c.lost.Load(atomic.Acquire)
    self.term = c.max_term.Load(atomic.SeqCst)
    self.leaders = c.leaders.Load(atomic.Acquire)
    self.checksum = c.checksum.Load(atomic.Acquire)
    self.saved = c.saved.Load(atomic.Acquire)
    self.unsaved = c.unsaved.Load(atomic.Acquire)
    self.muted = c.muted.Load(atomic.Acquire)
}

/// What lasts across the phases of a run, a crash between them: the
/// clients, the history they write, and the clock.
export view struct Phase {
    export history: history.History
    export clients: List[Client]
    /// Nanoseconds since the run began, at the last step taken.
    export clock: i64
    /// Phases begun, the first one included.
    export count: int
}

/// The first phase of a run: its clients from the seed, an empty history.
/// Fails with OutOfMemory when a cannot hold them. O(clients); allocates
/// in a.
export Phase.New(a: &Arena, s: &Settings): OutOfMemory!Phase from a {
    var clients := List[Client].New(a)
    var seeds := math.Rng.New(s.seed ^ 0x5DEE_CE66_D1CE_4E5B)
    for c in 0..s.clients {
        try clients.Push(Client.New(c, s.nodes, s.keys, s.ops, seeds.U64()))
    }
    return Phase(history: try history.History.Numbered(a, s.keys), clients: clients, clock: 0, count: 0)
}

/// A node task that could not get memory; the run ends with OutOfMemory.
export error NodeFailed(node: int)

/// Everything a run can fail with.
export type Error = net.Error | fs.Error | OutOfMemory | Closed | NodeFailed | BadState

// The simulated time between two steps of the network.
const StepMillis: i64 = 10

// How often a run with partitions draws a new one.
const PartitionEvery: i64 = 300_000_000

/// Runs one phase of a cluster and its clients until s.duration of the
/// run's clock, in steps of 10 ms. Every step receives what each endpoint
/// was sent, lets each node handle it and run its timers, makes durable
/// what changed when s.crash is set, lets each client answer and invoke,
/// and sends what they produced, in endpoint order, so one seed gives one
/// run. A phase after the first starts its nodes from what they made
/// durable. io carries the network, the disk and the clock and may be
/// recorded; local carries the node tasks and their channels when s.tasks
/// is set, so no task but this one calls io. sm takes the injected faults.
/// Fails as the network and the disk do, with Canceled when io stops
/// answering (a crash, or a replay that left its recording), and with
/// OutOfMemory when h or a is exhausted. O(steps * messages); allocates in
/// a.
export Run(a: &Arena, h: &Heap, alloc: &Alloc, io: Io, local: Io, sm: mut sim.Sim, s: &Settings, cfg: &Config, p: mut Phase): Error!Stats {
    counters := Counters.New()
    var network := try Network.Open(a, alloc, io, cfg, s)
    onerr network.Free(alloc)
    p.count += 1
    base := if p.count == 1 { 0 } else { p.clock + Downtime.Nanos() }
    var seeds := math.Rng.New(s.seed)
    // a node draws the same seed whether it runs here or in a task, so
    // both ways give one history
    var node_seeds: [raft.MaxNodes]u64 = [0; raft.MaxNodes]
    var nodes := List[raft.Node].New(a)
    var restored := 0
    for i in 0..s.nodes {
        node_seeds[i] = seeds.U64()
        var n := try raft.Node.New(a, i, s.nodes, s.params, node_seeds[i], base)
        if s.crash {
            if d := try Load(a, io, i) {
                try n.Restore(d.term, d.vote, d.entries.Items())
                restored += 1
            }
        }
        try nodes.Push(n)
    }
    hint := Hint.New()
    // every step drains the inbox, so it is empty whenever a run ends well
    var inbox := coll.HeapDeque[raft.Msg].New(alloc)
    onerr inbox.Free(alloc)
    var outbox := coll.HeapDeque[raft.Msg].New(alloc)
    defer outbox.Free(alloc)
    var lanes := try Lanes.New(alloc, s.nodes)
    defer lanes.Free(alloc)
    var stats := Stats.Zero()
    var reports := [Report.Failure(); raft.MaxNodes]
    var unsaved := [false; raft.MaxNodes]
    once := sync.Once[raft.Params].New()
    ready := sync.Barrier.New(s.nodes + 1)
    t0 := time.Now(io)
    var next_partition := base + PartitionEvery
    // The node tasks and their channels live on local, never on io: a
    // recording replays one task's calls only, and this driver is
    // that task.
    scope tasks := local.Scope() {
        if s.tasks {
            for i in 0..s.nodes {
                tasks.go NodeTask(h, local, &lanes, i, *s, node_seeds[i], &once, &ready, &counters, &hint)
            }
            _ = try ready.Wait(local)
        }
        for {
            now := base + time.Now(io).Since(t0).Nanos()
            p.clock = now
            if now >= s.duration.Nanos() || Finished(&p.clients) {
                break
            }
            stats.steps += 1
            if s.partitions && now >= next_partition {
                network.Partition()
                stats.partitions += 1
                next_partition += PartitionEvery
            }
            if s.tasks {
                for i in 0..s.nodes {
                    try network.Receive(alloc, io, sm, &counters, i, mut inbox)
                    try lanes.Hand(local, &counters, i, mut inbox, now)
                }
                for i in 0..s.nodes {
                    reports[i] = try lanes.Collect(alloc, local, i, mut outbox)
                }
            } else {
                for i in 0..s.nodes {
                    try network.Receive(alloc, io, sm, &counters, i, mut inbox)
                    try StepNode(mut nodes[i], alloc, mut inbox, mut outbox, &counters, &hint, now)
                }
            }
            if s.crash {
                try Persist(a, alloc, io, sm, mut network, nodes.ItemsMut(), mut unsaved, mut outbox, &counters)
            }
            leader := hint.Read().leader
            if leader >= 0 && Repoint(&counters, leader) {
                stats.repoints += 1
            }
            for c in 0..s.clients {
                try network.Receive(alloc, io, sm, &counters, s.nodes + c, mut inbox)
                for m := inbox.PopFront() {
                    try p.clients[c].Handle(alloc, mut outbox, mut p.history, &m, now)
                }
                try p.clients[c].Tick(alloc, mut outbox, mut p.history, now, leader)
            }
            try network.Flush(alloc, io, sm, &counters, mut outbox)
            try io.Sleep(time.Millis(StepMillis))
        }
        tasks.Cancel()
    }
    if params := once.Into() {
        stats.configured = params.heartbeat > 0
    }
    stats.held = network.HeldForNodes()
    try network.Drain(alloc, io, sm, &counters, mut inbox)
    inbox.FreeEmpty(alloc)
    network.Close(alloc, &counters)
    for i in 0..s.nodes {
        if s.tasks {
            r := &reports[i]
            stats.elections += r.elections
            stats.commit = math.Max(stats.commit, r.commit)
        } else {
            n := &nodes[i]
            stats.elections += n.ElectionCount()
            stats.commit = math.Max(stats.commit, n.CommitIndex())
        }
    }
    stats.Gather(&counters)
    stats.restored = restored
    return stats
}

/// The time a crashed cluster stays down before it restarts.
export const Downtime: time.Duration = time.Millis(500)

// Makes durable each node whose term, vote or log changed, before any of
// its messages go out (Raft figure 2). A node whose state could not be saved
// sends nothing until a save succeeds: its replies would promise what a
// crash could take back. Fails with Canceled, which ends the phase, and
// with OutOfMemory.
Persist(a: &Arena, alloc: &Alloc, io: Io, sm: mut sim.Sim, network: mut Network, nodes: mut []raft.Node, unsaved: mut [raft.MaxNodes]bool, outbox: mut coll.HeapDeque[raft.Msg], c: &Counters): fs.Error|OutOfMemory!void {
    for i in 0..nodes.Len() {
        if !nodes[i].TakeDirty() && !unsaved[i] {
            continue
        }
        if network.faults && network.Roll(5) {
            sm.FailNext(sim.Op.Sync, sim.Fault.NoSpace)
        }
        unsaved[i] = false
        Save(a, io, i, &nodes[i]) else e {
            match e {
                Canceled {
                    fail e
                }
                _ {
                    unsaved[i] = true
                    Count(&c.unsaved)
                }
            }
        }
        if !unsaved[i] {
            Count(&c.saved)
        }
    }
    for _ in 0..outbox.Len() {
        m := outbox.PopFront() else {
            break
        }
        from := int(m.from)
        if from < nodes.Len() && unsaved[from] {
            Count(&c.muted)
            continue
        }
        try outbox.Push(alloc, m)
    }
}

Finished(clients: &List[Client]): bool {
    for c in clients {
        if !c.Finished() {
            return false
        }
    }
    return true
}

// One node's step: the messages it was sent, in arrival order, then its
// timers. A leader publishes itself as the clients' hint.
StepNode(n: mut raft.Node, a: &Alloc, inbox: mut coll.HeapDeque[raft.Msg], outbox: mut coll.HeapDeque[raft.Msg], c: &Counters, hint: &Hint, now: i64): OutOfMemory!void {
    for m := inbox.PopFront() {
        try n.Handle(a, outbox, &m, now)
        Count(&c.handled)
        Fold(c, m.term, u8(m.kind))
    }
    try n.Tick(a, outbox, now)
    leads := n.CurrentRole() == raft.Role.Leader
    MarkLeader(c, n.Number(), leads)
    RaiseTerm(c, n.CurrentTerm())
    if leads {
        hint.Publish(n.CurrentTerm(), n.Number())
    }
}
