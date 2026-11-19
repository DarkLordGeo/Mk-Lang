// Why: SystemPool keeps a pool for the process's life; each rule needs a test.
// What: a test cell (CellPool over a PEM file): one load for many tasks, and
// nothing kept on failure; SystemPool's own answers.
// How: a test build's leak check (exit 102) shows kept blocks are not
// counted; std/std-tls_syspool_wsl_test.go covers the Linux store.

import std.core
import std.fs
import std.mem
import std.raw
import std.rt
import std.sim
import std.time

// SpRoot is the Mk bench root of the TLS loopback benchmark (P-256).
const SpRoot: str = "-----BEGIN CERTIFICATE-----\nMIIBYzCCAQmgAwIBAgIBATAKBggqhkjOPQQDAjAYMRYwFAYDVQQDEw1NayBiZW5j\naCByb290MCAXDTAwMDEwMTAwMDAwMFoYDzIyMDAwMTAxMDAwMDAwWjAYMRYwFAYD\nVQQDEw1NayBiZW5jaCByb290MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEyTx+\nYlmJmAU6O4dZ0UQ3l5NkWBp3YM0DnNEBOBQ4TupVoFHlQRki7kyY6p+VX9Qrr6Hn\nz8thWyiM/gt642/2dqNCMEAwDgYDVR0PAQH/BAQDAgIEMA8GA1UdEwEB/wQFMAMB\nAf8wHQYDVR0OBBYEFCRUSRvKuJ4w64m9Xtlmi468lMZ5MAoGCCqGSM49BAMCA0gA\nMEUCIQCGfzbV5b0fQ7vvXGWBDlwaBxou4hsIMFapz+zbFYiqbQIgIH6Cu3lYtLDM\n5NakQ34Pul2rlQOLEFZvP0CmTMKU1NE=\n-----END CERTIFICATE-----\n"

// The test cells: one per block, so the blocks do not see each other's
// pools when a runner runs them in one process.
var SpManyCell: Mutex[PoolCell] = Mutex[PoolCell].New(PoolCell(pool: none, empty: none, loads: 0))
var SpEmptyCell: Mutex[PoolCell] = Mutex[PoolCell].New(PoolCell(pool: none, empty: none, loads: 0))
var SpCancelCell: Mutex[PoolCell] = Mutex[PoolCell].New(PoolCell(pool: none, empty: none, loads: 0))

// SpLoads is how many loads cell began.
SpLoads(cell: &Mutex[PoolCell], io: Io): int {
    g := cell.Lock(io.NoCancel()).Must()
    n := g.Ref().loads
    g.Unlock()
    return n
}

// SpAsk adds 1 to ok when cell's pool from p holds one certificate whose
// DER is der.
SpAsk(cell: &Mutex[PoolCell], h: &Heap, io: Io, p: str, der: []u8, ok: &raw.Atomic[u64]) {
    pool := CellPool(cell, h, h.Alloc(), io, p) else {
        return
    }
    if pool.certs.Len() == 1 && Same(pool.certs[0].data, der) {
        // Edge: the scope's join orders this Add before the block's Load.
        _ = ok.Add(1)
    }
}

// SpFails is whether the pool of cell from p (SystemPool's own when cell
// is SystemCell) fails with the error named want.
SpFails(cell: &Mutex[PoolCell], h: &Heap, io: Io, p: str, want: str): bool {
    if raw.Same(raw.Of(cell), raw.Of(&SystemCell)) {
        _ = SystemPool(h, io) else e {
            return SpIs(e, want)
        }
        return false
    }
    _ = CellPool(cell, h, h.Alloc(), io, p) else e {
        return SpIs(e, want)
    }
    return false
}

// SpIs is whether e is Canceled or Unsupported as want names.
SpIs(e: Error, want: str): bool {
    return match e {
        Canceled { want == "Canceled" }
        Unsupported { want == "Unsupported" }
        OutOfMemory { want == "OutOfMemory" }
        _ { false }
    }
}

// SpFile writes text to a new file of the system's temporary directory,
// named name and 16 random hexadecimal digits (fs.CreateTemp), so two
// runs at once never share one, and is its path.
SpFile(a: &Arena, io: Io, name: str, text: str): str from a {
    (file, path) := fs.CreateTemp(a, io, "", name).Must()
    var f := file
    f.WriteAll(io, text.AsBytes()).Must()
    f.Close(io).Must()
    return path
}

