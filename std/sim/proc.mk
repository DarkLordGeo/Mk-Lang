// Why: std.proc's waits (Child.Wait, PipeReader.Read, PipeWriter.WriteAll,
// Pty.Read, Pty.Write) must be testable under a Sim: a deadline or a cancel
// interrupts them and the same call can be made again, with no real
// process behind them.
//
// What: simulated children (Kid), pipes and pseudo-terminals (SimPty), the
// call entry's process ops (ProcCall, CallSpawn, CallPipe, CallSignal,
// CallPtyOpen, CallPtyResize), ProcWait, and PipeSend and PipeSendLoop.
//
// How: a child runs no program, it behaves as cat; every pipe is a pair of
// network connection ends marked pipe, so bytes travel at the network's
// seeded pace. The model in detail follows.
//
// Model. A spawned child runs no program: it is cat. Its standard input
// and output, when both are pipes, are one simulated stream: what the
// parent writes to the child's stdin arrives at its stdout at the
// network's seeded pace (net.mk's Deliver). Every pipe is a pair of
// connection ends marked pipe: a write takes at most the room left of
// PipeCap unread bytes and waits while there is none; a write whose
// reader is gone, or whose child has ended, is Closed; a read after the
// writer is gone, or the child has ended, returns the bytes already
// arrived and then 0. The child exits with code 0 when its input ends
// (the parent closes its stdin, or the Pty is closed; at once when stdin
// is not a pipe), and at once on a kill, reported as the target reports
// one (Linux: minus the signal; Windows: code 1, and any signal but a
// kill Unsupported); the bytes still in flight to its stdout are lost
// then. Arguments, environment and directory are ignored, except that a
// program named "missing" is NotFound; a lent handle (core.IoStdHandle)
// is as Null: the child reads nothing from it. A Pty is a stream from its
// writer to its reader (the terminal echoes what is written); it ends
// after the last child spawned on it ends, and a later Spawn on it opens
// it again. A Pty's term is its index + 1; a child's pid its index + 100.
// No call here draws from the random stream but a pipe's Deliver, so a
// run with no process keeps every seed's schedule.

import std.core
import std.raw
import std.rt

// PipeCap is a simulated pipe's capacity in unread bytes (Linux's default
// pipe buffer).
const PipeCap: int = 65536

// Full in a pipe write's os: the pipe holds PipeCap unread bytes, so the
// write must wait for its reader.
const Full: i64 = 2

// A simulated child. h is its process handle (0 once closed: waited for
// or detached); inp the end whose closing ends its input (the parent's
// stdin end, or its Pty's writer), -1 once ended; own the child's sides of
// its pipes, closed when it ends; loop the parent's stdin end when its
// peer is the parent's stdout (cat's one stream), -1 none; pty its Pty's
// index, -1 none; code its exit as ChildWait reports it once done.
struct Kid {
    used: bool
    h: uint
    inp: int
    own: [3]int
    loop: int
    pty: int
    done: bool
    code: i64
}

// A simulated pseudo-terminal: a, the writer's end, whose bytes arrive at
// its peer, the reader's (-1 once the writer's end closed: the Pty is
// closed); live, its children not yet ended; its size.
struct SimPty {
    a: int
    live: int
    cols: int
    rows: int
}

// ProcCall answers the call a when its op is one of this file's, else
// none.
ProcCall(io: Io, a: *core.IoCallArgs): ?core.IoResult {
    unsafe "a is the caller's record, live until the call entry returns" {
        op := (*a).op
        if op == core.IoOpPipe {
            return some(CallPipe(io, a))
        }
        if op == core.IoOpSpawn {
            return some(CallSpawn(io, a))
        }
        if op == core.IoOpSignal {
            return some(CallSignal(io, (*a).h, Trunc[u32]((*a).w.a)))
        }
        if op == core.IoOpDetach {
            // The child runs on; its handle is given up (rt.ChildDetach
            // closes a foreign one itself, so only a direct call lands
            // here).
            _ = rt.CloseRuntimeHandle((*a).h)
            return some(Ok(0))
        }
        if op == core.IoOpGroupEnd || op == core.IoOpPtyClose {
            // No group and no console: nothing to end.
            return some(Ok(0))
        }
        if op == core.IoOpPtyOpen {
            return some(CallPtyOpen(io, a))
        }
        if op == core.IoOpPtyResize {
            w := (*a).w
            return some(CallPtyResize(io, Trunc[uint](w.c), w.a, w.b))
        }
    }
    return none
}

