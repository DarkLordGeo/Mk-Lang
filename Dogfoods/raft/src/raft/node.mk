// Why: one Raft node, independent of sockets and clocks, so the simulator
// and the tests can drive it.
// What: Node with its role, term, log, elections, replication and commit.
// How: a state machine: Handle takes one message and Tick the due timers;
// both append the messages to send to a queue. Each rule cites the section
// of the Raft paper it follows.

import std.coll
import std.math

/// A node's role in its current term.
export enum Role: u8 {
    Follower = 0
    Candidate = 1
    Leader = 2
}

/// How a node keeps time and which fault it is built with, in nanoseconds
/// of the cluster's clock.
export struct Params {
    /// An election timeout is drawn from election_min to
    /// election_min + election_spread.
    export election_min: i64
    export election_spread: i64
    export heartbeat: i64
    /// A deliberate bug for testing the checker: every node answers Get
    /// from its own state at once, without the log, so reads go stale.
    export stale_reads: bool
}

/// What the store remembers of a client: its last applied command and the
/// result, replayed to a retry of that command.
struct Session {
    seq: i64
    value: i64
    ok: bool
}

const NoSession: Session = Session(seq: 0, value: 0, ok: false)

/// One Raft server (Ongaro and Ousterhout, "In Search of an Understandable
/// Consensus Algorithm", 2014): its persistent state (term, vote, log), its
/// volatile state, its timers and the register store it applies to.
/// Messages it sends go to the outbox its caller passes; it does no I/O.
export view struct Node {
    id: int
    size: int
    params: Params
    term: i64
    // -1 when the node has not voted in term
    voted_for: int
    role: Role
    // -1 when no leader is known in term
    leader: int
    // entry i, counted from 1 as in the paper, is log[i - 1]
    log: List[Entry]
    commit: i64
    applied: i64
    votes: coll.Bitset
    next: [MaxNodes]i64
    matched: [MaxNodes]i64
    timers: coll.PriorityQueue[Timer]
    // a timer whose gen is older than its kind's gen was cancelled
    election_gen: i64
    heartbeat_gen: i64
    rng: math.Rng
    kv: [MaxKeys]i64
    sessions: [MaxClients]Session
    elections: int
    // the term, the vote or the log changed since TakeDirty last ran
    dirty: bool
}

/// A follower in term 0 with an empty log, its election timer armed from
/// now. Fails with OutOfMemory when a cannot hold its log, vote set or
/// timers. O(1); allocates in a.
export Node.New(a: &Arena, id: int, size: int, params: Params, seed: u64, now: i64): OutOfMemory!Node from a {
    var n := Node(
        id: id,
        size: size,
        params: params,
        term: 0,
        voted_for: -1,
        role: Role.Follower,
        leader: -1,
        log: List[Entry].New(a),
        commit: 0,
        applied: 0,
        votes: try coll.Bitset.New(a, MaxNodes),
        next: [1; MaxNodes],
        matched: [0; MaxNodes],
        timers: coll.PriorityQueue[Timer].New(a),
        election_gen: 0,
        heartbeat_gen: 0,
        rng: math.Rng.New(seed),
        kv: [0; MaxKeys],
        sessions: [NoSession; MaxClients],
        elections: 0,
        dirty: false,
    )
    try n.ResetElection(now)
    return n
}

/// Restores the persistent state a node saved before a crash: its term,
/// its vote and its log. The commit index is volatile and starts at 0.
/// Fails with OutOfMemory when the log does not fit. O(entries).
export Node.Restore(mut self, term: i64, voted_for: int, entries: []Entry): OutOfMemory!void {
    self.term = term
    self.voted_for = voted_for
    for e in entries {
        try self.log.Push(e)
    }
}

/// Whether the term, the vote or the log changed since the last call, so
/// they must be made durable before the node's messages go out (Raft
/// figure 2, "Persistent state on all servers"). O(1).
export Node.TakeDirty(mut self): bool {
    d := self.dirty
    self.dirty = false
    return d
}

/// The node's number, 0 to size - 1. O(1).
export Node.Number(&self): int {
    return self.id
}

/// Follower, candidate or leader, in the node's current term. O(1).
export Node.CurrentRole(&self): Role {
    return self.role
}

