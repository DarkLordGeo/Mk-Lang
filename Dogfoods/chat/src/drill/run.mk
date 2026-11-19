// Why: The `drill` command reports, wait by wait, that a cancelled scope wakes
// its tasks.
//
// What: Run, DrillError, and the reports Orders and Counters.
//
// How: For each wait in drill.mk it starts the task in a scope, cancels the
// scope and prints the outcome; then it drives the hub's atomic counters and
// prints the memory orders.

import std.atomic
import std.fs
import std.net
import std.sync
import src.hub

/// The word for an outcome.
Word(o: Outcome): str {
    return match o {
        .Canceled { "canceled" }
        .Done { "done" }
        .Skipped { "skipped" }
    }
}

Show(w: mut Writer, point: str, o: Outcome): WriteError!void {
    try Print(w, "{point:<28} {o}\n", (point, o: Word(o)))
}

/// The errors a drill run can stop on.
export type DrillError = net.Error | OutOfMemory | WriteError

/// Cancels a scope while a task waits at each kind of wait the server has,
/// and reports how each wait ended; every one must say `canceled`. Then
/// checks the server's counters and memory orders. Returns 0 when every
/// wait was cancelled, else 1.
export Run(h: &Heap, io: Io, w: mut Writer): DrillError!int {
    var bad := 0
    // Semaphore.Acquire with no permit, as the accept loop waits for a slot
    sem := sync.Semaphore.New(0)
    var o1 := Outcome.Skipped
    scope s := io.Scope() {
        s.go AcquireOne(io, &sem, mut o1)
        s.Cancel()
    }
    try Show(w, "Semaphore.Acquire", o1)
    // Mutex.Lock while another holds it, as for the message of the day
    m := Mutex.New(0)
    var o2 := Outcome.Skipped
    g := try m.Lock(io)
    scope s := io.Scope() {
        s.go LockMutex(io, &m, mut o2)
        s.Cancel()
    }
    g.Unlock()
    try Show(w, "Mutex.Lock", o2)
    // RwLock: a reader waits for a writer, a writer for a reader
    rw := sync.RwLock.New(0)
    var o3 := Outcome.Skipped
    gw := try rw.Lock(io)
    scope s := io.Scope() {
        s.go LockRead(io, &rw, mut o3)
        s.Cancel()
    }
    gw.Unlock()
    try Show(w, "RwLock.LockRead", o3)
    var o4 := Outcome.Skipped
    gr := try rw.LockRead(io)
    scope s := io.Scope() {
        s.go LockWrite(io, &rw, mut o4)
        s.Cancel()
    }
    gr.Unlock()
    try Show(w, "RwLock.Lock", o4)
    // Notify.Wait with no permit, as the history writer waits
    n := sync.Notify.New()
    var o5 := Outcome.Skipped
    scope s := io.Scope() {
        s.go WaitNotify(io, &n, mut o5)
        s.Cancel()
    }
    try Show(w, "Notify.Wait", o5)
    // Barrier.Wait one arrival short, as the bots wait for each other
    b := sync.Barrier.New(2)
    var o6 := Outcome.Skipped
    scope s := io.Scope() {
        s.go WaitBarrier(io, &b, mut o6)
        s.Cancel()
    }
    try Show(w, "Barrier.Wait", o6)
    // channels: an empty Recv, a full Send, a Select over two empty ones
    a := h.Alloc()
    empty := try Chan[int].New(a, 1)
    full := try Chan[int].New(a, 1)
    full.Send(io, 1) else {}
    var o7 := Outcome.Skipped
    var o8 := Outcome.Skipped
    var o9 := Outcome.Skipped
    scope s := io.Scope() {
        s.go RecvOne(io, &empty, mut o7)
        s.go SendOne(io, &full, mut o8)
        s.go SelectTwo(io, &empty, &empty, mut o9)
        s.Cancel()
    }
    full.Free(a)
    empty.FreeEmpty(a)
    try Show(w, "Chan.Recv", o7)
    try Show(w, "Chan.Send", o8)
    try Show(w, "sync.Select", o9)
    // Io.Sleep, as the rate refill waits
    var o10 := Outcome.Skipped
    scope s := io.Scope() {
        s.go Sleep(io, mut o10)
        s.Cancel()
    }
    try Show(w, "Io.Sleep", o10)
    // the network: an Accept with no client, a Read with no data
    l := try net.Listen(io, "127.0.0.1:0")
    defer l.Close()
    var o11 := Outcome.Skipped
    scope s := io.Scope() {
        s.go AcceptOne(io, &l, mut o11)
        s.Cancel()
    }
    try Show(w, "Listener.Accept", o11)
    var arena := try Arena.New(h)
    defer arena.Free()
    peer := try net.Dial(io, try arena.Fmt("127.0.0.1:{p}", (p: l.Port())))
    defer peer.Close()
    served := try l.Accept(io)
    var o12 := Outcome.Skipped
    scope s := io.Scope() {
        s.go ReadOne(io, served, mut o12)
        s.Cancel()
    }
    try Show(w, "Stream.Read", o12)
    // the history writer, cancelled while it waits to be woken
    hist := hub.History.New()
    var log: ?fs.File = none
    var failed: ?hub.LogError = none
    var written := 0
    scope s := io.Scope() {
        s.go hub.HistoryWriter(io, &hist, mut log, mut failed, mut written)
        s.Cancel()
    }
    if f := log {
        f.Abandon()
    }
    try Print(w, "{p:<28} stopped, wrote {written}\n", (p: "history writer", written))
    for o in [o1, o2, o3, o4, o5, o6, o7, o8, o9, o10, o11, o12] {
        if o != Outcome.Canceled {
            bad += 1
        }
    }
    try Orders(w)
    try Counters(w)
    try Print(w, "drill: {n} of 12 waits cancelled\n", (n: 12 - bad))
    return if bad == 0 { 0 } else { 1 }
}