// PipePair adds a pipe: two connection ends marked pipe, the first's index
// (bytes written there arrive at the second), or -1 when out of memory.
PipePair(w: mut World): int {
    k := NewPair(w, 0, 0)
    if k >= 0 {
        w.ends.Mut(k).pipe = true
        w.ends.Mut(k + 1).pipe = true
    }
    return k
}

// FreePair frees both ends of the pair k no handle names yet.
FreePair(w: mut World, k: int) {
    FreeEnd(w.ends.Mut(k))
    FreeEnd(w.ends.Mut(k + 1))
}

// ConnHand records a connection handle for end i; 0 when the heap is
// exhausted.
ConnHand(w: mut World, i: int, write: bool): uint {
    r := NewHand(w, HConn, i, write)
    return if r.err == core.SysErr.None { Cast[uint](r.value) } else { 0 }
}

// DropConn drops the connection handle h this file just made.
DropConn(w: mut World, h: uint) {
    hi := FindHand(&*w, h, HConn)
    DropHand(w, hi)
}

// CallPipe is rt.PipeOpen under a Sim: value the read end's handle, os the
// write end's. Other (with os 0) when memory runs out, as natively.
CallPipe(io: Io, a: *core.IoCallArgs): core.IoResult {
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    w := WorldOf(&e)
    var r := Fail(core.SysErr.Other)
    k := PipePair(w)
    if k >= 0 {
        hw := ConnHand(w, k, true)
        hr := if hw != 0 { ConnHand(w, k + 1, false) } else { 0 }
        if hr != 0 {
            r = core.IoResult(value: Cast[i64](hr), err: core.SysErr.None, os: Cast[i64](hw))
        } else {
            if hw != 0 {
                DropConn(w, hw)
            }
            FreePair(w, k)
        }
    }
    Leave(&e)
    return r
}

// NewKid adds a child record; its index, or -1 when out of memory.
NewKid(w: mut World): int {
    k := Kid(used: true, h: 0, inp: -1, own: [-1; 3], loop: -1, pty: -1, done: false, code: 0)
    for i in 0..w.kids.Len() {
        if !w.kids.Get(i).used {
            w.kids.Set(i, k)
            return i
        }
    }
    if !w.kids.Push(k) {
        return -1
    }
    return w.kids.Len() - 1
}

// IsMissing: the program is the one name a Sim does not find.
IsMissing(args: []str): bool {
    return args.Len() > 0 && args[0] == "missing"
}

// CallSpawn is rt.Spawn under a Sim (the model above): value the child's
// process handle, os its pid; the IoSpawnReq's pipes the parent's ends.
// NotFound for the program "missing"; Other for a Pty that is not this
// run's or is closed, and (with os 0) when memory runs out. A cancelled
// scope ends it before it starts, as the native pool route's does.
CallSpawn(io: Io, a: *core.IoCallArgs): core.IoResult {
    if Halted(io) {
        return Fail(core.SysErr.Canceled)
    }
    unsafe "a is the caller's live record" {
        if (*a).w.a == 0 {
            return Fail(core.SysErr.Other)
        }
    }
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    w := WorldOf(&e)
    var r := Fail(core.SysErr.Other)
    unsafe "a is the caller's record; w.a the address of its IoSpawnReq and p its arguments, live until the call returns" {
        s := raw.FromAddr[core.IoSpawnReq](Trunc[uint]((*a).w.a))
        args := raw.Slice(raw.PtrCast[u8, str]((*a).p), (*a).pn)
        r = if IsMissing(args) { Fail(core.SysErr.NotFound) } else { SpawnKid(w, mut *s) }
    }
    WakeNet(w)
    Settle(io, mut e)
    Leave(&e)
    return r
}