/// The latest term the node has seen. O(1).
export Node.CurrentTerm(&self): i64 {
    return self.term
}

/// The node it voted for in its current term, or -1. O(1).
export Node.Vote(&self): int {
    return self.voted_for
}

/// The index of the last entry known committed. O(1).
export Node.CommitIndex(&self): i64 {
    return self.commit
}

/// How many elections this node started.
export Node.ElectionCount(&self): int {
    return self.elections
}

/// The index of the last entry, 0 when the log is empty.
export Node.LastIndex(&self): i64 {
    return i64(self.log.Len())
}

/// The log, entry 1 first, for the caller to persist.
export Node.Entries(&self): []Entry {
    return self.log.Items()
}

/// The term of entry i, 0 for index 0 and past the end.
export Node.TermAt(&self, i: i64): i64 {
    if i <= 0 || i > self.LastIndex() {
        return 0
    }
    return self.log[i - 1].term
}

Node.Majority(&self): int {
    return self.size / 2 + 1
}

/// Runs every timer due at now: an election timeout starts an election
/// (a leader only rearms it), and a leader's heartbeat sends Append to
/// every peer. Fails with OutOfMemory when a queue cannot grow. O(timers
/// due + peers).
export Node.Tick(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg], now: i64): OutOfMemory!void {
    for self.timers.Len() > 0 {
        due := self.timers.Peek() else {
            return
        }
        if due.at > now {
            return
        }
        t := self.timers.Pop() else {
            return
        }
        match t.kind {
            .Election {
                if t.generation == self.election_gen {
                    if self.role == Role.Leader {
                        try self.ResetElection(now)
                    } else {
                        try self.StartElection(a, out, now)
                    }
                }
            }
            .Heartbeat {
                if t.generation == self.heartbeat_gen && self.role == Role.Leader {
                    try self.Broadcast(a, out)
                    try self.ScheduleHeartbeat(now)
                }
            }
        }
    }
}

/// Handles one message received at now. A message of a later term makes
/// the node a follower of that term first (Raft section 5.1). Fails with
/// OutOfMemory when the log or a queue cannot grow. O(batch + peers).
export Node.Handle(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg], m: &Msg, now: i64): OutOfMemory!void {
    if m.kind != Kind.Request && m.kind != Kind.Reply && m.term > self.term {
        self.term = m.term
        self.voted_for = -1
        self.leader = -1
        self.dirty = true
        self.StepDown()
    }
    match m.kind {
        .Vote {
            try self.OnVote(a, out, m, now)
        }
        .VoteReply {
            try self.OnVoteReply(a, out, m, now)
        }
        .Append {
            try self.OnAppend(a, out, m, now)
        }
        .AppendReply {
            try self.OnAppendReply(a, out, m)
        }
        .Request {
            try self.OnRequest(a, out, m)
        }
        .Reply {
        }
    }
}

Node.StepDown(mut self) {
    if self.role == Role.Leader {
        self.heartbeat_gen += 1
    }
    self.role = Role.Follower
}

Node.ResetElection(mut self, now: i64): OutOfMemory!void {
    self.election_gen += 1
    spread := self.rng.Below(Cast[u64](self.params.election_spread))
    at := now + self.params.election_min + Cast[i64](spread)
    try self.timers.Push(Timer(at: at, kind: TimerKind.Election, generation: self.election_gen))
}

Node.ScheduleHeartbeat(mut self, now: i64): OutOfMemory!void {
    at := now + self.params.heartbeat
    try self.timers.Push(Timer(at: at, kind: TimerKind.Heartbeat, generation: self.heartbeat_gen))
}

Node.StartElection(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg], now: i64): OutOfMemory!void {
    self.term += 1
    self.role = Role.Candidate
    self.voted_for = self.id
    self.dirty = true
    self.leader = -1
    self.elections += 1
    for p in 0..self.votes.Len() {
        self.votes.Remove(p)
    }
    self.votes.Set(self.id)
    try self.ResetElection(now)
    if self.votes.Count() >= self.Majority() {
        try self.BecomeLeader(a, out, now)
        return
    }
    last := self.LastIndex()
    for p in 0..self.size {
        if p == self.id {
            continue
        }
        var m := Msg.New(Kind.Vote, self.id, p, self.term)
        m.last_index = last
        m.last_term = self.TermAt(last)
        try out.Push(a, m)
    }
}

