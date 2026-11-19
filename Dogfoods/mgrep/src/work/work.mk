// Why: searching many files is parallel work whose output must not depend
// on which task finishes first.
// What: Run, Tuning and Totals, and the stats table.
// How: a feeder task hands out file indexes through a channel, search tasks
// take them, at most max_open files are open at once, and this task prints
// each report in file order; a small file is read in one call, a larger one
// sniffed for binary first.

import std.fs
import std.simd
import std.sync
import std.text
import src.search
import src.walk

/// Files at most this large are read in one call; larger ones are sniffed first.
export const SmallFile: i64 = 65536

/// How many bytes at the start of a file decide whether it is binary.
export const SniffBytes: int = 1024

/// A file changed size while it was being read.
export error Changed

/// How the search tasks run.
export struct Tuning {
    /// Search tasks
    export threads: int
    /// Files open at once, across every task
    export max_open: int
    /// Search files that hold a NUL byte too
    export binary: bool
    /// Print a table of matching files and totals at the end
    export stats: bool
}

/// What a whole run found.
export struct Totals {
    export files: int
    export matched_files: int
    export matched_lines: int
    export matches: int
    export lines: int
    export invalid_lines: int
    export skipped_binary: int
    export failed: int
}

// What searching one file produced, sent from a search task to the printer
struct Report {
    index: int
    out: HeapText
    err: HeapText
    sum: search.Summary
    binary: bool
    failed: bool
}

impl Free for Report {
    Free(self, a: &Alloc) {
        Report(out, err, ..) := self
        out.Free(a)
        err.Free(a)
    }
}

// One matching file's line for the table that `stats` prints
struct Row {
    export name: str
    export lines: int
    export matches: int
}

/// Searches every file of `files` in parallel tasks and writes their reports
/// to `out` and `err` in the order of `files`, whatever order the tasks finish
/// in, so the output is the same on any number of carriers. A feeder task hands
/// out file indexes through a channel, `tune.threads` tasks search, at most
/// `tune.max_open` files are open at once, and this task prints. With `quiet`,
/// the first match cancels the rest. O(total bytes); each task allocates in an
/// arena of its own, and reports wait in a heap map until their turn.
export Run(a: &Arena, heap: &Heap, io: Io, files: []walk.Source, m: &search.Matcher, cfg: &search.Config, tune: &Tuning, out: mut Writer, err: mut Writer): OutOfMemory|WriteError!Totals *files into a {
    var totals := Totals(files: files.Len(), matched_files: 0, matched_lines: 0, matches: 0, lines: 0, invalid_lines: 0, skipped_binary: 0, failed: 0)
    var rows := List[Row].New(a)
    h := heap.Alloc()
    jobs := try Chan[int].New(h, 64)
    defer jobs.Free(h)
    results := try Chan[Report].New(h, 64)
    defer results.Free(h)
    gate := sync.Semaphore.New(tune.max_open)
    var pending := HeapMap[int, Report].New(h)
    defer pending.Free(h)
    var printed := false
    var next := 0
    scope s := io.Scope() {
        s.go Feed(io, &jobs, files.Len())
        for t in 0..tune.threads {
            _ = t
            s.go Work(heap, io, files, m, cfg, tune.binary, &jobs, &results, &gate)
        }
        for next < files.Len() {
            r := results.Recv(io) else {
                break
            }
            if r.index != next {
                pending.Set(h, r.index, r) else {
                    r.Free(h)
                    s.Cancel()
                    break
                }
                continue
            }
            onerr r.Free(h)
            printed = try Deliver(files, cfg, tune, &r, printed, mut totals, mut rows, out, err)
            r.Free(h)
            next += 1
            for p := pending.Remove(&next) {
                onerr p.Free(h)
                printed = try Deliver(files, cfg, tune, &p, printed, mut totals, mut rows, out, err)
                p.Free(h)
                next += 1
            }
            if cfg.quiet && totals.matched_lines > 0 {
                s.Cancel()
                break
            }
        }
        // the loop ends early only when a waiting report could not be stored
        if next < files.Len() && !(cfg.quiet && totals.matched_lines > 0) {
            fail OutOfMemory
        }
    }
    if tune.stats {
        try Table(rows[..], &totals, out)
    }
    return totals
}