// SpawnKid makes the child s asks for. Every end and handle it made is
// gone again when it fails.
SpawnKid(w: mut World, s: mut core.IoSpawnReq): core.IoResult {
    var pi := -1
    if s.pty != 0 {
        pi = Cast[int](s.pty) - 1
        if pi < 0 || pi >= w.ptys.Len() || w.ptys.Get(pi).a < 0 {
            return Fail(core.SysErr.Other)
        }
    }
    ki := NewKid(w)
    if ki < 0 {
        return Fail(core.SysErr.Other)
    }
    var hs: [3]uint = [0; 3]
    var pairs: [3]int = [-1; 3]
    var ok := true
    piped := pi < 0 && s.stdio[0] == core.IoStdPipe
    for i in 0..3 {
        if pi >= 0 || s.stdio[i] != core.IoStdPipe || i == 1 && piped {
            continue
        }
        pairs[i] = PipePair(w)
        if pairs[i] < 0 {
            ok = false
            break
        }
    }
    var kid := w.kids.Get(ki)
    if ok && piped {
        // stdin's end pairs[0] the parent writes; its peer is the child's,
        // or, with stdout a pipe too, the parent's stdout (cat's stream).
        hs[0] = ConnHand(w, pairs[0], true)
        ok = hs[0] != 0
        kid.inp = pairs[0]
        if ok && s.stdio[1] == core.IoStdPipe {
            hs[1] = ConnHand(w, pairs[0] + 1, false)
            ok = hs[1] != 0
            kid.loop = pairs[0]
        } else {
            kid.own[0] = pairs[0] + 1
        }
    }
    for i in 1..3 {
        if ok && pairs[i] >= 0 {
            // The child writes at pairs[i]; the parent reads its peer.
            hs[i] = ConnHand(w, pairs[i] + 1, false)
            ok = hs[i] != 0
            kid.own[i] = pairs[i]
        }
    }
    var ph: uint = 0
    if ok {
        hr := NewHand(w, HProc, ki, false)
        ok = hr.err == core.SysErr.None
        ph = if ok { Cast[uint](hr.value) } else { 0 }
    }
    if !ok {
        for i in 0..3 {
            if hs[i] != 0 {
                DropConn(w, hs[i])
            }
            if pairs[i] >= 0 {
                FreePair(w, pairs[i])
            }
        }
        w.kids.Mut(ki).used = false
        return Fail(core.SysErr.Other)
    }
    kid.h = ph
    if pi >= 0 {
        pa := w.ptys.Get(pi).a
        kid.pty = pi
        kid.inp = pa
        if w.ptys.Get(pi).live == 0 {
            // A Pty whose last child ended reads again for this one.
            w.ends.Mut(pa).wshut = false
        }
        w.ptys.Mut(pi).live += 1
    }
    w.kids.Set(ki, kid)
    if pi < 0 && !piped {
        // Its input is empty: cat ends at once.
        KidEnd(w, ki, 0)
    }
    s.pipes = hs
    s.pid = Cast[i64](ki) + 100
    s.job = 0
    return Ok(Cast[i64](ph))
}

// DropFlight drops end i's inbound bytes not yet arrived: the child that
// would have written them is gone.
DropFlight(w: mut World, i: int) {
    now := w.clock
    upto := w.ends.Get(i).head + Arrived(&w.ends.Get(i), now)
    e := w.ends.Mut(i)
    var k := 0
    for k < e.marks.Len() && e.marks.Get(k).at <= now {
        k += 1
    }
    e.marks.Truncate(k)
    e.data.Truncate(upto)
    if k > 0 {
        e.last = e.marks.Get(k - 1).at
    }
}

