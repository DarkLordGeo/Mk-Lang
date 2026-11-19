// Why: a Raft cluster is correct only if its clients' history is
// linearizable; this decides it.
// What: Check, its Verdict, and the TooManyOps error.
// How: Wing and Gong's search with Lowe's cache of visited points, one
// register at a time (linearizability is local); events form a doubly linked
// list whose entries are lifted out and put back on backtracking.

import src.history

/// A register with more operations than one search can track.
export error TooManyOps(key: int, ops: int)

/// The operations one register's search tracks at most: a linearized set
/// is one u64.
export const MaxOps: int = 64

/// The checker's answer. When ok is false, key names the first register
/// whose operations have no linearization, with its operation count.
export struct Verdict {
    export ok: bool
    export key: int
    export ops: int
    /// Search states visited over every register.
    export states: int
}

// One point of the search: the operations linearized so far and the
// register's value after them. Two paths that reach one point have the same
// future, so the second is cut (Lowe, "Testing for linearizability", 2017).
struct Point {
    done: u64
    value: i64
}

impl Eq for Point {
    Eq(&self, other: &Point): bool {
        return self.done == other.done && self.value == other.value
    }
}

impl Hash for Point {
    Hash(&self, h: mut Hasher) {
        h.U64(self.done)
        h.U64(Trunc[u64](self.value))
    }
}

struct Event {
    time: i64
    is_ret: bool
    op: int
}

// A response sorts before an invocation at the same instant: a client that
// was answered at t and then asked again at t did so in that order.
EventLess(a: &Event, b: &Event): bool {
    if a.time != b.time {
        return a.time < b.time
    }
    if a.is_ret != b.is_ret {
        return a.is_ret
    }
    return a.op < b.op
}

// The register's sequential specification. An operation with no response
// may have run or not; running it last is always possible, so it is
// accepted in either case and its effect is the one it would have had.
Step(value: i64, op: &history.Op): ?i64 {
    unknown := op.ret < 0
    return match op.kind {
        .Get {
            if unknown || op.value == value { some(value) } else { none }
        }
        .Put {
            some(op.arg)
        }
        .Cas {
            swaps := value == op.arg
            if unknown || swaps == op.ok { some(if swaps { op.arg2 } else { value }) } else { none }
        }
    }
}

/// Decides whether a history is linearizable as a set of registers that
/// start at 0, one register at a time, since linearizability is local
/// (Herlihy and Wing, 1990). The search is Wing and Gong's with Lowe's
/// cache of visited points. Fails with TooManyOps for a register with more
/// than MaxOps operations and OutOfMemory when a cannot hold the search.
/// Exponential in the operations of one register at worst, polynomial when
/// few overlap; allocates in a sub-arena of a, freed before it returns.
export Check(a: &Arena, h: &history.History): TooManyOps|OutOfMemory!Verdict {
    var states := 0
    for key in 0..h.keys.Len() {
        var scratch := try a.Sub()
        defer scratch.Free()
        var ops := List[history.Op].New(&scratch)
        for op in h.ops {
            if op.key == key {
                try ops.Push(op)
            }
        }
        if ops.Len() > MaxOps {
            fail TooManyOps(key: key, ops: ops.Len())
        }
        r := try Search(&scratch, ops.Items())
        states += r.states
        if !r.ok {
            return Verdict(ok: false, key: key, ops: ops.Len(), states: states)
        }
    }
    return Verdict(ok: true, key: -1, ops: h.ops.Len(), states: states)
}

struct Found {
    ok: bool
    states: int
}

// The events of ops in time order as a doubly linked list over indices,
// with node n2 = 2 * ops.Len() as the head; a linearized operation's call
// and return are lifted out and put back on backtracking.
Search(a: &Arena, ops: []history.Op): OutOfMemory!Found {
    n := ops.Len()
    n2 := 2 * n
    var events := List[Event].New(a)
    for i, op in ops {
        try events.Push(Event(time: op.call, is_ret: false, op: i))
        ret := if op.ret < 0 { 9_223_372_036_854_775_807 } else { op.ret }
        try events.Push(Event(time: ret, is_ret: true, op: i))
    }
    events.Sort(EventLess)
    next := try a.Slice[int](n2 + 1)
    prev := try a.Slice[int](n2 + 1)
    partner := try a.Slice[int](n2)
    call_of := try a.Slice[int](n)
    for i in 0..n2 {
        next[i] = if i + 1 < n2 { i + 1 } else { n2 }
        prev[i] = if i == 0 { n2 } else { i - 1 }
        e := events[i]
        if !e.is_ret {
            call_of[e.op] = i
        }
    }
    next[n2] = if n2 > 0 { 0 } else { n2 }
    prev[n2] = if n2 > 0 { n2 - 1 } else { n2 }
    for i in 0..n2 {
        e := events[i]
        if e.is_ret {
            partner[i] = call_of[e.op]
            partner[call_of[e.op]] = i
        }
    }
    var seen := Set[Point].New(a)
    var stack := List[(at: int, value: i64)].New(a)
    var done: u64 = 0
    var value: i64 = 0
    var at := next[n2]
    var states := 0
    for next[n2] != n2 {
        if at == n2 {
            // every remaining event was tried from this point
            top := stack.Pop() else {
                return Found(ok: false, states: states)
            }
            value = top.value
            op := events[top.at].op
            done = done & ~(u64(1) << op)
            Relink(next, prev, partner[top.at])
            Relink(next, prev, top.at)
            at = next[top.at]
            continue
        }
        e := events[at]
        if e.is_ret {
            top := stack.Pop() else {
                return Found(ok: false, states: states)
            }
            value = top.value
            op := events[top.at].op
            done = done & ~(u64(1) << op)
            Relink(next, prev, partner[top.at])
            Relink(next, prev, top.at)
            at = next[top.at]
            continue
        }
        if after := Step(value, &ops[e.op]) {
            point := Point(done: done | (u64(1) << e.op), value: after)
            if try seen.Add(point) {
                states += 1
                try stack.Push((at, value))
                value = after
                done = point.done
                Unlink(next, prev, at)
                Unlink(next, prev, partner[at])
                at = next[n2]
                continue
            }
        }
        at = next[at]
    }
    return Found(ok: true, states: states)
}

Unlink(next: mut []int, prev: mut []int, i: int) {
    next[prev[i]] = next[i]
    prev[next[i]] = prev[i]
}

// Puts i back between the neighbours it had when it was unlinked, which
// are linked again by then since unlinking is undone in reverse order.
Relink(next: mut []int, prev: mut []int, i: int) {
    next[prev[i]] = i
    prev[next[i]] = i
}
