// simwal, Mk, std.sim: an append-and-sync write-ahead log on the simulated
// disk (SIM-R8). One Sim; n records of 20 bytes appended with WriteAt at
// the log's end, each followed by a Sync, as the 11-examples store does
// per Put; then a Restart and a read of the whole log, checked. With the
// dirty-page sync the cost is linear in n; the full-page-list sync it
// replaced made it quadratic. Usage: simwal <records> <seed>. Prints the
// record count and the sum of the log's bytes after the restart.

import std.fs
import std.sim

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    seed := Cast[u64](try sys.args[2].ParseInt())
    var out := sys.Stdout()
    var s := try sim.Sim.New(sys.arena, seed)
    io := s.Io()
    var f := fs.Create(io, "wal") else {
        s.Free()
        try Print(mut out, "create failed\n", ())
        return
    }
    var rec: [20]u8 = [0; 20]
    var ok := true
    for i in 0..n {
        for k in 0..20 {
            rec[k] = Trunc[u8](i + k)
        }
        _ = f.WriteAt(io, i64(i) * 20, rec[..]) else {
            ok = false
            break
        }
        f.Sync(io) else {
            ok = false
            break
        }
    }
    f.Abandon()
    io2 := s.Restart()
    var sum: u64 = 0
    var got: i64 = 0
    var g := fs.Open(io2, "wal") else {
        s.Free()
        try Print(mut out, "open failed\n", ())
        return
    }
    var buf: [65536]u8 = [0; 65536]
    for {
        m := g.Read(io2, mut buf[..]) else {
            break
        }
        if m == 0 {
            break
        }
        for k in 0..m {
            sum +%= u64(buf[k])
        }
        got += i64(m)
    }
    g.Abandon()
    s.Free()
    if !ok || got != i64(n) * 20 {
        try Print(mut out, "log short: {got} bytes\n", (got,))
        return
    }
    try Print(mut out, "records {n} sum {sum}\n", (n, sum))
}
