// Why: one unreadable file must not stop a search of the others.
// What: tests of work.Run under injected open and read faults.
// How: SearchUnderFault runs a search on a std.sim machine with one fault
// armed and checks the report.

import std.fs
import std.sim
import src.search
import src.walk

// Searches the files `names` of a simulated disk for "needle" after
// arranging for the next operation of kind `op` to fail with `fault`.
SearchUnderFault(sys: &System, seed: u64, op: sim.Op, fault: sim.Fault): OutOfMemory|WriteError|fs.Error|search.Error!Totals {
    var s := try sim.Sim.New(sys.arena, seed)
    defer s.Free()
    io := s.Io()
    try fs.WriteAll(io, "a.txt", b"needle one\nnothing\n")
    try fs.WriteAll(io, "b.txt", b"nothing\nneedle two\n")
    try fs.WriteAll(io, "c.txt", b"no match here\n")
    names := ["a.txt", "b.txt", "c.txt"]
    filter := walk.Filter(hidden: false, globs: names[..0], max_depth: -1, max_size: -1, separator: "/")
    var files := List[walk.Source].New(sys.arena)
    var errors := Text.New(sys.arena)
    var ew := errors.Writer()
    problems := try walk.Walk(sys.arena, io, names[..], &filter, mut files, mut ew)
    Assert(problems == 0)
    Assert(files.Len() == 3)
    patterns := ["needle"]
    spec := search.Spec(patterns: patterns[..], fixed: false, fold: false, word: false)
    m := try search.Compile(sys.arena, &spec)
    cfg := search.Config(invert: false, count: false, count_matches: false, files_only: false, only_matching: false, line_number: true, column: false, heading: false, with_filename: true, color: false, before: 0, after: 0, max_count: 0, replace: none, group: none, quiet: false)
    tune := Tuning(threads: 2, max_open: 1, binary: false, stats: false)
    s.FailNext(op, fault)
    var output := Text.New(sys.arena)
    var ow := output.Writer()
    totals := try Run(sys.arena, sys.heap, io, files[..], &m, &cfg, &tune, mut ow, mut ew)
    return totals
}

test "a denied read is reported for its file and the other files are still searched" (sys: &System) {
    for seed in 0..20 {
        totals := try SearchUnderFault(sys, Cast[u64](seed), sim.Op.Read, sim.Fault.Denied)
        Assert(totals.failed == 1)
        Assert(totals.files == 3)
        Assert(totals.matched_lines >= 1)
    }
}

test "a denied open is reported for its file and the other files are still searched" (sys: &System) {
    for seed in 0..20 {
        totals := try SearchUnderFault(sys, Cast[u64](seed), sim.Op.Open, sim.Fault.Denied)
        Assert(totals.failed == 1)
        Assert(totals.matched_lines >= 1)
    }
}
