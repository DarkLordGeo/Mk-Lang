// Why: the protocol's messages and log entries, shared by the node and the
// wire format.
// What: Msg, Entry, Command, Kind, Op and the size limits.
// How: fixed-size values for a fixed membership of at most MaxNodes nodes,
// so a message never allocates.

/// The largest cluster this implementation runs: a fixed membership of five.
export const MaxNodes: int = 5

/// Log entries one Append message carries at most.
export const MaxBatch: int = 4

/// Keys of the replicated register store.
export const MaxKeys: int = 8

/// Clients the store keeps a session for.
export const MaxClients: int = 8

/// What a client asks of the store. Noop is the entry a new leader commits
/// to learn its commit index, and no client sends it.
export enum Op: u8 {
    Noop = 0
    Get = 1
    Put = 2
    Cas = 3
}

/// One client operation: client and seq name it, so a retried request is
/// applied once (Raft section 6.3's sessions).
export struct Command {
    export client: i32
    export seq: i64
    export op: Op
    export key: u8
    export arg: i64
    export arg2: i64
}

/// A log entry: the term its leader received it in, and the command.
export struct Entry {
    export term: i64
    export cmd: Command
}

/// The six messages of the protocol: RequestVote and AppendEntries with
/// their replies (Raft figure 2), and a client's request and its reply.
export enum Kind: u8 {
    Vote = 1
    VoteReply = 2
    Append = 3
    AppendReply = 4
    Request = 5
    Reply = 6
}

/// A message. One struct for every kind keeps it plain, so it travels in
/// channels and queues by value; the fields a kind does not use are zero.
export struct Msg {
    export kind: Kind
    /// The sending and receiving endpoints: nodes are 0 to size - 1, and
    /// client c is size + c.
    export from: u8
    export to: u8
    export term: i64
    /// Vote: the candidate's last log index and its term.
    export last_index: i64
    export last_term: i64
    /// VoteReply: granted. AppendReply: the entries matched.
    export ok: bool
    /// Append: the entry before the batch, the leader's commit index and
    /// the batch.
    export prev_index: i64
    export prev_term: i64
    export commit: i64
    export count: u8
    export entries: [MaxBatch]Entry
    /// AppendReply: the last index known to match the leader's log, or a
    /// hint where to retry from when ok is false.
    export match_index: i64
    /// Request and Reply: the command.
    export cmd: Command
    /// Reply: false when the receiver is not the leader; then leader is
    /// the node it believes leads, or -1.
    export served: bool
    export leader: i8
    /// Reply: the register's value before the command ran.
    export value: i64
}

/// The command of a no-op entry.
export const NoCommand: Command = Command(client: 0, seq: 0, op: Op.Noop, key: 0, arg: 0, arg2: 0)

const NoEntry: Entry = Entry(term: 0, cmd: NoCommand)

/// A message of kind from one endpoint to another, every other field zero.
/// O(1); allocates nothing.
export Msg.New(kind: Kind, from: int, to: int, term: i64): Msg {
    return Msg(
        kind: kind,
        from: Trunc[u8](from),
        to: Trunc[u8](to),
        term: term,
        last_index: 0,
        last_term: 0,
        ok: false,
        prev_index: 0,
        prev_term: 0,
        commit: 0,
        count: 0,
        entries: [NoEntry; MaxBatch],
        match_index: 0,
        cmd: NoCommand,
        served: false,
        leader: -1,
        value: 0,
    )
}
