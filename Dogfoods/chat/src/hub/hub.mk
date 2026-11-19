// Why: The server's tasks share one set of state; this file defines it and its
// settings.
//
// What: Config, Hub and Hub.New.
//
// How: Main makes the hub once, with every client slot allocated up front;
// tasks reach it by `&Hub`, and Release takes it apart after they all end.

import std.atomic
import std.log
import std.sync
import src.proto

/// The server's settings, fixed at start.
export struct Config {
    /// Connections served at once; the rest wait to be accepted.
    export max_clients: int
    /// Room frames queued per client before it counts as too slow.
    export queue: int
    /// Messages a client may send per refill; 0 means no limit.
    export burst: int
    /// Milliseconds between rate refills.
    export refill_ms: int
    /// Bytes each connection may allocate for its own state.
    export client_memory: int
    /// The operator's token for `KICK`, `SHUTDOWN` and `MOTD`; empty: none.
    export token: proto.Name
    /// How many bots join at start.
    export bots: int
    /// Milliseconds a connection may stay with nothing read or queued before
    /// it is dropped; 0 means never.
    export idle_ms: int
    /// Whether each TCP connection logs every byte it reads and writes.
    export record: bool
}

/// Everything the server's tasks share. Made once in `Main`, reached by
/// `&Hub` from every task, and taken apart by `Release` after they end.
export struct Hub {
    export cfg: Config
    export heap: &Heap
    export slots: HeapList[Slot]
    /// Free slot numbers; a connection takes one and gives it back.
    export free: Chan[int]
    /// Admission: one permit per slot, taken before `Accept`.
    export admit: sync.Semaphore
    /// The room table, made by whichever task needs it first.
    export rooms: sync.Once[sync.RwLock[Rooms]]
    /// The message of the day, shared by every greeting that is being written.
    export motd: Mutex[Msg]
    export history: History
    /// The bots and `Main` meet here before the first client is accepted.
    export bots_ready: sync.Barrier
    /// Woken once to begin shutdown.
    export stop: sync.Notify
    export stats: Stats
    /// Where connections, departures and recorded bytes are reported.
    export log: log.Sink
}

/// A hub for `cfg`, with `motd` as the first message of the day, reporting
/// to `sink`. Fails with `OutOfMemory` when the slots cannot be made.
/// O(max_clients); allocates every slot's queues.
export Hub.New(h: &Heap, io: Io, cfg: Config, motd: str, sink: log.Sink): OutOfMemory!Hub from h, sink {
    a := h.Alloc()
    var slots := HeapList[Slot].New(a)
    onerr slots.FreeEach(a, fn(s: Slot) {
        s.Release(a, h, &atomic.AtomicInt.New(0))
    })
    for _ in 0..cfg.max_clients {
        s := try Slot.New(a, cfg.queue, cfg.burst)
        slots.Push(a, s) else {
            s.Release(a, h, &atomic.AtomicInt.New(0))
            fail OutOfMemory
        }
    }
    free := try Chan[int].New(a, cfg.max_clients + 1)
    onerr free.Free(a)
    for i in 0..cfg.max_clients {
        // the channel has room for every slot, so this send never waits
        free.Send(io.NoCancel(), i) else {}
    }
    m := try sync.Shared[proto.Frame].New(h, proto.Frame.Of(proto.Kind.Line, motd))
    return Hub(
        cfg: cfg,
        heap: h,
        slots: slots,
        free: free,
        admit: sync.Semaphore.New(cfg.max_clients),
        rooms: sync.Once[sync.RwLock[Rooms]].New(),
        motd: Mutex.New(m),
        history: History.New(),
        bots_ready: sync.Barrier.New(cfg.bots + 1),
        stop: sync.Notify.New(),
        stats: Stats.New(),
        log: sink,
    )
}
