// Why: Every task reaches shared server state through the hub; the locking
// rules sit in one file.
//
// What: Hub methods: Table, Slot, Broadcast, NoticeAll, Greeting, SetMotd,
// TakeSlot, GiveBack and Release.
//
// How: The room table sits behind one RwLock, made on first use. A broadcast
// makes one shared frame and gives each member slot a reference-counted clone;
// a slot with no credit left is skipped and asked to go.

import std.atomic
import std.sync
import src.proto

/// The room table, made empty on first use. Fails with `Canceled` only while
/// another task is making it.
export Hub.Table(&self, io: Io): Canceled!&sync.RwLock[Rooms] from self {
    return try self.rooms.Get(io, fn(): sync.RwLock[Rooms] {
        return sync.RwLock.New(Rooms.New())
    })
}

/// The slot numbered `i`.
export Hub.Slot(&self, i: int): &Slot from self {
    return &self.slots[i]
}

/// Sends `frame` to every member of room `room`, skipping `except` (a slot
/// number, or -1 for none). Returns how many clients it reached. A member
/// that is too slow is skipped and asked to go. Fails with `Canceled`, or
/// `OutOfMemory` when the shared frame cannot be made.
export Hub.Broadcast(&self, io: Io, room: str, frame: proto.Frame, except: int): Canceled|OutOfMemory!int *self into self {
    m := try sync.Shared[proto.Frame].New(self.heap, frame)
    defer m.Free(self.heap)
    table := try self.Table(io)
    g := try table.LockRead(io)
    defer g.Unlock()
    rooms := g.Ref()
    i := rooms.Find(room) else {
        return 0
    }
    r := rooms.At(i)
    var n := 0
    var at := -1
    for s := r.NextMember(at) {
        at = s
        if s != except && self.slots[s].Deliver(io, self.heap, &m) {
            n += 1
        }
    }
    return n
}

/// Sends a notice to every connected client. Returns how many it reached.
/// Fails with `OutOfMemory` when the shared frame cannot be made.
export Hub.NoticeAll(&self, io: Io, frame: proto.Frame): OutOfMemory!int *self into self {
    m := try sync.Shared[proto.Frame].New(self.heap, frame)
    defer m.Free(self.heap)
    var n := 0
    for s in &self.slots {
        if s.live.Load() && s.Notice(io, self.heap, &m) {
            n += 1
        }
    }
    return n
}

/// A reference to the current message of the day, for a greeting. The caller
/// frees it with `Free(heap)`. Fails with `Canceled` while waiting for the lock.
export Hub.Greeting(&self, io: Io): Canceled!Msg {
    g := try self.motd.Lock(io)
    m := g.Ref().Clone()
    g.Unlock()
    atomic.Fence(atomic.Acquire)
    return m
}

/// Replaces the message of the day. Greetings already holding the old one
/// finish with it; the last of them frees it. Fails with `Canceled` or
/// `OutOfMemory`, keeping the old message.
export Hub.SetMotd(&self, io: Io, text: str): Canceled|OutOfMemory!void *self into self {
    m := try sync.Shared[proto.Frame].New(self.heap, proto.Frame.Of(proto.Kind.Line, text))
    var g := self.motd.Lock(io) else e {
        m.Free(self.heap)
        fail e
    }
    old := Replace(g.Mut(), m)
    g.Unlock()
    old.Free(self.heap)
    atomic.Fence(atomic.Release)
    self.stats.Flag(FlagMotdSet, true)
}

/// Takes a free slot for a new connection. Admission has already taken a
/// permit, so one is free; fails with `Canceled` only.
export Hub.TakeSlot(&self, io: Io): Canceled!int {
    i := self.free.Recv(io) else e {
        match e {
            Canceled {
                fail Canceled
            }
            Closed {
                // the free list is closed only by Release, after every task
                Panic("free slot list closed while serving")
            }
        }
    }
    self.slots[i].live.Store(true)
    _ = self.stats.Connected()
    return i
}

/// Gives slot `i` back after its connection ended: leaves every room, empties
/// the slot and lets the next connection in. Returns the frames never written.
export Hub.GiveBack(&self, io: Io, i: int): int {
    calm := io.NoCancel()
    // calm is never cancelled, so neither call below can fail
    table := self.Table(calm) else {
        Panic("the room table failed under NoCancel")
    }
    var g := table.Lock(calm) else {
        Panic("a lock failed under NoCancel")
    }
    _ = g.Mut().Leave(i)
    g.Unlock()
    lost := self.slots[i].Reset(calm, self.heap)
    self.free.Send(calm, i) else {}
    self.stats.Disconnected()
    self.admit.Release(1)
    return lost
}

/// Frees everything, after every task has ended. Panics unless every slot
/// came back to the free list; returns the final traffic and the frames that
/// were queued and never written.
export Hub.Release(self, io: Io): (traffic: Traffic, undelivered: i64) {
    Hub(cfg, heap, slots, free, admit: _, rooms, motd, history: _, bots_ready: _, stop: _, stats, log: _) := self
    a := heap.Alloc()
    var back := 0
    for free.Ready() {
        _ = free.Recv(io.NoCancel()) else {
            break
        }
        back += 1
    }
    Assert(back == cfg.max_clients)
    free.Close()
    free.FreeEmpty(a)
    slots.FreeEach(a, fn(s: Slot) {
        s.Release(a, heap, &stats.undelivered)
    })
    // the table is plain: taking it out of its lock and its once is all its release
    if table := rooms.Into() {
        _ = table.Into()
    }
    motd.Into().Free(heap)
    undelivered := stats.undelivered.Load()
    return (traffic: stats.Into(), undelivered)
}