// Prints one file's report in its turn.
Deliver(files: []walk.Source, cfg: &search.Config, tune: &Tuning, r: &Report, printed: bool, totals: mut Totals, rows: mut List[Row], out: mut Writer, err: mut Writer): OutOfMemory|WriteError!bool *files into rows {
    try err.Str(r.err.Str())
    if r.failed {
        totals.failed += 1
        return printed
    }
    if r.binary {
        totals.skipped_binary += 1
        return printed
    }
    totals.lines += r.sum.lines
    totals.invalid_lines += r.sum.invalid_lines
    if r.sum.invalid_lines > 0 && !cfg.quiet {
        try Print(err, "mgrep: {name}: lines skipped as not UTF-8: {n}\n", (name: files[r.index].name, n: r.sum.invalid_lines))
    }
    if r.sum.matched_lines > 0 {
        totals.matched_files += 1
        totals.matched_lines += r.sum.matched_lines
        totals.matches += r.sum.matches
        if tune.stats {
            try rows.Push(Row(name: files[r.index].rel, lines: r.sum.matched_lines, matches: r.sum.matches))
        }
    }
    body := r.out.Str()
    if body.Len() == 0 {
        return printed
    }
    if printed && cfg.heading {
        try out.Str("\n")
    } else if printed && (cfg.before > 0 || cfg.after > 0) && !cfg.count && !cfg.files_only {
        try out.Str("--\n")
    }
    try out.Str(body)
    return true
}

// Hands out the index of every file, then closes the channel.
Feed(io: Io, jobs: &Chan[int], n: int) {
    defer jobs.Close()
    for i in 0..n {
        jobs.Send(io, i) else {
            return
        }
    }
}

// A search task: takes file indexes until the channel closes, and sends one report for each.
Work(heap: &Heap, io: Io, files: []walk.Source, m: &search.Matcher, cfg: &search.Config, binary: bool, jobs: &Chan[int], results: &Chan[Report], gate: &sync.Semaphore) {
    h := heap.Alloc()
    var arena := Arena.New(heap) else {
        // no arena: each file still gets its report, so the printer never waits forever
        for {
            i := jobs.Recv(io) else {
                return
            }
            r := Failed(h, i, files[i].name, "out of memory")
            results.Send(io, r) else {
                r.Free(h)
                return
            }
        }
    }
    defer arena.Free()
    for {
        i := jobs.Recv(io) else {
            return
        }
        r := One(&arena, h, io, &files[i], i, m, cfg, binary, gate)
        arena.Reset()
        results.Send(io, r) else {
            r.Free(h)
            return
        }
    }
}

// A report that says only that the file could not be searched.
Failed(h: &Alloc, index: int, name: str, why: str): Report from h {
    var e := HeapText.New(h)
    _ = e.Push(h, "mgrep: ") else {}
    _ = e.Push(h, name) else {}
    _ = e.Push(h, ": ") else {}
    _ = e.Push(h, why) else {}
    _ = e.Push(h, "\n") else {}
    return Report(index: index, out: HeapText.New(h), err: e, sum: search.Summary(matched_lines: 0, matches: 0, lines: 0, invalid_lines: 0), binary: false, failed: true)
}

