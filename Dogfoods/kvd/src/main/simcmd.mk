// Why: crash and fault testing is a command a user can run, with a seed
// that reproduces a run.
// What: `kvd sim` (or `kvd --sim`): RunSim and SeedRange, which parses
// `A..B` or a single seed.
// How: hands the options to simrun.Run, which runs the server and its
// clients under std.sim one seed at a time.

import src.simrun

/// The flags `kvd sim` takes.
const SimFlags: Accepts = Accepts(values: ["--seeds", "--seed", "--clients", "--steps"], switches: ["--verbose"])

export error BadRange(arg: int)

/// Parses `A..B`, both inclusive, or a single seed `N`.
SeedRange(text: str): BadRange!(first: u64, last: u64) {
    i := text.Find("..") else {
        n := text.ParseInt() else {
            fail BadRange(arg: 0)
        }
        if n < 0 {
            fail BadRange(arg: 0)
        }
        return (first: Cast[u64](n), last: Cast[u64](n))
    }
    first := text[..i].ParseInt() else {
        fail BadRange(arg: 0)
    }
    last := text[i + 2..].ParseInt() else {
        fail BadRange(arg: 0)
    }
    if first < 0 || last < first {
        fail BadRange(arg: 0)
    }
    return (first: Cast[u64](first), last: Cast[u64](last))
}

/// `kvd sim`: runs the seeds and prints a summary; exits 5 when a seed's
/// recovered state is not what its clients were told.
RunSim(sys: &System, rest: []str): int *rest into sys {
    args := ParseArgs(sys.arena, rest, &SimFlags) else e {
        return UsageError(sys, e)
    }
    // the harness passes --seed N; a range is --seeds A..B
    text := args.Str("--seed", args.Str("--seeds", "0..99"))
    (first, last) := SeedRange(text) else e {
        sys.PrintErr("kvd: bad seed range {text}: {e}\n", (text, e)) else {}
        return ExitUsage
    }
    clients := args.Int("--clients", 1, 3) else e {
        return UsageError(sys, e)
    }
    steps := args.Int("--steps", 1, 16) else e {
        return UsageError(sys, e)
    }
    if clients > i64(simrun.MaxClients) || steps > i64(simrun.MaxSteps) {
        sys.PrintErr("kvd: at most {c} clients of {s} steps\n", (c: simrun.MaxClients, s: simrun.MaxSteps)) else {}
        return ExitUsage
    }
    o := simrun.Options(first: first, last: last, clients: Cast[int](clients), steps: Cast[int](steps), verbose: args.Has("--verbose"))
    var out := sys.Stdout()
    t := simrun.Run(sys, &o, mut out) else e {
        sys.PrintErr("kvd: simulation failed: {e}\n", (e)) else {}
        return ExitSim
    }
    sys.Print("kvd sim: seeds {first}..{last}, {c} clients of {s} steps\n", (first, last, c: o.clients, s: o.steps)) else {}
    sys.Print("  crashed at a log write: {n} runs, at {p} distinct writes, {torn} torn\n", (n: t.crashes, p: t.points, torn: t.torn)) else {}
    sys.Print("  shut down cleanly:      {n} runs\n", (n: t.clean)) else {}
    sys.Print("  disk or network fault:  {n} runs, {r} steps refused\n", (n: t.faults, r: t.refused)) else {}
    sys.Print("  client gone mid-command: {n} runs\n", (n: t.partial)) else {}
    sys.Print("  recovery crashed and ran again: {n} runs\n", (n: t.recrashed)) else {}
    sys.Print("  every run recovered what its clients were told, twice\n") else {}
    sys.Print("  digest {d:016x}\n", (d: t.digest)) else {}
    return 0
}