// EndStream ends the stream the parent writes at end i for its readers:
// they read what has arrived, then 0, and a write at i is Closed.
EndStream(w: mut World, i: int) {
    if w.ends.Get(i).used && w.ends.Get(i).open {
        w.ends.Mut(i).wshut = true
        DropFlight(w, Peer(i))
    }
}

// KidEnd ends child ki with code: its own pipe ends close, cat's stream
// and (with its last child) its Pty end, and a record with no handle left
// is freed. Waiters on the network are woken.
KidEnd(w: mut World, ki: int, code: i64) {
    var k := w.kids.Get(ki)
    k.done = true
    k.code = code
    k.inp = -1
    own := k.own
    k.own = [-1; 3]
    lp := k.loop
    k.loop = -1
    if k.h == 0 {
        k.used = false
    }
    w.kids.Set(ki, k)
    for i in 0..3 {
        if own[i] >= 0 {
            CloseEnd(w, own[i])
        }
    }
    if lp >= 0 {
        EndStream(w, lp)
    }
    if k.pty >= 0 {
        w.ptys.Mut(k.pty).live -= 1
        pa := w.ptys.Get(k.pty).a
        if w.ptys.Get(k.pty).live == 0 && pa >= 0 {
            EndStream(w, pa)
        }
    }
    WakeNet(w)
}

// KidsInputClosed ends every running child whose input is end i, which
// is closing (net.mk's CloseEnd), and marks a Pty whose writer it is
// closed.
KidsInputClosed(w: mut World, i: int) {
    for ki in 0..w.kids.Len() {
        k := w.kids.Get(ki)
        if k.used && !k.done && k.inp == i {
            KidEnd(w, ki, 0)
        }
    }
    for pi in 0..w.ptys.Len() {
        if w.ptys.Get(pi).a == i {
            w.ptys.Mut(pi).a = -1
        }
    }
}

// KillCode is how the Sim's target reports a child ended by signal sig.
KillCode(sig: u32): i64 {
    return if Target.os == core.Os.Windows { 1 } else { -Cast[i64](sig) }
}

// KidSignal sends sig (Linux's numbers) to the child of process handle h:
// 1, 2, 9 and 15 end a running child (on Windows only 9; any other is
// Unsupported); a child already ended is success; Closed when h is not a
// child of this run. Wakes the network's waiters.
KidSignal(w: mut World, h: uint, sig: u32): core.IoResult {
    hi := FindHand(&*w, h, HProc)
    if hi < 0 {
        return Fail(core.SysErr.Closed)
    }
    ki := w.handles.Get(hi).obj
    if Target.os == core.Os.Windows && sig != 9 {
        return Fail(core.SysErr.Unsupported)
    }
    if sig != 1 && sig != 2 && sig != 9 && sig != 15 {
        return Fail(core.SysErr.Unsupported)
    }
    if !w.kids.Get(ki).done {
        KidEnd(w, ki, KillCode(sig))
    }
    return Ok(0)
}

// CallSignal is rt.ChildSignal given a Sim's Io: not a cancellation
// point, as natively.
CallSignal(io: Io, h: uint, sig: u32): core.IoResult {
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    r := KidSignal(WorldOf(&e), h, sig)
    Settle(io, mut e)
    Leave(&e)
    return r
}

// SimForeignSignal is SimOps.signal: rt.ChildSignal with a Sim's child
// and any Io (std.proc's Kill passes the native one). As SimForeignClose,
// a handle of an ended run or a freed Sim is Closed.
SimForeignSignal(h: uint, sig: u32): core.IoResult {
    var os: uint = 0
    var tok: u64 = 0
    unsafe "rt calls this with h's live record" {
        r := rt.Rec(h)
        os = (*r).os
        tok = Cast[u64]((*r).pos)
    }
    slot := Cast[int](os >> 58)
    if slot >= SlotCount || tok == 0 {
        return Fail(core.SysErr.Closed)
    }
    var e := Enter(IoFor(slot, tok))
    if !e.ok {
        return Fail(core.SysErr.Closed)
    }
    r := KidSignal(WorldOf(&e), h, sig)
    Settle(IoFor(slot, tok), mut e)
    Leave(&e)
    return r
}

