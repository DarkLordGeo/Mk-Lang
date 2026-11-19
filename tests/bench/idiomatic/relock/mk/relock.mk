// relock, Mk, idiomatic std (the relock measurement of
// ../README.md): a waiter's File.Lock against a peer that holds
// the same file's lock on a handle of its own. Each round the peer takes
// the lock, a task of its own holds it for hold ms, and then either
// releases it once (relock 0: the hand-off, whose time is the round's
// time less the hold) or releases and retakes it at once every hold ms
// (relock 1: the peer relocks, and the waiter must still win). The main
// task's Lock runs in a scope with a one-second deadline; a round it wins
// in time counts. Prints the rounds and the rounds won, so a waiter that
// loses a round fails the row's checksum. Usage: relock <rounds> <hold ms> <relock>.

import std.atomic
import std.fs
import std.time

// Peer holds p's lock, which it already has, for hold ms, and then releases
// it; with relock it retakes the lock at once and repeats, until stop is set
// or two seconds pass. It closes p.
Peer(io: Io, p: fs.File, hold: i64, relock: bool, stop: &atomic.Atomic[u32]) {
    for i in 0..400 {
        io.Sleep(time.Millis(hold)) else {}
        p.Unlock(io) else {}
        if !relock || stop.Load(atomic.SeqCst) == 1 || i * Cast[int](hold) >= 2000 {
            break
        }
        var again := true
        p.Lock(io, fs.LockMode.Exclusive) else {
            again = false
        }
        if !again || stop.Load(atomic.SeqCst) == 1 {
            p.Unlock(io) else {}
            break
        }
    }
    p.Abandon()
}

// Round runs one round and returns whether the waiter took the lock within
// one second.
Round(io: Io, path: str, hold: i64, relock: bool): fs.Error!bool {
    var w := try fs.OpenWith(io, path, fs.ReadWrite)
    onerr w.Abandon()
    var p := try fs.OpenWith(io, path, fs.ReadWrite)
    p.Lock(io, fs.LockMode.Exclusive) else e {
        p.Abandon()
        fail e
    }
    stop := atomic.Atomic[u32].New(0)
    var won := false
    scope s := io.Scope() {
        s.go Peer(io, p, hold, relock, &stop)
        scope d := io.Scope() {
            d.CancelAfter(time.Seconds(1))
            var got := true
            w.Lock(io, fs.LockMode.Exclusive) else {
                got = false
            }
            won = got
        }
        stop.Store(1, atomic.SeqCst)
        if won {
            w.Unlock(io) else {}
        }
    }
    try w.Close(io)
    return won
}

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError!void {
    io := sys.io
    rounds := Cast[int](try sys.args[1].ParseInt())
    hold := try sys.args[2].ParseInt()
    relock := (try sys.args[3].ParseInt()) == 1
    tmp := try fs.TempDir(sys.arena, io)
    path := try sys.arena.Fmt("{tmp}/mkbench-relock-mk.lock", (tmp))
    var f := try fs.Create(io, path)
    onerr f.Abandon()
    try f.Close(io)
    var won := 0
    for _ in 0..rounds {
        if try Round(io, path, hold, relock) {
            won += 1
        }
    }
    try fs.Remove(io, path)
    try sys.Print("{rounds} {won}\n", (rounds, won))
}