// A candidate's log is at least as up to date as ours when its last term
// is later, or equal with an index at least ours (Raft section 5.4.1).
Node.UpToDate(&self, last_term: i64, last_index: i64): bool {
    mine := self.LastIndex()
    my_term := self.TermAt(mine)
    return last_term > my_term || (last_term == my_term && last_index >= mine)
}

Node.OnVote(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg], m: &Msg, now: i64): OutOfMemory!void {
    from := int(m.from)
    var granted := false
    if m.term == self.term && (self.voted_for == -1 || self.voted_for == from) && self.UpToDate(m.last_term, m.last_index) {
        granted = true
        self.voted_for = from
        self.dirty = true
        try self.ResetElection(now)
    }
    var r := Msg.New(Kind.VoteReply, self.id, from, self.term)
    r.ok = granted
    try out.Push(a, r)
}

Node.OnVoteReply(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg], m: &Msg, now: i64): OutOfMemory!void {
    from := int(m.from)
    // a duplicated reply counts once
    if self.role != Role.Candidate || m.term != self.term || !m.ok || self.votes.Has(from) {
        return
    }
    self.votes.Set(from)
    if self.votes.Count() >= self.Majority() {
        try self.BecomeLeader(a, out, now)
    }
}

// A new leader appends a no-op of its term, since it may commit entries of
// earlier terms only by committing one of its own (Raft section 5.4.2).
Node.BecomeLeader(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg], now: i64): OutOfMemory!void {
    self.role = Role.Leader
    self.leader = self.id
    next := self.LastIndex() + 1
    for p in 0..self.size {
        self.next[p] = next
        self.matched[p] = 0
    }
    try self.log.Push(Entry(term: self.term, cmd: NoCommand))
    self.dirty = true
    self.matched[self.id] = self.LastIndex()
    self.heartbeat_gen += 1
    try self.Broadcast(a, out)
    try self.ScheduleHeartbeat(now)
    try self.AdvanceCommit(a, out)
}

Node.Broadcast(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg]): OutOfMemory!void {
    for p in 0..self.size {
        if p != self.id {
            try self.SendAppend(a, out, p)
        }
    }
}

Node.SendAppend(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg], p: int): OutOfMemory!void {
    prev := self.next[p] - 1
    var m := Msg.New(Kind.Append, self.id, p, self.term)
    m.prev_index = prev
    m.prev_term = self.TermAt(prev)
    m.commit = self.commit
    var n := 0
    for n < MaxBatch && prev + i64(n) < self.LastIndex() {
        m.entries[n] = self.log[prev + i64(n)]
        n += 1
    }
    m.count = Trunc[u8](n)
    try out.Push(a, m)
}

Node.OnAppend(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg], m: &Msg, now: i64): OutOfMemory!void {
    var r := Msg.New(Kind.AppendReply, self.id, int(m.from), self.term)
    if m.term < self.term {
        try out.Push(a, r)
        return
    }
    self.StepDown()
    self.leader = int(m.from)
    try self.ResetElection(now)
    if m.prev_index > self.LastIndex() || self.TermAt(m.prev_index) != m.prev_term {
        r.match_index = math.Min(self.LastIndex(), m.prev_index - 1)
        try out.Push(a, r)
        return
    }
    for i in 0..int(m.count) {
        e := m.entries[i]
        idx := m.prev_index + 1 + i64(i)
        if idx <= self.LastIndex() {
            if self.TermAt(idx) == e.term {
                continue
            }
            self.Truncate(idx - 1)
        }
        try self.log.Push(e)
        self.dirty = true
    }
    last := m.prev_index + i64(m.count)
    commit := math.Min(m.commit, last)
    if commit > self.commit {
        self.commit = commit
        try self.Apply(a, out)
    }
    r.ok = true
    r.match_index = last
    try out.Push(a, r)
}

// Only entries past the commit index are ever removed: a leader's log holds
// every committed entry (Raft section 5.4, Leader Completeness).
Node.Truncate(mut self, n: i64) {
    for self.LastIndex() > n {
        _ = self.log.Pop()
        self.dirty = true
    }
}