// Searches one file into a report, under the open-file gate.
One(a: &Arena into a, h: &Alloc, io: Io, src: &walk.Source, index: int, m: &search.Matcher, cfg: &search.Config, binary: bool, gate: &sync.Semaphore): Report from h {
    // a free permit is taken without suspending; otherwise wait for one
    if !gate.TryAcquire(1) {
        gate.Acquire(io, 1) else {
            return Failed(h, index, src.name, "Canceled")
        }
    }
    data := Load(a, io, src, binary) else e {
        gate.Release(1)
        why := a.Fmt("{e}", (e)) else { "out of memory" }
        return Failed(h, index, src.name, why)
    }
    gate.Release(1)
    bytes := data else {
        var r := Failed(h, index, src.name, "")
        r.binary = true
        r.failed = false
        r.err.Clear()
        return r
    }
    var text := Text.New(a)
    var w := text.Writer()
    sum := search.Search(a, m, cfg, src.name, bytes, mut w) else e {
        why := a.Fmt("{e}", (e)) else { "out of memory" }
        return Failed(h, index, src.name, why)
    }
    var out := HeapText.New(h)
    out.Push(h, text.Str()) else {
        out.Free(h)
        return Failed(h, index, src.name, "out of memory")
    }
    return Report(index: index, out: out, err: HeapText.New(h), sum: sum, binary: false, failed: false)
}

// Reads a whole file into `a`, or gives none for a binary file that is not wanted.
// A small file is read in one call; a larger one is sniffed with ReadAt first, so
// a binary file is skipped after reading SniffBytes of it.
Load(a: &Arena, io: Io, src: &walk.Source, binary: bool): fs.Error|ReadError|Changed|OutOfMemory!?[]u8 from a {
    if src.size <= SmallFile {
        bytes := try fs.ReadBytes(a, io, src.path)
        if !binary && IsBinary(bytes) {
            return none
        }
        return some(bytes)
    }
    var f := try fs.Open(io, src.path)
    onerr f.Abandon()
    var head: [SniffBytes]u8 = [0; SniffBytes]
    n := try f.ReadAt(io, 0, mut head[..])
    if !binary && IsBinary(head[..n]) {
        try f.Close(io)
        return none
    }
    data := try a.Slice[u8](Cast[int](src.size))
    var chunk := try a.Slice[u8](65536)
    var r := f.Reader(io, chunk)
    first := try r.Read(mut data[..])
    rest := try r.ReadAll(mut data[first..])
    var probe: [1]u8 = [0]
    grew := try f.Read(io, mut probe[..])
    try f.Close(io)
    if grew > 0 || first + rest < data.Len() {
        fail Changed
    }
    return some(data[..first + rest])
}

/// Reports whether `bytes` looks binary: a NUL byte among its first SniffBytes bytes.
export IsBinary(bytes: []u8): bool {
    end := if bytes.Len() < SniffBytes { bytes.Len() } else { SniffBytes }
    return simd.IndexByte(bytes[..end], 0).IsSome()
}

// Prints the table of matching files, each name padded to the widest in
// terminal columns, then the totals.
Table(rows: []Row, totals: &Totals, out: mut Writer): WriteError!void {
    var widest := 5
    for r in rows {
        w := text.Width(r.name)
        if w > widest {
            widest = w
        }
    }
    try Pad(out, "file", widest)
    try out.Str("   lines  matches\n")
    for r in rows {
        try Pad(out, r.name, widest)
        try Print(out, " {r.lines:>7} {r.matches:>8}\n", (r))
    }
    try Print(out, "{totals.matched_files} of {totals.files} files matched, {totals.matched_lines} lines, {totals.matches} matches, {totals.lines} lines searched\n", (totals))
    if totals.skipped_binary > 0 {
        try Print(out, "binary files skipped: {totals.skipped_binary}\n", (totals))
    }
    if totals.invalid_lines > 0 {
        try Print(out, "lines skipped as not UTF-8: {totals.invalid_lines}\n", (totals))
    }
}

// Writes `s` then spaces up to `width` terminal columns.
Pad(out: mut Writer, s: str, width: int): WriteError!void {
    try out.Str(s)
    for i in text.Width(s)..width {
        _ = i
        try out.Str(" ")
    }
}
