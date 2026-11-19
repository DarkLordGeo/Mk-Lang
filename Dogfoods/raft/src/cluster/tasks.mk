// Why: nodes can run as tasks of their own, which hand their output back to
// the driver.
// What: Report, Lane and Lanes, and NodeTask.
// How: each node task has a Lane, a channel to the driver carrying a message
// to send, the end of its step with its state, or its failure.

import std.atomic
import std.coll
import std.sync
import src.raft

// What a node task hands back: a message to send, or the end of its step
// with its state, or that it ran out of memory.
struct Report {
    msg: raft.Msg
    done: bool
    failed: bool
    term: i64
    commit: i64
    elections: int
}

Report.Send(m: raft.Msg): Report {
    return Report(msg: m, done: false, failed: false, term: 0, commit: 0, elections: 0)
}

Report.EndOf(n: &raft.Node): Report {
    m := raft.Msg.New(raft.Kind.Reply, 0, 0, 0)
    return Report(msg: m, done: true, failed: false, term: n.CurrentTerm(), commit: n.CommitIndex(), elections: n.ElectionCount())
}

Report.Failure(): Report {
    m := raft.Msg.New(raft.Kind.Reply, 0, 0, 0)
    return Report(msg: m, done: true, failed: true, term: 0, commit: 0, elections: 0)
}

// One node task's channels: the messages of its step, then the step's
// time, and back the messages it sends.
struct Lane {
    inbox: Chan[raft.Msg]
    tick: Chan[i64]
    out: Chan[Report]
}

impl Free for Lane {
    Free(self, a: &Alloc) {
        Lane(inbox, tick, out) := self
        inbox.Free(a)
        tick.Free(a)
        out.Free(a)
    }
}

struct Lanes {
    list: HeapList[Lane]
}

impl Free for Lanes {
    Free(self, a: &Alloc) {
        Lanes(list) := self
        list.Free(a)
    }
}

Lane.New(a: &Alloc): OutOfMemory!Lane {
    inbox := try Chan[raft.Msg].New(a, 64)
    onerr inbox.Free(a)
    tick := try Chan[i64].New(a, 1)
    onerr tick.Free(a)
    out := try Chan[Report].New(a, 64)
    return Lane(inbox: inbox, tick: tick, out: out)
}

Lanes.New(a: &Alloc, n: int): OutOfMemory!Lanes {
    var list := HeapList[Lane].New(a)
    onerr list.Free(a)
    for _ in 0..n {
        lane := try Lane.New(a)
        onerr lane.Free(a)
        try list.Push(a, lane)
    }
    return Lanes(list: list)
}

// Hands node i the messages of its step, then the step's time.
Lanes.Hand(&self, io: Io, c: &Counters, i: int, inbox: mut coll.HeapDeque[raft.Msg], now: i64): Canceled|Closed!void {
    lane := &self.list[i]
    for m := inbox.PopFront() {
        try lane.inbox.Send(io, m)
        _ = c.queued.FetchAdd(1, atomic.Relaxed)
    }
    try lane.tick.Send(io, now)
}

// Takes node i's messages until the end of its step, and returns its state.
Lanes.Collect(&self, a: &Alloc, io: Io, i: int, outbox: mut coll.HeapDeque[raft.Msg]): Canceled|Closed|OutOfMemory|NodeFailed!Report {
    lane := &self.list[i]
    for {
        r := try lane.out.Recv(io)
        if r.failed {
            fail NodeFailed(node: i)
        }
        if r.done {
            return r
        }
        try outbox.Push(a, r.msg)
    }
}

// A node in a task of its own. It waits on its two channels with Select:
// the messages first, since their channel's name sorts first, then the
// time, which ends its step. The cluster's timing is made once, by
// whichever node asks first. It runs until its scope is cancelled.
NodeTask(h: &Heap, io: Io, lanes: &Lanes, id: int, s: Settings, seed: u64, once: &sync.Once[raft.Params], ready: &sync.Barrier, c: &Counters, hint: &Hint) {
    lane := &lanes.list[id]
    var arena := Arena.New(h) else {
        _ = ready.Wait(io) else {
            return
        }
        lane.out.Send(io, Report.Failure()) else {
            return
        }
        return
    }
    defer arena.Free()
    a := h.Alloc()
    params := once.Get(io, fn(): raft.Params { return s.params }) else {
        return
    }
    var node := raft.Node.New(&arena, id, s.nodes, *params, seed, 0) else {
        _ = ready.Wait(io) else {
            return
        }
        lane.out.Send(io, Report.Failure()) else {
            return
        }
        return
    }
    var queue := coll.HeapDeque[raft.Msg].New(a)
    defer queue.Free(a)
    var outq := coll.HeapDeque[raft.Msg].New(a)
    defer outq.Free(a)
    _ = ready.Wait(io) else {
        return
    }
    for {
        which := sync.Select(io, (inbox: &lane.inbox, tick: &lane.tick)) else {
            return
        }
        if which == 0 {
            m := lane.inbox.Recv(io) else {
                return
            }
            _ = c.queued.FetchSub(1, atomic.Relaxed)
            queue.Push(a, m) else {
                lane.out.Send(io, Report.Failure()) else {
                    return
                }
                return
            }
            continue
        }
        now := lane.tick.Recv(io) else {
            return
        }
        StepNode(mut node, a, mut queue, mut outq, c, hint, now) else {
            lane.out.Send(io, Report.Failure()) else {
                return
            }
            return
        }
        for m := outq.PopFront() {
            lane.out.Send(io, Report.Send(m)) else {
                return
            }
        }
        lane.out.Send(io, Report.EndOf(&node)) else {
            return
        }
    }
}
