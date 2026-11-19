// cancelpoint, Mk: n uncontended Mutex.Lock and Unlock pairs, each Lock a
// cancellation point (std/core/sync_mutex.mk CancelPoint), the guarded
// value updated as x = x * 31 + i (wrapping). With expired 1 an unrelated
// scope stays expired for the whole loop: a sibling task opens a scope,
// cancels it by a zero deadline (Scope.CancelAfter), says so, and waits on
// a NoCancel Io until the loop is done; the loop's scope chain holds no
// cancelled scope, so each Lock must still succeed. Prints n, x and the
// failed calls. Usage: cancelpoint <n> <expired>.

import std.time

Holder(io: Io, ready: &Chan[u64], gate: &Chan[u64]) {
    scope e := io.Scope() {
        e.CancelAfter(time.Millis(0))
        q := io.NoCancel()
        v: u64 = if e.Expired() { 1 } else { 0 }
        ready.Send(q, v) else {
            return
        }
        _ = gate.Recv(q) else {
            return
        }
    }
}

export Main(sys: &System): ParseError|WriteError|OutOfMemory!void {
    n := Cast[int](try sys.args[1].ParseInt())
    expired := try sys.args[2].ParseInt()
    a := sys.heap.Alloc()
    ready := try Chan[u64].New(a, 1)
    gate := Chan[u64].New(a, 1) else e {
        ready.FreeEmpty(a)
        fail e
    }
    var m := Mutex[u64].New(0)
    var fails := 0
    scope s := sys.io.Scope() {
        if expired == 1 {
            s.go Holder(sys.io, &ready, &gate)
            // The holder's scope is expired before the first Lock.
            v := ready.Recv(sys.io) else {
                fails += 1
                0
            }
            if v != 1 {
                fails += 1
            }
        }
        for i in 0..n {
            var g := m.Lock(sys.io) else {
                fails += 1
                break
            }
            x := *g.Ref()
            g.Store(x *% 31 +% Cast[u64](i))
            g.Unlock()
        }
        if expired == 1 {
            gate.Send(sys.io, 0) else {
                fails += 1
            }
        }
    }
    ready.FreeEmpty(a)
    gate.FreeEmpty(a)
    x := m.Into()
    var out := sys.Stdout()
    try Print(mut out, "{n} {x} {fails}\n", (n, x, fails))
}
