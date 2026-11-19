// Why: the write-ahead log can be read, checked and compacted offline.
// What: `kvd wal dump|check|compact FILE`: RunWal, Dump, Check and Measure.
// How: the file is read whole and its records decoded with wal.Records;
// `--max-memory` bounds the store rebuilt from it.

import std.fs
import std.mem
import src.store
import src.wal

/// The flags `kvd wal` takes.
const WalFlags: Accepts = Accepts(values: ["--max-memory", "--out"], switches: [])

/// The longest value `dump` prints whole; longer ones are cut with "...".
const DumpWidth: int = 40

/// `kvd wal`: dispatches to its three actions.
RunWal(sys: &System, rest: []str): int *rest into sys {
    args := ParseArgs(sys.arena, rest, &WalFlags) else e {
        return UsageError(sys, e)
    }
    action := args.Arg(0) else { "" }
    path := args.Arg(1) else {
        sys.PrintErr("kvd: wal needs an action and a file; see kvd --help\n") else {}
        return ExitUsage
    }
    if args.plain.Len() > 2 {
        sys.PrintErr("kvd: wal takes one file; see kvd --help\n") else {}
        return ExitUsage
    }
    limit := args.Int("--max-memory", 4096, 0) else e {
        return UsageError(sys, e)
    }
    data := fs.ReadBytes(sys.arena, sys.io, path) else e {
        sys.PrintErr("kvd: {path}: {e}\n", (path, e)) else {}
        return ExitFailure
    }
    return match action {
        "dump" { Dump(sys, path, data) }
        "check" { Check(sys, path, data, Cast[int](limit), "") }
        "compact" { Check(sys, path, data, Cast[int](limit), args.Str("--out", path)) }
        _ {
            sys.PrintErr("kvd: unknown wal action {action}; see kvd --help\n", (action)) else {}
            ExitUsage
        }
    }
}

/// Prints the header and every record, then how the log ends. Exits 3 on
/// a bad header or damage before the end.
Dump(sys: &System, path: str, data: []u8): int {
    head := wal.ReadHeader(data) else {
        sys.PrintErr("kvd: {path}: not a kvd log, or its header is damaged\n", (path)) else {}
        return ExitCorrupt
    }
    how := if head.clean { "clean shutdown" } else { "not shut down cleanly" }
    sys.Print("{path}: generation {g}, {how}\n", (path, g: head.generation, how)) else {}
    var records := wal.Records.New(data)
    for r := records.Next() {
        op := match r.op {
            .Set { "SET" }
            .Del { "DEL" }
            .Append { "APPEND" }
        }
        value := if r.value.Len() > DumpWidth { Cut(r.value) } else { r.value }
        sep := if value.Len() == 0 { "" } else { " " }
        sys.Print("{r.offset:>8} {op:<6} {r.key}{sep}{value}\n", (r, op, sep, value)) else {}
    }
    end := records.End()
    sys.Print("end: {end}\n", (end)) else {}
    return if wal.CorruptAt(end).IsSome() { ExitCorrupt } else { 0 }
}

/// The first `DumpWidth` bytes of `s` on a character boundary, for display.
Cut(s: str): str from s {
    var n := DumpWidth
    for n > 0 && (s.Byte(n) & 0xC0) == 0x80 {
        n -= 1
    }
    return s[..n]
}

/// Loads the log into a store whose memory is counted, under `limit` bytes
/// when `limit` is not 0, and reports it; with an `out` path, writes it as
/// one record per key at `out`. Exits 3 on damage and 4 past the limit.
Check(sys: &System, path: str, data: []u8, limit: int, out: str): int {
    if limit == 0 {
        counted := mem.Counting.New(sys.heap.Alloc())
        return Measure(sys, path, data, counted.Alloc(), &counted, out)
    }
    budget := sys.arena.Slice[u8](limit) else {
        return ExitMemory
    }
    fixed := mem.Fixed.New(budget)
    counted := mem.Counting.New(fixed.Alloc())
    return Measure(sys, path, data, counted.Alloc(), &counted, out)
}

/// `Check` over `a`, the allocator `counted` counts. `a` is a parameter of
/// its own, so the store's stores reach the allocator and stop there.
Measure(sys: &System, path: str, data: []u8, a: &Alloc, counted: &mem.Counting, out: str): int {
    var s := store.Store.NewFixed(a)
    defer s.Free(a)
    got := wal.Load(a, data, mut s) else e {
        sys.PrintErr("kvd: {path}: {e}\n", (path, e)) else {}
        return match e {
            OutOfMemory { ExitMemory }
            _ { ExitCorrupt }
        }
    }
    how := if got.clean { "clean shutdown" } else { "not shut down cleanly" }
    sys.Print("{path}: {n} records, {k} keys, {b} bytes of keys and values\n", (path, n: got.records, k: s.Len(), b: s.Bytes())) else {}
    sys.Print("generation {g}, {how}, end: {end}\n", (g: got.generation, how, end: got.end)) else {}
    sys.Print("digest {d:016x}, peak heap {p} bytes\n", (d: s.Digest(), p: counted.Peak())) else {}
    if out.Len() == 0 {
        return 0
    }
    tmp := sys.arena.Fmt("{out}.tmp", (out)) else {
        return ExitMemory
    }
    size := wal.WriteLog(sys.io, out, tmp, got.generation + 1, &s) else e {
        sys.PrintErr("kvd: {out}: {e}\n", (out, e)) else {}
        return ExitFailure
    }
    sys.Print("compacted: {n} records, {size} bytes\n", (n: s.Len(), size)) else {}
    return 0
}
