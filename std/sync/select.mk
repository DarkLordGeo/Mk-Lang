// Why: a task that serves several channels must wait until any one of them
// has a value, without polling and without a lost wake-up.
//
// What: sync.Select and the Selectable interface (Ready, Watch, Unwatch,
// Pass): wait until one channel of a record can Recv without waiting, and
// return that field's index in byte order of the names (the order `fields`
// visits a record in). The caller then calls Recv on that field.
//
// How: Go's selectgo without the receive: scan the fields, and when none is
// ready link one watch cell per field into its channel and park.

// Algorithm (std/core/sync_chan.mk, "sync.Select and sync.RwLock
// support"): Go's selectgo without the receive. A scan of Ready in
// field order; when nothing is ready, one watch cell per field, from a
// SelPark on this frame, is linked into that channel's watch queue under
// the channel's lock, unless the channel is ready by then; the Select
// parks on the park's fired word until an operation on a watched channel
// fires it; then every cell still linked is unlinked, under each channel's
// lock, and the scan runs again. A Send wakes one Select that watches its
// channel, Close every one; a Select that a channel fired passes
// the wake on to that channel as it leaves (Pass: one more Select is fired
// if the channel is still ready), whatever it returns, so a value it did
// not take is not left beside parked Selects. The first ready field in order wins, so a
// channel that is always ready starves the ones after it, as a
// fixed-priority select (Go's select is uniformly random; Mk fixes the
// index order). O(n) per scan, plus O(n) for each field before the found one
// that a Send makes ready during the scan, O(n^2) at worst, and
// O(n) per round of watches; no allocation; a record of more than
// core.SelCells fields polls every 1 ms.
//
// Cancellation: a cancellation point first (level-triggered: in a
// cancelled scope Select returns Canceled even when a channel is ready, as
// Recv does), and a cancelled wait returns Canceled; every cell is
// unlinked before Select returns, on every path.

import std.core

/// What Select can wait on. Chan[T] and &Chan[T] implement it for every
/// T, and no other type can: its methods name a type of std's own, which
/// no module outside std can import.
///
/// Ready reports whether Recv would not wait now: a value is buffered, a
/// sender is waiting, or the channel is closed. The answer may be stale as
/// soon as it is given; a Recv after a true Ready waits only if another
/// task received meanwhile. Watch, Unwatch and Pass are Select's own: they
/// register a waiting Select with the channel, take it off again, and pass
/// a wake on to one more waiting Select while the channel is still ready.
export interface Selectable {
    Ready(&self): bool
    Watch(&self, p: *core.SelPark, i: int): bool
    Unwatch(&self, p: *core.SelPark, i: int)
    Pass(&self, p: *core.SelPark)
}

impl[T] Selectable for Chan[T] {
    Ready(&self): bool {
        return core.ChanReady(self)
    }
    Watch(&self, p: *core.SelPark, i: int): bool {
        return core.ChanWatch(self, p, i)
    }
    Unwatch(&self, p: *core.SelPark, i: int) {
        core.ChanUnwatch(self, p, i)
    }
    Pass(&self, p: *core.SelPark) {
        core.ChanPass(self, p)
    }
}

impl[T] Selectable for &Chan[T] {
    Ready(&self): bool {
        return core.ChanReady(*self)
    }
    Watch(&self, p: *core.SelPark, i: int): bool {
        return core.ChanWatch(*self, p, i)
    }
    Unwatch(&self, p: *core.SelPark, i: int) {
        core.ChanUnwatch(*self, p, i)
    }
    Pass(&self, p: *core.SelPark) {
        core.ChanPass(*self, p)
    }
}