Node.OnAppendReply(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg], m: &Msg): OutOfMemory!void {
    if self.role != Role.Leader || m.term != self.term {
        return
    }
    p := int(m.from)
    if m.ok {
        if m.match_index > self.matched[p] {
            self.matched[p] = m.match_index
        }
        self.next[p] = self.matched[p] + 1
        try self.AdvanceCommit(a, out)
        if self.next[p] <= self.LastIndex() {
            try self.SendAppend(a, out, p)
        }
        return
    }
    self.next[p] = math.Max(1, math.Min(self.next[p] - 1, m.match_index + 1))
    try self.SendAppend(a, out, p)
}

Node.Replicas(&self, n: i64): int {
    var count := 0
    for p in 0..self.size {
        if self.matched[p] >= n {
            count += 1
        }
    }
    return count
}

// The commit index moves to the last entry of the leader's own term that a
// majority holds (Raft section 5.3 and 5.4.2).
Node.AdvanceCommit(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg]): OutOfMemory!void {
    var n := self.LastIndex()
    for n > self.commit {
        if self.TermAt(n) == self.term && self.Replicas(n) >= self.Majority() {
            self.commit = n
            try self.Apply(a, out)
            return
        }
        n -= 1
    }
}

// Applies every committed entry in order. The leader answers each client
// whose command it applies; a command applied before answers with the
// session's result, so a retried request runs once.
Node.Apply(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg]): OutOfMemory!void {
    for self.applied < self.commit {
        self.applied += 1
        cmd := self.log[self.applied - 1].cmd
        if cmd.op == Op.Noop {
            continue
        }
        s := self.Execute(cmd)
        if self.role == Role.Leader {
            try self.Answer(a, out, cmd, s)
        }
    }
}

Node.Execute(mut self, cmd: Command): Session {
    c := int(cmd.client)
    last := self.sessions[c]
    if cmd.seq <= last.seq {
        return last
    }
    k := int(cmd.key)
    var s := Session(seq: cmd.seq, value: self.kv[k], ok: true)
    match cmd.op {
        .Noop {
        }
        .Get {
        }
        .Put {
            self.kv[k] = cmd.arg
        }
        .Cas {
            if self.kv[k] == cmd.arg {
                self.kv[k] = cmd.arg2
            } else {
                s.ok = false
            }
        }
    }
    self.sessions[c] = s
    return s
}

Node.Answer(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg], cmd: Command, s: Session): OutOfMemory!void {
    if cmd.seq != s.seq {
        return
    }
    var r := Msg.New(Kind.Reply, self.id, self.size + int(cmd.client), self.term)
    r.cmd = cmd
    r.served = true
    r.leader = Trunc[i8](self.id)
    r.value = s.value
    r.ok = s.ok
    try out.Push(a, r)
}

Node.OnRequest(mut self, a: &Alloc, out: mut coll.HeapDeque[Msg], m: &Msg): OutOfMemory!void {
    cmd := m.cmd
    if cmd.client < 0 || int(cmd.client) >= MaxClients || int(cmd.key) >= MaxKeys || cmd.op == Op.Noop {
        return
    }
    if self.params.stale_reads && cmd.op == Op.Get {
        try self.Answer(a, out, cmd, Session(seq: cmd.seq, value: self.kv[int(cmd.key)], ok: true))
        return
    }
    if self.role != Role.Leader {
        var r := Msg.New(Kind.Reply, self.id, self.size + int(cmd.client), self.term)
        r.cmd = cmd
        r.leader = Trunc[i8](self.leader)
        try out.Push(a, r)
        return
    }
    last := self.sessions[int(cmd.client)]
    if cmd.seq <= last.seq {
        try self.Answer(a, out, cmd, last)
        return
    }
    if self.Pending(cmd) {
        return
    }
    try self.log.Push(Entry(term: self.term, cmd: cmd))
    self.dirty = true
    self.matched[self.id] = self.LastIndex()
    try self.Broadcast(a, out)
    try self.AdvanceCommit(a, out)
}

// A retried command still waiting in the log is not appended again.
Node.Pending(&self, cmd: Command): bool {
    var i := self.commit
    for i < self.LastIndex() {
        e := self.log[i].cmd
        if e.client == cmd.client && e.seq == cmd.seq {
            return true
        }
        i += 1
    }
    return false
}