/// The memory orders and what each may do.
Orders(w: mut Writer): WriteError!void {
    for u in hub.Orders() {
        try Print(w, "order {u.name:<8} load={u.load} store={u.store} fence={u.fence}\n", (u))
    }
}

/// The server's counters, driven through each of their operations.
Counters(w: mut Writer): WriteError!void {
    st := hub.Stats.New()
    id1 := st.Connected()
    id2 := st.Connected()
    st.Disconnected()
    st.Wrote(3, 120)
    st.Wrote(1, 40)
    t := st.Snapshot()
    reset := st.ResetTraffic()
    st.Flag(hub.FlagMotdSet, true)
    st.Flag(hub.FlagBotsReady, true)
    st.Flag(hub.FlagBotsReady, false)
    on := st.Toggle(hub.FlagDraining)
    off := st.Toggle(hub.FlagDraining)
    first := st.BeginDrain()
    again := st.BeginDrain()
    flags := st.FlagWord()
    hub.StoreWith(&st.peak, 9, atomic.Release)
    hub.StoreWith(&st.peak, 10, atomic.SeqCst)
    hub.StoreWith(&st.peak, 11, atomic.Relaxed)
    p1 := hub.LoadWith(&st.peak, atomic.Acquire)
    p2 := hub.LoadWith(&st.peak, atomic.Relaxed)
    p3 := hub.LoadWith(&st.peak, atomic.SeqCst)
    conns := st.Connections()
    st.Renumber()
    after := st.Connections()
    try Print(w, "stats ids={id1},{id2} conns={conns} renumbered={after} frames={t.frames} bytes={t.bytes} reset={reset.frames}\n", (id1, id2, conns, after, t, reset))
    try Print(w, "stats toggles={on},{off} drain={first},{again} flags={flags} peak={p1},{p2},{p3}\n", (on, off, first, again, flags, p1, p2, p3))
    final := st.Into()
    try Print(w, "stats final frames={final.frames}\n", (final))
}
