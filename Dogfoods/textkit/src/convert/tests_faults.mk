// Why: convert must report disk faults, not lose output.
// What: tests of `textkit convert` under simulated faults.
// How: RunConvert runs it on std.sim's disk and keeps its output.

import std.fs
import std.sim
import src.input

/// Runs `textkit convert` with `args` on the simulated disk of `s`, with
/// standard output and error kept in `out` and `err`. Returns the exit status.
RunConvert(sys: &System, s: &sim.Sim, args: []str, out: mut Text, err: mut Text): int {
    c := input.Ctx(arena: sys.arena, heap: sys.heap, io: s.Io())
    var streams := input.Streams(out: out.Writer(), err: err.Writer())
    var stdin := sys.Stdin()
    status := Convert(&c, mut streams, mut stdin, args)
    streams.out.Flush() else {}
    streams.err.Flush() else {}
    return status
}

test "a read fault stops one input's conversion, and the next input is still converted" (sys: &System) {
    for seed in 0..20 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        defer s.Free()
        // "café" in windows-1252
        cafe: [5]u8 = [b'c', b'a', b'f', 0xE9, b'\n']
        try fs.WriteAll(s.Io(), "a.txt", cafe[..])
        try fs.WriteAll(s.Io(), "b.txt", cafe[..])
        s.FailNext(sim.Op.Read, sim.Fault.Denied)
        var out := Text.New(sys.arena)
        var err := Text.New(sys.arena)
        args: [6]str = ["--from", "latin1", "--buffer", "2", "a.txt", "b.txt"]
        status := RunConvert(sys, &s, args[..], mut out, mut err)
        Assert(status == input.Failed)
        Assert(err.Str() == "textkit: a.txt: ReadError\n")
        Assert(out.Str() == "café\n")
    }
}

test "an open fault is reported, and a strict conversion of the next input still runs" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 7)
    defer s.Free()
    try fs.WriteAll(s.Io(), "a.txt", "one\n".AsBytes())
    s.FailNext(sim.Op.Open, sim.Fault.Denied)
    var out := Text.New(sys.arena)
    var err := Text.New(sys.arena)
    args: [5]str = ["--strict", "--dump", "hex", "a.txt", "a.txt"]
    status := RunConvert(sys, &s, args[..], mut out, mut err)
    Assert(status == input.Failed)
    Assert(err.Str() == "textkit: a.txt: Denied\n")
    Assert(out.Str() == "6f6e650a\n")
}