test "SystemPool-Cell sixteen tasks make one load, and the kept pool outlives the file and the load's arena" (sys: &System) {
    io := sys.io
    a := sys.arena
    p := SpFile(a, io, "mk-x509-syspool-many-", SpRoot)
    want := ParsePem(a, SpRoot).Must()
    var ok := raw.Atomic[u64].New(0)
    scope sc := io.Scope() {
        for _ in 0..16 {
            sc.go SpAsk(&SpManyCell, sys.heap, io, p, want[0].data, &ok)
        }
    }
    Assert(ok.Load() == 16)
    Assert(SpLoads(&SpManyCell, io) == 1)
    // The file is gone: the kept pool is still answered, with no load.
    fs.Remove(io, p).Must()
    pool := CellPool(&SpManyCell, sys.heap, sys.heap.Alloc(), io, p).Must()
    Assert(SpLoads(&SpManyCell, io) == 1)
    Assert(pool.certs.Len() == 1 && Same(pool.certs[0].data, want[0].data))
    // The copy is a whole certificate: its parts read as the original's.
    Assert(pool.certs[0].Subject(a).Must() == want[0].Subject(a).Must())
    Assert(pool.certs[0].NotAfter().Unix() == want[0].NotAfter().Unix())
}

test "SystemPool-Cell a load with no certificate keeps nothing, so each call loads again" (sys: &System) {
    io := sys.io
    p := SpFile(sys.arena, io, "mk-x509-syspool-empty-", "no certificate here\n")
    one := CellPool(&SpEmptyCell, sys.heap, sys.heap.Alloc(), io, p).Must()
    two := CellPool(&SpEmptyCell, sys.heap, sys.heap.Alloc(), io, p).Must()
    Assert(one.certs.Len() == 0 && two.certs.Len() == 0 && !one.system)
    Assert(SpLoads(&SpEmptyCell, io) == 2)
    // The store appears: the next call keeps it.
    fs.WriteAll(io, p, SpRoot.AsBytes()).Must()
    three := CellPool(&SpEmptyCell, sys.heap, sys.heap.Alloc(), io, p).Must()
    four := CellPool(&SpEmptyCell, sys.heap, sys.heap.Alloc(), io, p).Must()
    Assert(three.certs.Len() == 1 && four.certs.Len() == 1)
    Assert(SpLoads(&SpEmptyCell, io) == 3)
    fs.Remove(io, p).Must()
}

test "SystemPool-Cell a cancelled call is Canceled and keeps nothing; the next call loads" (sys: &System) {
    io := sys.io
    p := SpFile(sys.arena, io, "mk-x509-syspool-cancel-", SpRoot)
    var canceled := false
    scope sc := io.Scope() {
        sc.Cancel()
        canceled = SpFails(&SpCancelCell, sys.heap, io, p, "Canceled")
    }
    Assert(canceled)
    Assert(SpLoads(&SpCancelCell, io) == 0)
    pool := CellPool(&SpCancelCell, sys.heap, sys.heap.Alloc(), io, p).Must()
    Assert(pool.certs.Len() == 1 && SpLoads(&SpCancelCell, io) == 1)
    fs.Remove(io, p).Must()
}

