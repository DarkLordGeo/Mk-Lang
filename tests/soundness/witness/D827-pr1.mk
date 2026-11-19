// verdict: run 20
// want: 20 runs: 0 failed, 0 mixed, 0 wrong length, 0 with Counting not 0 after free
// note: the proc.Run hole: proc.Run over an Arena.NewIn arena over a Counting over a Fixed, with no s.go or unsafe in the program. If Run's two pumps raced on the caller's Counting, some rounds would return a stdout holding the stderr stream's bytes; the run must print the `want` line. Judged by std's TestWitnesses, which runs it with the argument 20
import std.mem
import std.proc

// pr1: std's own proc.Run over an Arena.NewIn arena.
// No s.go, no unsafe and no std.raw in this program. Run makes two Subs of
// the caller's arena and spawns two Pump tasks on them (std/proc/run.mk's
// Run), and each Sub's chunks come from the caller's
// Counting, which "one task at a time reaches" (std/mem/counting.mk).
// The child (this program with the argument "child") writes only 'O' to
// stdout and only 'E' to stderr, interleaved, 1 MiB each, so both pumps
// grow their segments at the same time.
//
// Canaries, any of which shows the two pumps raced on one task-confined
// allocator: a byte other than 'O' or '\n' in stdout (or 'E' in stderr),
// a stream length other than 1 MiB (two segments handed the same bytes by
// the Fixed), Counting.Live() or Bytes() not 0 after the arena is freed
// (lost updates on the Counting's plain cells), or a crash.
export Main(sys: &System): ParseError|WriteError!void {
    a1 := sys.args[1]
    var out := sys.Stdout()
    var err := sys.Stderr()
    if a1.Len() == 5 && a1.AsBytes()[0] == 99 {
        for _ in 0..16384 {
            try out.Str("OOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOO\n")
            try err.Str("EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\n")
        }
        return
    }
    n := Cast[int](try a1.ParseInt())
    args := [sys.args[0], "child"]
    buf := sys.arena.Slice[u8](67108864) else {
        try Print(mut out, "no buffer\n")
        return
    }
    var fails := 0
    var mixed := 0
    var short := 0
    var leaked := 0
    for i in 0..n {
        fixed := mem.Fixed.New(buf)
        counting := mem.Counting.New(fixed.Alloc())
        var arena := Arena.NewIn(counting.Alloc()) else {
            fails += 1
            continue
        }
        r := proc.Run(&arena, sys.io, args[0..], b"", 67108864) else {
            fails += 1
            arena.Free()
            continue
        }
        var bad_o := 0
        for b in r.stdout {
            if b != 79 && b != 10 {
                bad_o += 1
            }
        }
        var bad_e := 0
        for b in r.stderr {
            if b != 69 && b != 10 {
                bad_e += 1
            }
        }
        lo := r.stdout.Len()
        le := r.stderr.Len()
        arena.Free()
        l := counting.Live()
        by := counting.Bytes()
        if bad_o != 0 || bad_e != 0 {
            mixed += 1
        }
        if lo != 1048576 || le != 1048576 {
            short += 1
        }
        if l != 0 || by != 0 {
            leaked += 1
        }
        if bad_o != 0 || bad_e != 0 || lo != 1048576 || le != 1048576 || l != 0 || by != 0 {
            try Print(mut out, "run {i}: stdout {lo} bytes, {bad_o} not O; stderr {le} bytes, {bad_e} not E; live {l} bytes {by}\n", (i, lo, bad_o, le, bad_e, l, by))
        }
    }
    try Print(mut out, "{n} runs: {fails} failed, {mixed} mixed, {short} wrong length, {leaked} with Counting not 0 after free\n", (n, fails, mixed, short, leaked))
}
