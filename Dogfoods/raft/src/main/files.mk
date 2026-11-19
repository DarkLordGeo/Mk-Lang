// Why: a failing seed is kept as a recording that reproduces the run.
// What: Header, ParseHeader, ReplayFile, CheckFile and their errors.
// How: the first line holds the settings of the run; the bytes after it are
// the recorder's log, stored as "log N" lines of N bytes each.

import std.fs
import std.sim
import std.time
import src.check
import src.cluster
import src.history

// The first line of a recording: the settings that reproduce the run.
// The bytes after it are the recorder's log.
const Magic: str = "raft-recording 1"

Header(a: &Arena, s: &cluster.Settings, peers: str): OutOfMemory!str from a {
    return try a.Fmt(
        "{magic} seed={s.seed} nodes={s.nodes} clients={s.clients} ops={s.ops} keys={s.keys} loss={s.loss} delay={s.delay} duplicate={s.duplicate} corrupt={s.corrupt} inbox={s.inbox} partitions={p} faults={f} crash={c} duration={ms} tasks={t} stale={st} peers={peers}\n",
        (
            magic: Magic,
            s,
            p: Flag(s.partitions),
            f: Flag(s.faults),
            c: Flag(s.crash),
            ms: s.duration.Nanos() / 1_000_000,
            t: Flag(s.tasks),
            st: Flag(s.params.stale_reads),
            peers,
        ),
    )
}

Flag(b: bool): int {
    return if b { 1 } else { 0 }
}

/// A recording whose first line is not a raft recording header.
error BadHeader(word: int)

// The settings and peers a header names; every key is required once.
ParseHeader(line: str): BadHeader!(s: cluster.Settings, peers: str) {
    if !line.StartsWith(Magic) {
        fail BadHeader(word: 1)
    }
    var s := cluster.Settings.Default(0)
    var peers := ""
    var seen := 0
    var word := 2
    for part in line[Magic.Len()..].Words() {
        word += 1
        eq := part.Find("=") else {
            fail BadHeader(word: word)
        }
        key := part[..eq]
        value := part[eq + 1..]
        if key == "peers" {
            peers = value
            seen += 1
            continue
        }
        n := value.ParseInt() else {
            fail BadHeader(word: word)
        }
        b := Bounds(key) else {
            fail BadHeader(word: word)
        }
        if n < b.lo || n > b.hi {
            fail BadHeader(word: word)
        }
        match key {
            "seed" {
                s.seed = Cast[u64](n)
            }
            "nodes" {
                s.nodes = Trunc[int](n)
            }
            "clients" {
                s.clients = Trunc[int](n)
            }
            "ops" {
                s.ops = Trunc[int](n)
            }
            "keys" {
                s.keys = Trunc[int](n)
            }
            "loss" {
                s.loss = Trunc[int](n)
            }
            "delay" {
                s.delay = Trunc[int](n)
            }
            "duplicate" {
                s.duplicate = Trunc[int](n)
            }
            "corrupt" {
                s.corrupt = Trunc[int](n)
            }
            "inbox" {
                s.inbox = Trunc[int](n)
            }
            "partitions" {
                s.partitions = n != 0
            }
            "faults" {
                s.faults = n != 0
            }
            "crash" {
                s.crash = n != 0
            }
            "duration" {
                s.duration = time.Millis(n)
            }
            "tasks" {
                s.tasks = n != 0
            }
            "stale" {
                s.params.stale_reads = n != 0
            }
            _ {
                fail BadHeader(word: word)
            }
        }
        seen += 1
    }
    if seen != 17 || Conflict(&s).IsSome() {
        fail BadHeader(word: word)
    }
    return (s, peers)
}

// raft check FILE: reads a history and says whether it is linearizable.
CheckFile(sys: &System, path: str): WriteError!int {
    text := fs.ReadText(sys.arena, sys.io, path) else e {
        try sys.PrintErr("raft: {path}: {e}\n", (path, e))
        return ExitInput
    }
    h := history.Parse(sys.arena, text) else e {
        try sys.PrintErr("raft: {path}: {e}\n", (path, e))
        return ExitInput
    }
    v := check.Check(sys.arena, &h) else e {
        try sys.PrintErr("raft: {path}: {e}\n", (path, e))
        return ExitInput
    }
    if !v.ok {
        name := h.Name(v.key)
        try sys.Print("{path}: not linearizable: register {name} has no order for its {v.ops} operations ({v.states} states searched)\n", (path, name, v))
        return ExitNotLinearizable
    }
    try sys.Print("{path}: linearizable: {v.ops} operations on {n} registers ({v.states} states searched)\n", (path, v, n: h.keys.Len()))
    return ExitOk
}

