// Why: A reader must never see half an image, even when the program dies
// mid-write.
//
// What: Writing an image: to standard output, or to a file replaced atomically
// (written beside it, synced, then renamed over it).
//
// How: Under `--seed` the write runs on a simulated disk instead, with a crash
// at a seeded point or an injected fault, and the result is checked to be the
// whole image or nothing.

import std.fs
import std.sim

/// The simulated disk left part of the image at the output path.
error Torn

/// Writes `data` to `path` through `tmp`, replacing `path` only once the
/// data is synced. Fails with the file system's error; `tmp` is removed on a
/// failure after it was created. O(n).
WriteAtomic(io: Io, path: str, tmp: str, data: []u8): fs.Error!void {
    var f := try fs.Create(io, tmp)
    onerr f.Abandon()
    onerr fs.Remove(io, tmp) else {}
    try f.WriteAll(io, data)
    try f.Sync(io)
    try f.Close(io)
    try fs.Rename(io, tmp, path)
}

/// Writes the image where the options say: standard output for `-`, else
/// the file. O(n); allocates the temporary name in `a`.
Save(sys: &System, a: &Arena, path: str, data: []u8): fs.Error|OutOfMemory|WriteError!void {
    if path == "-" {
        var out := sys.Stdout()
        try out.Bytes(data)
        return
    }
    tmp := try a.Fmt("{path}.tmp", (path))
    try WriteAtomic(sys.io, path, tmp, data)
}

/// Runs the write on a simulated disk seeded by `seed`. With `fault`, the
/// next write fails with it and the error is returned; without, the disk
/// crashes after a seeded number of operations and restarts, and the output
/// must then hold the whole image or nothing (`Torn` otherwise). O(n).
Simulate(a: &Arena, seed: u64, fault: ?sim.Fault, path: str, data: []u8): Torn|fs.Error|OutOfMemory!void {
    var s := try sim.Sim.New(a, seed)
    defer s.Free()
    tmp := try a.Fmt("{path}.tmp", (path))
    if f := fault {
        s.FailNext(sim.Op.Write, f)
        try WriteAtomic(s.Io(), path, tmp, data)
        return
    }
    s.CrashAfterRandomOps()
    WriteAtomic(s.Io(), path, tmp, data) else {}
    io := s.Restart()
    back := fs.ReadBytes(a, io, path) else e {
        match e {
            fs.NotFound {
                return
            }
            _ {
                fail e
            }
        }
    }
    if back.Len() != data.Len() {
        fail Torn
    }
    for i, b in back {
        if b != data[i] {
            fail Torn
        }
    }
}
