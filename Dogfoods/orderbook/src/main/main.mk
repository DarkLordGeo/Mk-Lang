// Why: orderbook is a limit order book matching engine that replays a
// market-data feed and reports the book, the trades and statistics on them.
// What: Main and Run, the replay of a file or a synthetic feed, and Exit,
// a failure already reported with its exit status.
// How: each failure is reported once on standard error where it happens and
// leaves as Exit, so Main only maps it to the process status.

import std.csv
import std.fs
import std.mem
import src.book
import src.feed
import src.sheet

/// A failure already reported on standard error; `code` is the exit status.
error Exit(code: int)

export Main(sys: &System): int {
    var err := sys.Stderr()
    o := ParseArgs(sys.args[1..], mut err) else {
        return 2
    }
    if o.help {
        sys.Print("{text}", (text: Usage)) else {
            return 4
        }
        return 0
    }
    Run(sys, &o) else e {
        match e {
            Exit(code) { return code }
        }
    }
    return 0
}

Run(sys: &System, o: &Options): Exit!void {
    if o.command == "synth" {
        try Synthesize(sys, o)
        return
    }
    var arena := Arena.New(sys.heap) else {
        fail Fatal(sys, "out of memory", 5)
    }
    defer arena.Free()
    var capacity := o.synth else { 0 }
    if path := o.path {
        capacity += try FeedBound(sys, path)
    }
    opening := try Opening(sys, o, &arena)
    capacity += opening.commands.Len()
    if limit := o.memory_limit {
        buf := arena.Slice[u8](limit) else {
            fail Fatal(sys, "out of memory reserving the engine's memory limit", 5)
        }
        fixed := mem.Fixed.New(buf)
        counting := mem.Counting.New(fixed.Alloc())
        try Replay(sys, o, &arena, counting.Alloc(), &counting, &opening, capacity)
    } else {
        counting := mem.Counting.New(sys.heap.Alloc())
        try Replay(sys, o, &arena, counting.Alloc(), &counting, &opening, capacity)
    }
}

/// Writes `error: <msg>` and gives the Exit to fail with.
Fatal(sys: &System, msg: str, code: int): Exit {
    sys.PrintErr("error: {msg}\n", (msg)) else {}
    return Exit(code: code)
}

/// The most commands the feed file `path` can hold: a command takes at
/// least three bytes and a line break, so a third of the file's size bounds
/// it. Fails, reported, when the file is missing or a directory.
FeedBound(sys: &System, path: str): Exit!int {
    info := fs.Stat(sys.io, path) else e {
        sys.PrintErr("error: cannot read {path}: {e}\n", (path, e)) else {}
        fail Exit(code: 4)
    }
    if info.kind == fs.Kind.Dir { // a directory opens; say why it cannot be read
        sys.PrintErr("error: cannot read {path}: IsDirectory\n", (path)) else {}
        fail Exit(code: 4)
    }
    return Cast[int](info.size / 3) + 1
}

/// The opening book the options name, or an empty one. Fails, reported,
/// on a book that cannot be read or is not a book file.
Opening(sys: &System, o: &Options, arena: &Arena): Exit!sheet.Opening from arena {
    path := o.book else {
        return sheet.Opening.Empty(arena)
    }
    return sheet.LoadBook(arena, sys.io, path) else e {
        var err := sys.Stderr()
        code := sheet.DescribeLoad(mut err, path, e) else { 4 }
        err.Str("\n") else {}
        fail Exit(code: code)
    }
}

/// Reports the CSV error `e` met reading `path` and gives the Exit to fail
/// with: 4 when the file could not be read, 3 when it is not CSV within
/// `co`'s limits.
CsvFailure(sys: &System, path: str, e: csv.Error, co: &csv.Options): Exit {
    var err := sys.Stderr()
    match e {
        ReadError {
            err.Str("error: ") else {}
        }
        _ {}
    }
    sheet.DescribeCsv(mut err, path, e, co) else {}
    err.Str("\n") else {}
    code := match e {
        ReadError { 4 }
        _ { 3 }
    }
    return Exit(code: code)
}