// KidClose drops the process handle h (a Wait's or a Detach's close): a
// child already ended is forgotten; a running one runs on with its
// pipes. False when h is no child of this run.
KidClose(w: mut World, h: uint): bool {
    hi := FindHand(&*w, h, HProc)
    if hi < 0 {
        return false
    }
    ki := w.handles.Get(hi).obj
    DropHand(w, hi)
    k := w.kids.Mut(ki)
    k.h = 0
    if k.done {
        k.used = false
    }
    return true
}

// KidTry is a wait's answer for the process handle h: its code once the
// child ended, Canceled while it runs, Closed when h is no child of this
// run.
KidTry(w: &World, h: uint): core.IoResult {
    hi := FindHand(&*w, h, HProc)
    if hi < 0 {
        return Fail(core.SysErr.Closed)
    }
    k := w.kids.Get(w.handles.Get(hi).obj)
    return if k.done { Ok(k.code) } else { Fail(core.SysErr.Canceled) }
}

// ProcWait is SimTable's procwait: it waits for the child h to end, as
// SimAccept waits for a dial (the baton's holder on the network, main
// natively while the run has tasks); a child no event can end in a
// one-task run is a deadlock, Canceled. A cancelled scope ends the wait
// with Canceled and the child running, so the wait can be made again.
ProcWait(io: Io, h: uint): core.IoResult {
    if Halted(io) {
        return Fail(core.SysErr.Canceled)
    }
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    var r := KidTry(&*WorldOf(&e), h)
    for r.err == core.SysErr.Canceled && IsHolder(&*WorldOf(&e)) {
        if BlockAs(io, mut e, BNet, 0, -1) != core.SysErr.None {
            if e.ok {
                Leave(&e)
            }
            return Dead()
        }
        r = KidTry(&*WorldOf(&e), h)
    }
    for r.err == core.SysErr.Canceled && !IsHolder(&*WorldOf(&e)) && MainWaits(&*WorldOf(&e)) {
        st := MainWait(io, mut e, -1)
        if !e.ok {
            return Dead()
        }
        if Halted(io) {
            Leave(&e)
            return Fail(core.SysErr.Canceled)
        }
        r = KidTry(&*WorldOf(&e), h)
        if st != core.SysErr.None {
            break
        }
    }
    if r.err == core.SysErr.Canceled {
        MainAloneCut(WorldOf(&e))
    }
    Leave(&e)
    return r
}

// PipeSend is NetSend for a pipe's end i: Closed once its reader is gone
// or its stream ended (EPIPE natively; never a reset); at most the room
// left of PipeCap, and the Full marker when there is none.
PipeSend(w: mut World, i: int, data: []u8): core.IoResult {
    p := Peer(i)
    if w.ends.Get(i).reset || w.ends.Get(i).wshut || !w.ends.Get(p).open {
        return Fail(core.SysErr.Closed)
    }
    if data.Len() == 0 {
        return Ok(0)
    }
    room := PipeCap - (w.ends.Get(p).data.Len() - w.ends.Get(p).head)
    if room <= 0 {
        return core.IoResult(value: 0, err: core.SysErr.Canceled, os: Full)
    }
    n := if data.Len() > room { room } else { data.Len() }
    return Deliver(w, p, data[0..n])
}

IsFull(r: core.IoResult): bool {
    return r.err == core.SysErr.Canceled && r.os == Full
}