test "SystemPool answers the same pool twice with one load on Windows; under a simulated or taped Io it is Unsupported" (sys: &System) {
    io := sys.io
    one := SystemPool(sys.heap, io).Must()
    two := SystemPool(sys.heap, io).Must()
    Assert(one.certs.Len() == two.certs.Len() && one.system == two.system)
    if Target.os == core.Os.Windows {
        Assert(one.system && one.certs.Len() == 0)
        Assert(SpLoads(&SystemCell, io) == 1)
    } else if one.certs.Len() > 0 {
        Assert(SpLoads(&SystemCell, io) == 1)
    }
    if one.system || one.certs.Len() > 0 {
        // Kept: a NoCancel copy of the native Io is native too, and gets
        // the same block.
        Assert(raw.Same(raw.Of(one), raw.Of(two)))
        Assert(raw.Same(raw.Of(one), raw.Of(SystemPool(sys.heap, io.NoCancel()).Must())))
    }
    // A Sim's Io and its NoCancel copy, a Recorder's over the native Io
    // and its NoCancel copy, and the Replayer of that recording: each is
    // Unsupported, and the recording replays without diverging.
    var s := sim.Sim.New(sys.arena, 1).Must()
    Assert(SpFails(&SystemCell, sys.heap, s.Io(), "", "Unsupported"))
    Assert(SpFails(&SystemCell, sys.heap, s.Io().NoCancel(), "", "Unsupported"))
    s.Free()
    var rec := sim.Recorder.New(io).Must()
    Assert(SpFails(&SystemCell, sys.heap, rec.Io(), "", "Unsupported"))
    Assert(SpFails(&SystemCell, sys.heap, rec.Io().NoCancel(), "", "Unsupported"))
    log := rec.Log(sys.arena).Must()
    rec.Free()
    var rep := sim.Replayer.New(log).Must()
    Assert(SpFails(&SystemCell, sys.heap, rep.Io(), "", "Unsupported"))
    Assert(SpFails(&SystemCell, sys.heap, rep.Io().NoCancel(), "", "Unsupported"))
    Assert(!rep.Diverged())
    rep.Free()
}

// SpRefused is whether the copy step of pool on keep was refused, with
// nothing left on keep (Live 0); when it was not, the one block it made
// (Live 1) is freed again before it returns.
SpRefused(keep: &Alloc, c: &mem.Counting, pool: &Pool): bool {
    copy := MakeCopy(keep, pool) else {
        Assert(c.Live() == 0)
        return true
    }
    Assert(c.Live() == 1)
    FreeCopy(copy)
    Assert(c.Live() == 0)
    return false
}

test "SystemPool-Cell a copy its allocator refuses leaves nothing allocated" (sys: &System) {
    a := sys.arena
    certs := ParsePem(a, SpRoot).Must().Done()
    pool := Pool(certs: certs[..], system: false)
    var refused := 0
    var made := 0
    var cap := 0
    for cap <= 2048 {
        var buf: [2056]u8 = [0; 2056]
        f := mem.Fixed.New(mut buf[0..cap])
        c := mem.Counting.New(f.Alloc())
        if SpRefused(c.Alloc(), &c, &pool) {
            refused += 1
        } else {
            made += 1
        }
        cap += 8
    }
    // Refusals while the block does not fit, then copies that do.
    Assert(refused > 3 && made > 0)
}

// SpOom is whether cell's pool from p, copied into keep, fails with
// OutOfMemory.
SpOom(cell: &Mutex[PoolCell], h: &Heap, keep: &Alloc, io: Io, p: str): bool {
    _ = CellPool(cell, h, keep, io, p) else e {
        return SpIs(e, "OutOfMemory")
    }
    return false
}

