// timers, Mk, idiomatic std: the task of ../README.md with std's timers.
// Mode sleep: per round one scope of n tasks, task i sleeping 1 to 50 ms
// with Io.Sleep, then adding i to one atomic.AtomicInt if time.Now shows
// it slept at least half its time; the scope's exit joins them. Mode
// deadline: n scopes one after another, each arming a deadline with
// Scope.CancelAfter and exiting at once: 60 s, which the exit disarms, or
// 0 s on every 1000th, which expires at once; prints the disarmed plus 3
// times the expired (Scope.Expired). Tasks run on MK_CARRIERS
// carriers (tools/bench sets the row's thread count). Usage: timers sleep
// <n> <rounds> <seed> | timers deadline <n>. Prints the checksum.

import std.atomic
import std.time

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Sleeper(io: Io, sum: &atomic.AtomicInt, i: i64, ms: i64) {
    t0 := time.Now(io)
    io.Sleep(time.Millis(ms)) else {
        return
    }
    if time.Now(io).Since(t0).Nanos() >= ms * 500000 {
        _ = sum.Add(i)
    }
}

Sleeps(io: Io, n: i64, rounds: i64, seed: u64): i64 {
    var st := seed
    var total: i64 = 0
    for _ in 0..rounds {
        sum := atomic.AtomicInt.New(0)
        scope s := io.Scope() {
            for i in 0..n {
                st = st +% 0x9e3779b97f4a7c15
                ms := Cast[i64](Mix(st) % 50) + 1
                s.go Sleeper(io, &sum, i, ms)
            }
        }
        total += sum.Load()
    }
    return total
}

Deadlines(io: Io, n: i64): i64 {
    var disarmed: i64 = 0
    var fired: i64 = 0
    for i in 0..n {
        d := if i % 1000 == 999 { time.Seconds(0) } else { time.Seconds(60) }
        var expired := false
        scope s := io.Scope() {
            s.CancelAfter(d)
            expired = s.Expired()
        }
        if expired {
            fired += 1
        } else {
            disarmed += 1
        }
    }
    return disarmed + 3 * fired
}

export Main(sys: &System): ParseError|WriteError!void {
    mode := sys.args[1]
    n := try sys.args[2].ParseInt()
    var total: i64 = 0
    if mode == "sleep" {
        rounds := try sys.args[3].ParseInt()
        seed := Cast[u64](try sys.args[4].ParseInt())
        total = Sleeps(sys.io, n, rounds, seed)
    } else {
        total = Deadlines(sys.io, n)
    }
    try sys.Print("{total}\n", (total))
}
