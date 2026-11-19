// Why: a server's state is linear: it is opened from a data directory and
// must be closed again.
// What: Open (lock, snapshot, log replay) returning Opened, and Close
// returning Closed.
// How: the instance lock is taken first, then the snapshot and log recover
// the store, and the log is opened to append to.

import src.store
import src.wal

/// A recovered server, ready to start, and what recovery found.
export linear view struct Opened {
    export state: State
    export recovered: wal.Recovered
}

/// Takes the instance lock of the data directory `d`, recovers the store
/// from its snapshot and log, and opens the log to append to, for up to
/// `clients` connections at once. Fails with `InUse` when another server
/// holds the directory. The store's map is keyed with `key`; the log is
/// read into `arena`.
export Open(a: &Alloc, arena: &Arena, io: Io, d: wal.Dir, key: HashKey, policy: wal.SyncPolicy, crash: wal.Crash, clients: int): wal.Error!Opened from a, d {
    lock := try wal.TakeLock(io, d.lock)
    onerr lock.Abandon()
    var s := store.Store.New(a, key)
    onerr s.Free(a)
    rec := try wal.Recover(a, arena, io, &d, mut s)
    w := try wal.Wal.Open(io, d, lock, &rec, policy, crash)
    onerr w.Abandon()
    st := try State.New(a, s, w, clients)
    return Opened(state: st, recovered: rec)
}

/// What closing found.
export linear view struct Closed {
    export store: store.Store
    export crashed: bool
    export failure: ?wal.Error
}

/// Ends a server whose tasks have all ended. After a simulated crash the
/// log is left as it lies; otherwise it is compacted one last time and
/// marked clean. Returns the store for the caller to read and free; the
/// snapshot's sorted keys go in a scratch arena on `heap`.
export Close(st: State, heap: &Heap, io: Io): Closed from st {
    rem := st.Finish(heap.Alloc())
    Remains(store, wal: log, crashed) := rem
    if crashed {
        log.Abandon()
        return Closed(store: store, crashed: true, failure: none)
    }
    var w := log
    var failure: ?wal.Error = none
    var arena := Arena.New(heap) else {
        // the log is whole: only the last compaction is missed
        _ = w.Close(io)
        return Closed(store: store, crashed: false, failure: some(OutOfMemory))
    }
    w.Compact(&arena, io, &store) else e {
        failure = some(e)
    }
    arena.Free()
    if e := w.Close(io) {
        if failure.IsNone() {
            failure = some(e)
        }
    }
    return Closed(store: store, crashed: false, failure: failure)
}