// SpChain is two certificates: the Mk bench leaf (localhost) and SpRoot.
const SpChain: str = "-----BEGIN CERTIFICATE-----\nMIIBfDCCASGgAwIBAgIBAjAKBggqhkjOPQQDAjAYMRYwFAYDVQQDEw1NayBiZW5j\naCByb290MCAXDTAwMDEwMTAwMDAwMFoYDzIyMDAwMTAxMDAwMDAwWjAUMRIwEAYD\nVQQDEwlsb2NhbGhvc3QwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAARYWAiT0sIB\nPFPoIQColXkYt09CRdmx6LjiHDj75/IQjS3AYB3CWa+ymGXqR2ucu2cguSiCoiNK\nqIH+y9bVqzPco14wXDAOBgNVHQ8BAf8EBAMCB4AwEwYDVR0lBAwwCgYIKwYBBQUH\nAwEwHwYDVR0jBBgwFoAUJFRJG8q4njDrib1e2WaLjryUxnkwFAYDVR0RBA0wC4IJ\nbG9jYWxob3N0MAoGCCqGSM49BAMCA0kAMEYCIQD4NROoBKH8KfhkQ/zOMONC60gR\ncRjTYRvpycsxggV3OgIhAKjdsY7ozxVLBHMGzKcJDmUfhRo7ejfd4qjWiC3mdn+n\n-----END CERTIFICATE-----\n-----BEGIN CERTIFICATE-----\nMIIBYzCCAQmgAwIBAgIBATAKBggqhkjOPQQDAjAYMRYwFAYDVQQDEw1NayBiZW5j\naCByb290MCAXDTAwMDEwMTAwMDAwMFoYDzIyMDAwMTAxMDAwMDAwWjAYMRYwFAYD\nVQQDEw1NayBiZW5jaCByb290MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEyTx+\nYlmJmAU6O4dZ0UQ3l5NkWBp3YM0DnNEBOBQ4TupVoFHlQRki7kyY6p+VX9Qrr6Hn\nz8thWyiM/gt642/2dqNCMEAwDgYDVR0PAQH/BAQDAgIEMA8GA1UdEwEB/wQFMAMB\nAf8wHQYDVR0OBBYEFCRUSRvKuJ4w64m9Xtlmi468lMZ5MAoGCCqGSM49BAMCA0gA\nMEUCIQCGfzbV5b0fQ7vvXGWBDlwaBxou4hsIMFapz+zbFYiqbQIgIH6Cu3lYtLDM\n5NakQ34Pul2rlQOLEFZvP0CmTMKU1NE=\n-----END CERTIFICATE-----\n"

var SpOomCell: Mutex[PoolCell] = Mutex[PoolCell].New(PoolCell(pool: none, empty: none, loads: 0))

test "SystemPool-Cell CellPool's refused copy fails OutOfMemory, frees the arena, unlocks and keeps nothing; the next call keeps" (sys: &System) {
    io := sys.io
    p := SpFile(sys.arena, io, "mk-x509-syspool-oom-", SpRoot)
    var buf: [8]u8 = [0; 8]
    f := mem.Fixed.New(mut buf[0..0])
    c := mem.Counting.New(f.Alloc())
    live := rt.LiveAllocations()
    Assert(SpOom(&SpOomCell, sys.heap, c.Alloc(), io, p) && c.Live() == 0)
    // The arena went back: the process heap holds what it held before.
    Assert(rt.LiveAllocations().count == live.count)
    // Unlocked, and nothing kept: the next call loads and keeps.
    g := SpOomCell.Lock(io).Must()
    Assert(g.Ref().pool.IsNone() && g.Ref().loads == 1)
    g.Unlock()
    pool := CellPool(&SpOomCell, sys.heap, sys.heap.Alloc(), io, p).Must()
    Assert(pool.certs.Len() == 1 && SpLoads(&SpOomCell, io) == 2)
    fs.Remove(io, p).Must()
}

// SpChurn holds depth arenas of h at once, each filled with n slices of
// 24 bytes of 'X', then frees them. The load's DER sat at the start of
// its arena's first 4 KiB chunk, so only small slices land in such a
// chunk (a 4,096-byte slice goes to a larger one), and the freed chunk
// waits behind other free blocks of its class, so arenas made and freed
// one at a time keep reusing the same block: several held at once reach
// it. With the DER left in the
// load's arena (MakeCopy not rebasing it), this churn overwrites it, and
// the block fails at the first Same below in dev and release even
// without KeptInBlock. O(depth * n).
SpChurn(h: &Heap, depth: int, n: int) {
    if depth == 0 {
        return
    }
    var x := Arena.New(h).Must()
    for _ in 0..n {
        s := x.Slice[u8](24).Must()
        for i in 0..s.Len() {
            s[i] = 0x58
        }
    }
    SpChurn(h, depth - 1, n)
    x.Free()
}

var SpKeptCell: Mutex[PoolCell] = Mutex[PoolCell].New(PoolCell(pool: none, empty: none, loads: 0))