// PipeSendLoop writes data to the pipe end h for the entry e holds,
// waiting while the pipe is full as RecvLoop waits for bytes; a cancel or
// a deadline ends the wait with Canceled and nothing written. It
// releases e.
PipeSendLoop(io: Io, e: mut Entry, h: uint, data: []u8): core.IoResult {
    var r := NetSend(WorldOf(&*e), h, data)
    for IsFull(r) && IsHolder(&*WorldOf(&*e)) {
        if BlockAs(io, e, BNet, 0, -1) != core.SysErr.None {
            if e.ok {
                Leave(&*e)
            }
            return Dead()
        }
        r = NetSend(WorldOf(&*e), h, data)
    }
    for IsFull(r) && !IsHolder(&*WorldOf(&*e)) && MainWaits(&*WorldOf(&*e)) {
        st := MainWait(io, e, -1)
        if !e.ok {
            return Dead()
        }
        if Halted(io) {
            Leave(&*e)
            return Fail(core.SysErr.Canceled)
        }
        r = NetSend(WorldOf(&*e), h, data)
        if st != core.SysErr.None {
            break
        }
    }
    if IsFull(r) {
        // Nothing can drain the pipe: a deadlock of a one-task run.
        r = Fail(core.SysErr.Canceled)
        MainAloneCut(WorldOf(&*e))
    }
    w := WorldOf(&*e)
    WakeNet(w)
    Settle(io, e)
    Leave(&*e)
    return r
}

// IsPipeHand: h is a connection handle of this run on a pipe's end.
IsPipeHand(w: &World, h: uint): bool {
    hi := FindHand(&*w, h, HConn)
    return hi >= 0 && w.ends.Get(w.handles.Get(hi).obj).pipe
}

// PipeTook wakes a writer waiting for room after a read of a pipe took
// bytes.
PipeTook(io: Io) {
    var e := Enter(io)
    if !e.ok {
        return
    }
    WakeNet(WorldOf(&e))
    Settle(io, mut e)
    Leave(&e)
}

// CallPtyOpen is rt.PtyOpen under a Sim: value the term, w.a the reader's
// handle, w.b the writer's. A size outside 1 to 32,767 is Other, as
// natively; Other (os 0) when memory runs out.
CallPtyOpen(io: Io, a: *core.IoCallArgs): core.IoResult {
    var cols: i64 = 0
    var rows: i64 = 0
    unsafe "a is the caller's live record" {
        cols = (*a).w.a
        rows = (*a).w.b
    }
    if cols < 1 || cols > 32767 || rows < 1 || rows > 32767 {
        return Fail(core.SysErr.Other)
    }
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    w := WorldOf(&e)
    var r := Fail(core.SysErr.Other)
    k := PipePair(w)
    if k >= 0 {
        hw := ConnHand(w, k, true)
        hr := if hw != 0 { ConnHand(w, k + 1, false) } else { 0 }
        pt := SimPty(a: k, live: 0, cols: Trunc[int](cols), rows: Trunc[int](rows))
        if hr != 0 && w.ptys.Push(pt) {
            unsafe "a is the caller's live record" {
                (*a).w.a = Cast[i64](hr)
                (*a).w.b = Cast[i64](hw)
            }
            r = Ok(Cast[i64](w.ptys.Len()))
        } else {
            if hr != 0 {
                DropConn(w, hr)
            }
            if hw != 0 {
                DropConn(w, hw)
            }
            FreePair(w, k)
        }
    }
    Leave(&e)
    return r
}

// CallPtyResize is rt.PtyResize under a Sim: the size is recorded (its
// child, cat, reads none). Other for a size outside 1 to 32,767 or a term
// that is not this run's.
CallPtyResize(io: Io, term: uint, cols: i64, rows: i64): core.IoResult {
    if cols < 1 || cols > 32767 || rows < 1 || rows > 32767 {
        return Fail(core.SysErr.Other)
    }
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    w := WorldOf(&e)
    pi := Cast[int](term) - 1
    var r := Fail(core.SysErr.Other)
    if pi >= 0 && pi < w.ptys.Len() {
        pt := w.ptys.Mut(pi)
        pt.cols = Trunc[int](cols)
        pt.rows = Trunc[int](rows)
        r = Ok(0)
    }
    Leave(&e)
    return r
}
