// C calls from tasks (the budget "C call from a task" and the runtime's rule that an extern runs
// on its carrier's system stack). See README.md.
//
// Part 1, stacks: an extern declared outside std runs on its carrier's 8 MiB system stack
// (the runtime's rule for externs), so a C function that uses 512 KiB or 7 MiB of stack works from
// any task. RefStack(kib) touches one byte per 4 KiB page and returns the sum of the bytes it
// wrote: 8128 for 512 KiB, 228480 for 7,168 KiB. Main calls both; 64 tasks each yield and then
// call the 512 KiB one (with the default carrier count the calls spread over carriers); one more
// task calls the 7 MiB one, which no 1 MiB task slot could hold. The line is
// `stacks <main 512> <tasks 512, summed> <main 7M> <task 7M>`, 8128 520192 228480 228480.
//
// Part 2, timing: 11 rounds, each 10,000,000 calls of RefAdd from Main's task through the extern
// (one stack switch each), then 10,000,000 direct C-to-C calls of the same
// noinline RefAdd inside RefAddLoop. The difference per call is the extern's cost over a direct
// call (budget: at most 5 ns). Both sums are 35000000.

import std.time
import std.atomic
import ref

const Calls: int = 10_000_000

MkLoop(n: int): i64 {
    var s: i64 = 0
    for i in 0..n {
        s = ref.Add(s, i64(i) & 7)
    }
    return s
}

Deep(io: Io, total: &atomic.AtomicInt) {
    io.Yield() else {
        return
    }
    _ = total.Add(ref.Stack(512))
}

Deep7M(out: mut i64) {
    *out = ref.Stack(7168)
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    main512 := ref.Stack(512)
    main7m := ref.Stack(7168)
    total := atomic.AtomicInt.New(0)
    var task7m: i64 = 0
    scope s := sys.io.Scope() {
        for _ in 0..64 {
            s.go Deep(sys.io, &total)
        }
        s.go Deep7M(mut task7m)
    }
    try Print(mut out, "stacks {a} {b} {c} {d}\n", (a: main512, b: total.Load(), c: main7m, d: task7m))
    var round := 0
    for round < 11 {
        round += 1
        t0 := time.Now(sys.io)
        a := MkLoop(Calls)
        t1 := time.Now(sys.io)
        b := ref.AddLoop(i64(Calls))
        t2 := time.Now(sys.io)
        try Print(mut out, "round {round} mk {mk_ns} ref {ref_ns} checksum {a} {b}\n", (round, mk_ns: t1.Since(t0).Nanos(), ref_ns: t2.Since(t1).Nanos(), a, b))
    }
}