// SelectScan is the index of the first ready field, or -1. The fields are
// read one after the other, so a field before the one found may have
// become ready after its read, by a Send that came before the found
// field's (say, a step's messages, then its time). The fields before
// it are read again, until none is ready: a Send that came before the
// found field became ready is seen by a read after it.
// O(n) when no field before the found one is ready on the second read,
// O(n^2) at worst.
SelectScan[R: Fields[Selectable] + Plain](chans: R): int {
    var i := 0
    var found := -1
    for _, c in fields(&chans) {
        if found < 0 && c.Ready() {
            found = i
        }
        i += 1
    }
    for found > 0 {
        var j := 0
        var earlier := -1
        for _, c in fields(&chans) {
            if j < found && earlier < 0 && c.Ready() {
                earlier = j
            }
            j += 1
        }
        if earlier < 0 {
            break
        }
        found = earlier
    }
    return found
}

/// Waits until a channel of the record chans can Recv without waiting (a
/// value buffered, a sender waiting, or closed) and returns that field's
/// index, counted in byte order of the field names. The caller then calls
/// Recv on that field. chans is a record whose fields are each a Chan or
/// a &Chan.
///
/// The fields are read in that order, and the fields before the one found
/// are read again until none of them is ready. So a field made ready by a
/// Send or Close before the found field became ready is returned instead,
/// unless another task received from it meanwhile. The first ready field
/// wins, so a channel that is always ready starves the fields after it.
/// A Send wakes one Select watching its channel, and that Select passes
/// the wake on as it returns while the channel is still ready; a Close
/// wakes every one.
///
/// A cancellation point: Canceled in a cancelled scope, even when a
/// channel is ready, and Canceled from a cancelled wait. A record of more
/// than 64 fields is polled every 1 ms instead of waited on. O(n) per
/// wake for n channels, plus O(n) for each field before the found one that
/// a Send makes ready during the scan; O(n^2) at worst. It allocates
/// nothing: what it registers with each channel lives on its own stack.
export Select[R: Fields[Selectable] + Plain](io: Io, chans: R): Canceled!int {
    // The cancellation point's fast test inline (core.CancelClear: a native
    // Io while no scope is cancelled), so such a call pays no call for it.
    if !core.CancelClear(io) {
        try core.SyncCancel(io)
    }
    first := SelectScan(chans)
    if first >= 0 {
        return first
    }
    var n := 0
    for _, _ in fields(&chans) {
        n += 1
    }
    if n > core.SelCells {
        // More fields than cells: poll.
        for {
            try io.Sleep(core.DurationOf(1_000_000))
            i := SelectScan(chans)
            if i >= 0 {
                return i
            }
        }
    }
    i := SelectPark(io, chans)
    if i < 0 {
        fail Canceled
    }
    return i
}

// SelectPark is Select's wait: the ready index, or -1 when cancelled. The
// park lives in this frame until the end, after every cell is unlinked.
SelectPark[R: Fields[Selectable] + Plain](io: Io, chans: R): int {
    var park := core.SelParkNew(io)
    p := core.SelParkOf(mut park)
    var res := -2
    for res == -2 {
        // Watch in field order; a ready channel stops the round.
        var linked := 0
        var ready := false
        for _, c in fields(&chans) {
            if !ready {
                if c.Watch(p, linked) {
                    ready = true
                } else {
                    linked += 1
                }
            }
        }
        canceled := if !ready { core.SelParkWait(p) } else { false }
        var j := 0
        for _, c in fields(&chans) {
            if j < linked {
                c.Unwatch(p, j)
            }
            j += 1
        }
        // Every cell is unlinked, so no channel fires p any more this
        // round: by is final (-1 when none fired it).
        by := core.SelParkFiredBy(p)
        if canceled {
            res = -1
        } else {
            i := SelectScan(chans)
            if i >= 0 {
                res = i
            } else {
                core.SelParkReset(p) // nothing ready: the readiness by fired for ended
            }
        }
        if res != -2 && by >= 0 {
            // Leaving after channel by fired p: pass its wake on.
            var k := 0
            for _, c in fields(&chans) {
                if k == by {
                    c.Pass(p)
                }
                k += 1
            }
        }
    }
    _ = core.SelParkOf(mut park) // park lives to here: channels linked its cells
    return res
}
