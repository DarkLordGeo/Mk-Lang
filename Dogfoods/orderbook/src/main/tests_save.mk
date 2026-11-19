// Why: a crash while saving must never leave a half-written report.
// What: tests that save the JSON report under simulated faults and crashes.
// How: runs the save on std.sim's disk and checks the file after each fault.

import std.fs
import std.json
import std.sim
import src.report

/// A small report, as Output builds one.
SampleReport(a: &Arena, events: int): OutOfMemory!report.ReportJson from a {
    return report.ReportJson(
        source: "sample.csv",
        events: events,
        orders: 2,
        fills: 1,
        cancels: 0,
        busts: 0,
        volume: 10,
        vwap: some("101.25"),
        buy_vwap: some("101.25"),
        sell_vwap: none,
        p50: some("101.25"),
        p99: some("101.25"),
        last: some("101.25"),
        best_bid: none,
        best_ask: some("101.50"),
        checksum: "0123456789abcdef",
        snapshot: report.SnapshotJson(event: events, best_bid: none, best_ask: some("101.50"), live: 1),
        memory: report.MemoryJson(peak_bytes: 100, live_bytes: 0, live_allocations: 0),
        rejects: List[report.RejectJson].New(a),
        bids: List[report.LevelJson].New(a),
        asks: List[report.LevelJson].New(a),
        largest: List[report.FillJson].New(a),
        bars: List[report.BarJson].New(a),
        accounts: List[report.AccountJson].New(a),
    )
}

/// The `events` member of the report in `path`, read through `io`; none when
/// the file is absent or is not a whole report.
EventsIn(a: &Arena into a, io: Io, path: str): ?i64 {
    text := fs.ReadText(a, io, path) else {
        return none
    }
    v := json.Parse(a, text) else {
        return none
    }
    events := v.Get("events") else {
        return none
    }
    return events.Int()
}

test "a crash while saving leaves the old report or the new one, whole" (sys: &System) {
    old := try SampleReport(sys.arena, 1)
    new := try SampleReport(sys.arena, 2)
    for seed in 0..200 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        defer s.Free()
        SaveJson(s.Io(), sys.arena, "report.json", &old) else {
            Assert(false)
        }
        s.CrashAfterRandomOps()
        SaveJson(s.Io(), sys.arena, "report.json", &new) else {}
        io := s.Restart()
        got := EventsIn(sys.arena, io, "report.json") else {
            Assert(false)
            return
        }
        Assert(got == 1 || got == 2)
    }
}

test "a full disk fails the save and keeps the old report" (sys: &System) {
    old := try SampleReport(sys.arena, 1)
    new := try SampleReport(sys.arena, 2)
    var s := try sim.Sim.New(sys.arena, 7)
    defer s.Free()
    try SaveJson(s.Io(), sys.arena, "report.json", &old)
    s.FailNext(sim.Op.Write, sim.Fault.NoSpace)
    var failed := false
    SaveJson(s.Io(), sys.arena, "report.json", &new) else e {
        failed = true
        msg := try sys.arena.Fmt("{e}", (e))
        // The fault reaches the report through a Writer, whose error set
        // is WriteError alone, so NoSpace itself is not visible here.
        Assert(msg == "WriteError")
    }
    Assert(failed)
    Assert(EventsIn(sys.arena, s.Io(), "report.json").Must() == 1)
}
