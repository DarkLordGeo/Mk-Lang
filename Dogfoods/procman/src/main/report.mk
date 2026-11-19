// Why: `start` shows each process's output and, on request, a summary of
// what failed, without letting a chatty process grow procman.
//
// What: Report (New, Handle, Print, Keep, Summary), ProcStat and TailBudget.
//
// How: output prints as each run ends; the last outputs of failed processes
// are kept within a fixed memory budget, `--tail-memory`.

import std.log
import std.mem
import std.proc
import src.manager
import src.procfile

/// The life of one process as the events told it.
struct ProcStat {
    runs: int
    restarts: int
    /// How the last run ended: `exit N`, `failed`, `killed`, or `-` before any
    last: str
    state: str
    failed: bool
}

struct Report {
    procs: []procfile.Entry
    stats: List[ProcStat]
    width: int
    /// The output of each process's last run, within the tail budget
    tails: HeapList[HeapText]
    /// Processes whose tail no longer fits; warned about once
    full: List[bool]
}

Report.New(a: &Arena, ta: &Alloc, procs: []procfile.Entry): OutOfMemory!Report from a, ta, procs {
    var stats := List[ProcStat].New(a)
    var full := List[bool].New(a)
    var width := 0
    for e in procs {
        try stats.Push(ProcStat(runs: 0, restarts: 0, last: "-", state: "waiting", failed: false))
        try full.Push(false)
        if e.name.Len() > width {
            width = e.name.Len()
        }
    }
    var tails := HeapList[HeapText].New(ta)
    for _ in procs {
        // A budget too small for the list itself leaves the rest without tails.
        t := HeapText.New(ta)
        tails.Push(ta, t) else {
            t.Free(ta)
            break
        }
    }
    return Report(procs: procs, stats: stats, width: width, tails: tails, full: full)
}

/// Shows `ev` and records it.
Report.Handle(mut self, a: &Arena into self, ta: &Alloc, out: mut Writer, logs: mut Logs, ev: &manager.Event): WriteError!void {
    i := ev.proc
    name := self.procs[i].name
    st := mut self.stats[i]
    match ev.phase {
        .Started {
            st.runs = ev.run
            st.state = "running"
            logs.Info("{name}: started (run {run})", (name, run: ev.run))
        }
        .Exited {
            st.last = a.Fmt("exit {code}", (code: ev.code)) else { "exit" }
            st.state = "ended"
            st.failed = ev.code != 0
            try self.Print(a, out, name, ev.stdout.Str())
            try self.Print(a, out, name, ev.stderr.Str())
            level := if ev.code == 0 { log.Level.Info } else { log.Level.Warn }
            logs.At(level, "{name}: exited with code {code}", (name, code: ev.code))
            self.Keep(ta, logs, i, name, ev)
        }
        .Failed {
            st.state = "failed"
            st.failed = true
            st.last = "failed"
            if e := ev.failure {
                why := Why(a, e, &self.procs[i])
                logs.Error("{name}: run {run} failed: {why}", (name, run: ev.run, why))
            }
        }
        .Restarting {
            st.restarts += 1
            st.state = "backoff"
            logs.Warn("{name}: restarting in {ms}ms (restart {n} of {max})", (name, ms: ev.delay_ms, n: ev.attempt, max: self.procs[i].retries))
        }
        .GaveUp {
            st.state = "gave-up"
            st.failed = true
            logs.Error("{name}: gave up after {runs} runs", (name, runs: st.runs))
        }
        .Stopped {
            if st.state == "running" {
                st.last = "killed"
            }
            st.state = "stopped"
            logs.Info("{name}: stopped", (name))
        }
        .DirFailed {
            if e := ev.dir_failure {
                logs.Warn("{name}: cannot write the run directory: {e}", (name, e))
            }
        }
    }
}

// Writes each line of `text` as `name | line`, the names padded to one width.
Report.Print(&self, a: &Arena, out: mut Writer, name: str, text: str): WriteError!void {
    if text.Len() == 0 {
        return
    }
    var scratch := Arena.Sub(a) else {
        return
    }
    defer scratch.Free()
    var pad := Text.New(&scratch)
    pad.Push(name) else {}
    for _ in name.Len()..self.width {
        pad.Push(" ") else {}
    }
    for line in text.Lines() {
        try Print(out, "{p} | {line}\n", (p: pad.Str(), line))
    }
}

// Keeps the output of the run that just ended as the process's tail. The
// budget's allocator frees only its last block, so tails that keep changing
// fill it; then the process keeps no tail and the summary says so.
Report.Keep(mut self, ta: &Alloc, logs: mut Logs, i: int, name: str, ev: &manager.Event) {
    if i >= self.tails.Len() || self.full[i] {
        return
    }
    self.tails[i].Set(ta, ev.stdout.Str()) else {
        self.tails[i].Clear()
        self.full[i] = true
        logs.Warn("{name}: --tail-memory is full; the summary will not show its output", (name))
        return
    }
    self.tails[i].Push(ta, ev.stderr.Str()) else {
        self.tails[i].Clear()
        self.full[i] = true
        logs.Warn("{name}: --tail-memory is full; the summary will not show its output", (name))
    }
}

/// True when any process failed: an exit code other than 0 on its last run,
/// a run that could not start, or giving up.
Report.Failed(&self): bool {
    for st in &self.stats {
        if st.failed {
            return true
        }
    }
    return false
}

/// The summary table, then the kept output of each process that failed.
Report.Summary(&self, out: mut Writer): WriteError!void {
    try Print(out, "{n:<12} {s:<8} {r:>4} {x:>8}  {l}\n", (n: "NAME", s: "STATE", r: "RUNS", x: "RESTARTS", l: "LAST"))
    for i, e in self.procs {
        st := &self.stats[i]
        try Print(out, "{n:<12} {s:<8} {r:>4} {x:>8}  {l}\n", (n: e.name, s: st.state, r: st.runs, x: st.restarts, l: st.last))
    }
    for i, e in self.procs {
        if !self.stats[i].failed {
            continue
        }
        if i >= self.tails.Len() || self.full[i] {
            try Print(out, "\n{n}: output not kept (--tail-memory is full)\n", (n: e.name))
            continue
        }
        tail := self.tails[i].Str()
        if tail.Len() == 0 {
            continue
        }
        try Print(out, "\nlast output of {n}:\n", (n: e.name))
        for line in tail.Lines() {
            try Print(out, "  {line}\n", (line))
        }
    }
}

impl Free for Report {
    Free(self, a: &Alloc) {
        Report(tails, stats: _, full: _, ..) := self
        tails.Free(a)
    }
}

// Says why a run failed, in words a user acts on.
Why(a: &Arena, e: proc.Error, entry: &procfile.Entry): str from a {
    return match e {
        proc.NotFound { a.Fmt("no program `{p}`", (p: entry.argv[0])) else { "no such program" } }
        proc.Denied { "permission denied" }
        proc.TooLarge { a.Fmt("its output passed the limit of {n} bytes", (n: entry.limit)) else { "too much output" } }
        proc.Os(code) { a.Fmt("the OS refused, code {code}", (code)) else { "the OS refused" } }
        proc.Unsupported { "not supported on this system" }
        Canceled { "stopped" }
        OutOfMemory { "out of memory" }
        _ {
            Panic("proc.Error gained a member this match does not handle")
        }
    }
}

/// The budget the tails live in: `bytes` of `a`, counted.
TailBudget(a: &Arena, bytes: int): OutOfMemory!mem.Fixed from a {
    buf := try a.Slice[u8](bytes)
    return mem.Fixed.New(buf)
}
