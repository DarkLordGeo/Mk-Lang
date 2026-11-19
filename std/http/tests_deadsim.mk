// Why: the client's NoCancel locks panic when they fail, and a dead Sim run
// (restarted or crashed) is not a scope cancellation, so the masked
// cancellation check must go on in one.
// What: Fetch, Do and Exchange.Abandon under a restarted Sim's Io, and a
// crash sweep of Fetch (pool.mk, exchange.mk, link.mk, wire.mk's locks).
// How: each case must return with an error or a result, never a panic. Run
// by mk test --std=std.http.

import std.net
import std.raw
import std.sim
import std.url

// DsFetchRestarted: Fetch with the Io of a Sim's earlier run (after Restart):
// the status, or CtCode of the error.
DsFetchRestarted(sys: &System): int {
    var s := sim.Sim.New(sys.arena, 5) else {
        return -1000
    }
    old := s.Io()
    _ = s.Restart()
    var c := Client.New(sys.heap, DefaultClientLimits)
    code := CtGet(&c, sys.heap, old, "http://127.0.0.1:7499/ok", "ok")
    c.Close()
    s.Free()
    return code
}

// DsDoThenAbandon: Do, then the run restarts, then the Exchange is abandoned.
DsDoThenAbandon(c: &Client, h: &Heap, io: Io, s: mut sim.Sim): int {
    var ar := Arena.New(h) else {
        return -100
    }
    defer ar.Free()
    pu := url.Parse(&ar, "http://127.0.0.1:7496/ok", none) else {
        return -101
    }
    hs := List[Header].New(&ar).Done()
    req := ClientRequest(method: "GET", url: &pu, headers: hs[..], body: "".AsBytes())
    var sub := ar.Sub() else {
        return -102
    }
    defer sub.Free()
    ex := c.Do(&sub, io, &req) else e {
        return CtCode(e)
    }
    _ = s.Restart()
    ex.Abandon()
    return 1
}

// DsAbandonRestarted: DsDoThenAbandon against a server in the same Sim.
DsAbandonRestarted(sys: &System): int {
    var s := sim.Sim.New(sys.arena, 9) else {
        return -1000
    }
    io := s.Io()
    l := net.Listen(io, "127.0.0.1:7496") else {
        s.Free()
        return -1001
    }
    n := raw.Atomic[u32].New(0)
    var c := Client.New(sys.heap, DefaultClientLimits)
    var res := 0
    scope srv := io.Scope() {
        srv.go CtServe(io, &l, &n)
        res = DsDoThenAbandon(&c, sys.heap, io, mut s)
        srv.Cancel()
    }
    c.Close()
    l.Close()
    s.Free()
    return res
}

// DsFetchUntilFail: fetch /ok until a fetch fails, under a crash point at a
// seeded op. i * 1000 - last code: the fetches that passed and the failing
// code.
DsFetchUntilFail(io: Io, h: &Heap): int {
    l := net.Listen(io, "127.0.0.1:7497") else {
        return -3000
    }
    n := raw.Atomic[u32].New(0)
    var c := Client.New(h, DefaultClientLimits)
    var last := 0
    var i := 0
    scope srv := io.Scope() {
        srv.go CtServe(io, &l, &n)
        for i < 500 {
            last = CtGet(&c, h, io, "http://127.0.0.1:7497/ok", "ok")
            if last != 200 {
                break
            }
            i += 1
        }
        srv.Cancel()
    }
    c.Close()
    l.Close()
    return i * 1000 - last
}

// DsFetchUntilFailTask runs DsFetchUntilFail in a task and stores its result.
DsFetchUntilFailTask(io: Io, h: &Heap, out: &raw.Atomic[i64]) {
    out.Store(i64(DsFetchUntilFail(io, h)))
}

// DsCrashSweep: DsFetchUntilFail in a Sim of the given seed that crashes
// after a random number of operations.
DsCrashSweep(sys: &System, seed: u64): int {
    var s := sim.Sim.New(sys.arena, seed) else {
        return -1000
    }
    s.CrashAfterRandomOps()
    io := s.Io()
    out := raw.Atomic[i64].New(-2000)
    scope r := io.Scope() {
        r.go DsFetchUntilFailTask(io, sys.heap, &out)
    }
    s.Free()
    return int(out.Load())
}

test "Fetch with a restarted Sim run's Io fails with an error, not a panic" (sys: &System) {
    // The expected answer: -7, CtCode's code for the error.
    Assert(DsFetchRestarted(sys) == -7)
}

test "Exchange.Abandon after the Sim run restarts returns" (sys: &System) {
    Assert(DsAbandonRestarted(sys) == 1)
}

test "a crash sweep of Fetch ends each seed's run with an error, not a panic" (sys: &System) {
    // The expected results for seeds 1 to 8.
    want: [8]int = [499800, 20007, 499800, 24007, 16007, 499800, 499800, 499800]
    for seed in 1..9 {
        Assert(DsCrashSweep(sys, Cast[u64](seed)) == want[seed - 1])
    }
}
