// Why: the network and the node tasks share counters and the current leader.
// What: Counters, Hint and the atomic helpers Read, Count and Order.
// How: plain counts use Relaxed atomics read after every task has joined;
// Hint publishes the latest leader and term.

import std.atomic

/// Counts of what happened to messages, shared by the network and the node
/// tasks. A count orders nothing, so each is counted Relaxed and read after
/// every task has joined.
struct Counters {
    sent: atomic.Atomic[i64]
    dropped: atomic.Atomic[i64]
    delayed: atomic.Atomic[i64]
    duplicated: atomic.Atomic[i64]
    corrupted: atomic.Atomic[i64]
    rejected: atomic.Atomic[i64]
    faults: atomic.Atomic[i64]
    delivered: atomic.Atomic[i64]
    overflowed: atomic.Atomic[i64]
    handled: atomic.Atomic[i64]
    late: atomic.Atomic[i64]
    lost: atomic.Atomic[i64]
    saved: atomic.Atomic[i64]
    unsaved: atomic.Atomic[i64]
    muted: atomic.Atomic[i64]
    // messages handed to node tasks and not yet handled
    queued: atomic.Atomic[i64]
    // bit i is set while node i believes it leads
    leaders: atomic.Atomic[u64]
    // the xor of every handled message's term and kind: equal runs handled
    // the same messages, in whatever order the tasks ran
    checksum: atomic.Atomic[u64]
    // the highest term any node reached
    max_term: atomic.Atomic[i64]
    // the leader the clients were last pointed at
    last_leader: atomic.Atomic[i64]
}

Counters.New(): Counters {
    return Counters(
        sent: atomic.Atomic[i64].New(0),
        dropped: atomic.Atomic[i64].New(0),
        delayed: atomic.Atomic[i64].New(0),
        duplicated: atomic.Atomic[i64].New(0),
        corrupted: atomic.Atomic[i64].New(0),
        rejected: atomic.Atomic[i64].New(0),
        faults: atomic.Atomic[i64].New(0),
        delivered: atomic.Atomic[i64].New(0),
        overflowed: atomic.Atomic[i64].New(0),
        handled: atomic.Atomic[i64].New(0),
        late: atomic.Atomic[i64].New(0),
        lost: atomic.Atomic[i64].New(0),
        saved: atomic.Atomic[i64].New(0),
        unsaved: atomic.Atomic[i64].New(0),
        muted: atomic.Atomic[i64].New(0),
        queued: atomic.Atomic[i64].New(0),
        leaders: atomic.Atomic[u64].New(0),
        checksum: atomic.Atomic[u64].New(0),
        max_term: atomic.Atomic[i64].New(0),
        last_leader: atomic.Atomic[i64].New(-1),
    )
}

// Reads a counter with the order the caller names; a wrapper that passes
// its order on states the operation's clause.
Read(c: &atomic.Atomic[i64], const o: atomic.Order): i64
    requires o.CanLoad() {
    return c.Load(o)
}

// Stores into a counter with the order the caller names.
Reset(c: &atomic.Atomic[i64], x: i64, const o: atomic.Order)
    requires o.CanStore() {
    c.Store(x, o)
}

// A fence of the order the caller names.
Order(const o: atomic.Order)
    requires o.CanFence() {
    atomic.Fence(o)
}

Count(c: &atomic.Atomic[i64]) {
    _ = c.FetchAdd(1, atomic.Relaxed)
}

// Raises max_term to term unless it is already higher. SeqCst so that the
// terms every task saw fall in one order with the hint's.
RaiseTerm(c: &Counters, term: i64) {
    var seen := c.max_term.Load(atomic.Relaxed)
    for seen < term {
        r := c.max_term.CompareExchange(seen, term, atomic.SeqCst, atomic.Relaxed)
        if r.ok {
            return
        }
        seen = r.value
    }
}

// Marks node id as leading or not in the leaders mask.
MarkLeader(c: &Counters, id: int, leads: bool) {
    bit := u64(1) << id
    if leads {
        _ = c.leaders.FetchOr(bit, atomic.Relaxed)
    } else {
        _ = c.leaders.FetchAnd(~bit, atomic.Relaxed)
    }
}

// Folds a handled message into the checksum: xor commutes, so the order
// the node tasks ran in does not change it.
Fold(c: &Counters, term: i64, kind: u8) {
    _ = c.checksum.FetchXor(Trunc[u64](term) *% 0x9E37_79B9_7F4A_7C15 ^ u64(kind), atomic.Relaxed)
}

// Points the clients at leader; returns whether that changed who they ask.
Repoint(c: &Counters, leader: int): bool {
    return c.last_leader.Swap(i64(leader), atomic.AcqRel) != i64(leader)
}

// The leader hint clients start from: the latest term known to have a
// leader, and that leader. Two words that must be read as a pair, so it is
// a sequence lock: a writer makes seq odd, writes, and makes it even again;
// a reader retries until it saw one even seq on both sides of its reads.
// Several node tasks may write at once, so a writer takes the odd seq by
// compare-exchange, and only a later term replaces the hint.
struct Hint {
    seq: atomic.Atomic[u64]
    term: atomic.Atomic[i64]
    leader: atomic.Atomic[i64]
}

Hint.New(): Hint {
    return Hint(seq: atomic.Atomic[u64].New(0), term: atomic.Atomic[i64].New(0), leader: atomic.Atomic[i64].New(-1))
}

Hint.Publish(&self, term: i64, leader: int) {
    for {
        s := self.seq.Load(atomic.Relaxed)
        if s & 1 == 1 {
            continue
        }
        if self.seq.CompareExchangeWeak(s, s + 1, atomic.Acquire, atomic.Relaxed).ok {
            // a reader that sees a store below also sees the odd seq
            Order(atomic.Release)
            if term > self.term.Load(atomic.Relaxed) {
                Reset(&self.term, term, atomic.Relaxed)
                Reset(&self.leader, i64(leader), atomic.Relaxed)
            }
            self.seq.Store(s + 2, atomic.Release)
            return
        }
    }
}

// The reader's acquire fence pairs with the writer's release fence: a read
// that saw a writer's store then sees its odd seq, so a torn pair never
// passes the check (Boehm, "Can seqlocks get along with programming
// language memory models?", 2012).
Hint.Read(&self): (term: i64, leader: int) {
    for {
        s1 := self.seq.Load(atomic.Acquire)
        if s1 & 1 == 1 {
            continue
        }
        term := self.term.Load(atomic.Relaxed)
        leader := self.leader.Load(atomic.Relaxed)
        atomic.Fence(atomic.Acquire)
        s2 := self.seq.Load(atomic.Relaxed)
        if s1 == s2 {
            return (term, leader: Trunc[int](leader))
        }
    }
}
