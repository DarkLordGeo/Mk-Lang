// selpool, Mk: a worker pool over sync.Select.
// w worker tasks each loop on Select(io, (jobs: &jobs, quit: &quit)) and,
// on index 0, jobs.Recv; one producer sends 0..n-1 on jobs (capacity c)
// and closes jobs; a worker ends when its Select or Recv finds jobs closed
// and drained; quit is closed after the scope has joined every worker, as
// in the Go cell, so neither cell ever takes quit (Go's
// select picks at random among ready cases, so a quit closed beside jobs
// would drop buffered values there). Prints the sum of what the workers received,
// n * (n - 1) / 2. A Select returns an index and takes no value,
// so a worker whose Recv finds jobs empty again parks as a receiver; the
// Close of jobs ends those. Usage: selpool <w> <n> <c>.

import std.atomic
import std.sync

type PoolError = Canceled | Closed

Worker(io: Io, jobs: &Chan[i64], quit: &Chan[i64], total: &atomic.AtomicInt) {
    var k: i64 = 0
    for {
        i := sync.Select(io, (jobs: jobs, quit: quit)) else {
            break
        }
        if i != 0 {
            break
        }
        x := jobs.Recv(io) else {
            break
        }
        k += x
    }
    _ = total.Add(k)
}

export Main(sys: &System): PoolError|OutOfMemory|ParseError|WriteError!void {
    w := try sys.args[1].ParseInt()
    n := try sys.args[2].ParseInt()
    cap := try sys.args[3].ParseInt()
    jobs := try Chan[i64].New(sys.arena.Alloc(), Trunc[int](cap))
    quit := try Chan[i64].New(sys.arena.Alloc(), 0)
    total := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for _ in 0..w {
            s.go Worker(sys.io, &jobs, &quit, &total)
        }
        for i in 0..n {
            try jobs.Send(sys.io, i)
        }
        jobs.Close()
    }
    quit.Close()
    var out := sys.Stdout()
    try Print(mut out, "{t}\n", (t: total.Load()))
}