/// Runs the engine over the opening book, then over the feed file, or over
/// the synthetic feed the options ask for when there is none, then reports.
/// The engine's heap comes from `counting`, which measures it.
Replay(sys: &System, o: &Options, arena: &Arena, a: &Alloc, counting: &mem.Counting, opening: &sheet.Opening, capacity: int): Exit!void {
    // the engine keeps an arena, and a store into the engine reaches it, so
    // the arena is this call's own rather than the caller's
    var own := arena.Sub() else {
        fail Fatal(sys, "out of memory starting the engine", 5)
    }
    defer own.Free()
    var engine := book.Engine.New(a, &own, capacity + 1, o.snapshot) else {
        fail Fatal(sys, "out of memory starting the engine", 5)
    }
    onerr engine.Free(a)
    var out := sys.Stdout()
    var seen := 0
    for cmd in opening.commands {
        try Step(sys, o, a, mut engine, cmd, counting, mut seen, mut out)
    }
    if path := o.path {
        try ReplayFile(sys, o, arena, a, mut engine, path, counting, mut seen, mut out)
    } else {
        for cmd in feed.Synth.New(o.synth else { 0 }, o.seed) {
            try Step(sys, o, a, mut engine, cmd, counting, mut seen, mut out)
        }
    }
    var partial := try Output(sys, o, arena, &engine, counting, mut out)
    try Sheets(sys, o, arena, &engine, opening, mut out)
    closed := engine.Finish(a)
    try Closing(sys, o, arena, &closed, counting, mut out, mut partial)
}

/// Runs the engine over every command of the feed file `path`, read as CSV
/// a record at a time. Fails, reported, at the first record that is not a
/// command, and when the file cannot be read.
ReplayFile(sys: &System, o: &Options, arena: &Arena, a: &Alloc into engine, engine: mut book.Engine, path: str, counting: &mem.Counting, seen: mut int, out: mut Writer): Exit!void {
    var scratch := arena.Sub() else {
        fail Fatal(sys, "out of memory", 5)
    }
    defer scratch.Free()
    rbuf := scratch.Slice[u8](64 * 1024) else {
        fail Fatal(sys, "out of memory", 5)
    }
    cbuf := scratch.Slice[u8](64 * 1024) else {
        fail Fatal(sys, "out of memory", 5)
    }
    var f := fs.Open(sys.io, path) else e {
        sys.PrintErr("error: cannot read {path}: {e}\n", (path, e)) else {}
        fail Exit(code: 4)
    }
    onerr f.Abandon()
    fo := feed.FeedOptions()
    var r := f.Reader(sys.io, rbuf)
    var records := csv.Records.New(mut r, cbuf, &fo)
    for {
        next := records.Next() else e {
            fail CsvFailure(sys, path, e, &fo)
        }
        rec := next else {
            break
        }
        parsed := feed.ParseRecord(&rec) else e {
            first := rec.Field(0) else { "" }
            var err := sys.Stderr()
            feed.Describe(mut err, path, e, first.Trim()) else {}
            err.Str("\n") else {}
            fail Exit(code: 3)
        }
        cmd := parsed else {
            continue
        }
        try Step(sys, o, a, engine, cmd, counting, seen, out)
    }
    f.Close(sys.io) else e {
        sys.PrintErr("error: cannot read {path}: {e}\n", (path, e)) else {}
        fail Exit(code: 4)
    }
}

/// Submits one command, reports running out of the memory limit, and
/// traces the fills it made.
Step(sys: &System, o: &Options, a: &Alloc into engine, engine: mut book.Engine, cmd: book.Command, counting: &mem.Counting, seen: mut int, out: mut Writer): Exit!void {
    engine.Submit(a, cmd) else {
        n := engine.Totals().events + 1
        limit := o.memory_limit else { 0 }
        sys.PrintErr("error: out of memory at command {n}: the engine's {limit}-byte limit is spent ({used} bytes live)\n", (n, limit, used: counting.Bytes())) else {}
        fail Exit(code: 5)
    }
    if !o.trace {
        return
    }
    all := engine.Trades().All()
    if *seen > all.Len() {
        *seen = all.Len()
    }
    for f in all[*seen..] {
        Print(out, "fill {f}\n", (f)) else {
            fail Exit(code: 4)
        }
    }
    *seen = all.Len()
}

/// Writes `n` synthetic commands to standard output as a feed.
Synthesize(sys: &System, o: &Options): Exit!void {
    var out := sys.Stdout()
    n := o.synth else { 0 }
    Print(mut out, "# orderbook synth --synth {n} --seed {seed}\n", (n, seed: o.seed)) else {
        fail Exit(code: 4)
    }
    for cmd in feed.Synth.New(n, o.seed) {
        feed.WriteLine(mut out, &cmd) else {
            fail Exit(code: 4)
        }
    }
    out.Flush() else {
        fail Exit(code: 4)
    }
}