test "SystemPool-Cell a kept pool's references all lie in its block, and survive the load's arena freed and overwritten" (sys: &System) {
    io := sys.io
    a := sys.arena
    p := SpFile(a, io, "mk-x509-syspool-kept-", SpChain)
    want := ParsePem(a, SpChain).Must()
    pool := CellPool(&SpKeptCell, sys.heap, sys.heap.Alloc(), io, p).Must()
    // The load's arena was freed in CellPool; arenas made now take its
    // memory again and fill it, on the real pool.
    // This comes before any call that can move the task to another
    // carrier, whose allocator would likely not hand out the freed chunk
    // (not shown: with the churn after fs.Remove an unrebased copy passed
    // the overwrite check in some runs at 16 carriers). The overwrite
    // depends on std.rt's heap (its per-carrier cache and size classes);
    // KeptInBlock below does not, and catches an unrebased copy alone.
    SpChurn(sys.heap, 16, 20000)
    fs.Remove(io, p).Must()
    // KeepCopy's premise: the certificates and every DER are inside the
    // block, so the Pool references nothing else.
    Assert(pool.certs.Len() == want.Len() && KeptInBlock(pool))
    // CopyAlign is enough for both kinds of value the block holds.
    Assert(AlignOf[Certificate]() <= CopyAlign && AlignOf[Pool]() <= CopyAlign)
    for i in 0..want.Len() {
        k := &pool.certs[i]
        Assert(Same(k.data, want[i].data))
        Assert(k.Subject(a).Must() == want[i].Subject(a).Must())
        Assert(k.Issuer(a).Must() == want[i].Issuer(a).Must())
        Assert(k.NotAfter().Unix() == want[i].NotAfter().Unix())
    }
}

// Concurrency blocks: waiters parked behind a holder whose load
// keeps nothing, cancelled waiters, then one load.

var RvCell: Mutex[PoolCell] = Mutex[PoolCell].New(PoolCell(pool: none, empty: none, loads: 0))
var RvCell2: Mutex[PoolCell] = Mutex[PoolCell].New(PoolCell(pool: none, empty: none, loads: 0))

// RvCancelled adds 1 to n when cell's pool from p fails with Canceled.
RvCancelled(cell: &Mutex[PoolCell], h: &Heap, io: Io, p: str, n: &raw.Atomic[u64]) {
    _ = CellPool(cell, h, h.Alloc(), io, p) else e {
        if SpIs(e, "Canceled") {
            _ = n.Add(1)
        }
        return
    }
}

test "SystemPool-Waiters: tasks behind a holder that keeps nothing load once after it; cancelled waiters load nothing" (sys: &System) {
    io := sys.io
    a := sys.arena
    p := SpFile(a, io, "mk-x509-review-", SpRoot)
    want := ParsePem(a, SpRoot).Must()
    var ok := raw.Atomic[u64].New(0)
    var cancelled := raw.Atomic[u64].New(0)
    // The test holds the lock, as a load in progress would.
    var g := RvCell.Lock(io).Must()
    scope sc := io.Scope() {
        for _ in 0..32 {
            sc.go SpAsk(&RvCell, sys.heap, io, p, want[0].data, &ok)
        }
        scope inner := io.Scope() {
            for _ in 0..8 {
                inner.go RvCancelled(&RvCell, sys.heap, io, p, &cancelled)
            }
            io.Sleep(time.Millis(20)) else {}
            inner.Cancel()
        }
        Assert(cancelled.Load() == 8)
        io.Sleep(time.Millis(20)) else {}
        // The holder's load failed: it keeps nothing and unlocks.
        Assert(g.Ref().pool.IsNone() && g.Ref().loads == 0)
        g.Unlock()
    }
    Assert(ok.Load() == 32)
    Assert(SpLoads(&RvCell, io) == 1)
    fs.Remove(io, p).Must()
}

test "SystemPool-Waiters: sixty-four first calls on one cell make one load" (sys: &System) {
    io := sys.io
    a := sys.arena
    p := SpFile(a, io, "mk-x509-review2-", SpRoot)
    want := ParsePem(a, SpRoot).Must()
    var ok := raw.Atomic[u64].New(0)
    var cancelled := raw.Atomic[u64].New(0)
    scope sc := io.Scope() {
        for _ in 0..32 {
            sc.go SpAsk(&RvCell2, sys.heap, io, p, want[0].data, &ok)
            sc.go RvCancelled(&RvCell2, sys.heap, io, p, &cancelled)
        }
    }
    Assert(ok.Load() == 32 && cancelled.Load() == 0)
    Assert(SpLoads(&RvCell2, io) == 1)
    fs.Remove(io, p).Must()
}