// raft replay FILE: runs the recorded seed again, phase by phase, from its
// recording and prints its history, which is the one the recording run
// printed.
ReplayFile(sys: &System, o: &Options, path: str): WriteError!int {
    data := fs.ReadBytes(sys.arena, sys.io, path) else e {
        try sys.PrintErr("raft: {path}: {e}\n", (path, e))
        return ExitInput
    }
    end := data.Index(&b'\n') else {
        try sys.PrintErr("raft: {path}: not a raft recording\n", (path))
        return ExitInput
    }
    line := str.From(data[..end]) else {
        try sys.PrintErr("raft: {path}: not a raft recording\n", (path))
        return ExitInput
    }
    header := ParseHeader(line) else e {
        try sys.PrintErr("raft: {path}: not a raft recording: {e}\n", (path, e))
        return ExitInput
    }
    logs := Logs(sys.arena, data[end + 1..]) else {
        try sys.PrintErr("raft: {path}: not a raft recording: its logs do not read\n", (path))
        return ExitInput
    }
    var sm := sim.Sim.New(sys.arena, header.s.seed) else e {
        try sys.PrintErr("raft: {e}\n", (e))
        return ExitMemory
    }
    defer sm.Free()
    cfg := cluster.Config.Parse(sys.arena, sm.Io(), header.peers, header.s.nodes, header.s.clients) else e {
        try sys.PrintErr("raft: {path}: bad peers: {e}\n", (path, e))
        return ExitInput
    }
    var phase := cluster.Phase.New(sys.arena, &header.s) else e {
        try sys.PrintErr("raft: {e}\n", (e))
        return ExitMemory
    }
    for i, log in logs {
        last := i == logs.Len() - 1
        rep := sim.Replayer.New(log) else e {
            try sys.PrintErr("raft: {e}\n", (e))
            return ExitMemory
        }
        defer rep.Free()
        var stopped := false
        _ = RunBudget(sys.arena, sys, rep.Io(), mut sm, o, &header.s, &cfg, mut phase) else e {
            if last {
                try sys.PrintErr("raft: {path}: the replay left the recording: {e}\n", (path, e))
                return ExitReplay
            }
            stopped = true
        }
        if !last && !stopped || !rep.Done() || last && rep.Diverged() {
            try sys.PrintErr("raft: {path}: the replay differs from the recording\n", (path))
            return ExitReplay
        }
    }
    var w := sys.Stdout()
    try phase.history.Write(mut w)
    v := check.Check(sys.arena, &phase.history) else e {
        try sys.PrintErr("raft: {path}: {e}\n", (path, e))
        return ExitInput
    }
    verdict := if v.ok { "linearizable" } else { "not linearizable" }
    try sys.Print("replay of seed {seed}: identical to the recording, {verdict}\n", (seed: header.s.seed, verdict))
    return if v.ok { ExitOk } else { ExitNotLinearizable }
}

/// A recording's logs that do not read as "log N" lines and N bytes each.
error BadLogs

// The logs after a recording's header: "log N", a newline and N bytes,
// one or more times.
Logs(a: &Arena, data: []u8 into a): BadLogs|OutOfMemory!List[[]u8] from a, data {
    var logs := List[[]u8].New(a)
    var rest := data
    for rest.Len() > 0 {
        end := rest.Index(&b'\n') else {
            fail BadLogs
        }
        line := str.From(rest[..end]) else {
            fail BadLogs
        }
        if !line.StartsWith("log ") {
            fail BadLogs
        }
        n := line[4..].ParseInt() else {
            fail BadLogs
        }
        if n < 0 || n > i64(rest.Len() - end - 1) {
            fail BadLogs
        }
        start := end + 1
        try logs.Push(rest[start..start + Trunc[int](n)])
        rest = rest[start + Trunc[int](n)..]
    }
    if logs.Len() == 0 {
        fail BadLogs
    }
    return logs
}
