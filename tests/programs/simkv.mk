// simkv, the key-value store under std.sim with its crash test,
// copied from
// tests/bench-lanes/std-sim/simkv/mk/simkv.mk. A write-ahead-log
// key-value store (doc/examples.md's) runs three lives per seed on one simulated disk,
// crashing at a seeded point in the first two; every recovery is checked against the
// committed writes. Usage: simkv <seeds> <first seed>. Prints the seed count, the crashes and
// the mismatches (always 0). One seed gives one run on every target.

import std.fs
import std.sim

const Keys: int = 7
const Puts: int = 24
const RecSize: int = 20

struct Store {
    wal: fs.File
    len: i64
    values: [7]u64
    has: [7]bool
}

Check(key: u64, value: u64): u32 {
    var h: u32 = 0x811C9DC5
    for k in 0..8 {
        h = (h ^ Trunc[u32](key >> Trunc[u64](k * 8))) *% 0x01000193
        h = (h ^ Trunc[u32](value >> Trunc[u64](k * 8))) *% 0x01000193
    }
    return h
}

Encode(key: u64, value: u64, out: mut [20]u8) {
    c := Check(key, value)
    for k in 0..8 {
        out[k] = Trunc[u8](key >> Trunc[u64](k * 8))
        out[8 + k] = Trunc[u8](value >> Trunc[u64](k * 8))
    }
    for k in 0..4 {
        out[16 + k] = Trunc[u8](c >> Trunc[u32](k * 8))
    }
}

U64At(b: []u8, at: int): u64 {
    var x: u64 = 0
    for k in 0..8 {
        x = x | (u64(b[at + k]) << Trunc[u64](k * 8))
    }
    return x
}

Store.Open(a: &Arena, io: Io): fs.Error|OutOfMemory!Store {
    var values: [7]u64 = [0; 7]
    var has: [7]bool = [false; 7]
    var good := 0
    b := fs.ReadBytes(a, io, "db.wal") else e {
        match e {
            fs.NotFound {
            }
            _ {
                fail e
            }
        }
        return try Store.Fresh(io, values, has, 0, b"")
    }
    for good + RecSize <= b.Len() {
        key := U64At(b, good)
        value := U64At(b, good + 8)
        var c: u32 = 0
        for k in 0..4 {
            c = c | (u32(b[good + 16 + k]) << Trunc[u32](k * 8))
        }
        if c != Check(key, value) || key >= u64(Keys) {
            break
        }
        values[Trunc[int](key)] = value
        has[Trunc[int](key)] = true
        good += RecSize
    }
    return try Store.Fresh(io, values, has, good, b[0..good])
}

Store.Fresh(io: Io, values: [7]u64, has: [7]bool, good: int, prefix: []u8): fs.Error|OutOfMemory!Store {
    var f := try fs.Create(io, "db.tmp")
    onerr f.Abandon()
    try f.WriteAll(io, prefix)
    try f.Sync(io)
    try fs.Rename(io, "db.tmp", "db.wal")
    return Store(wal: f, len: i64(good), values: values, has: has)
}

Store.Put(mut self, io: Io, key: u64, value: u64): fs.Error!void {
    var rec: [20]u8 = [0; 20]
    Encode(key, value, mut rec)
    n := try self.wal.WriteAt(io, self.len, rec[..])
    if n != RecSize {
        fail fs.NoSpace
    }
    try self.wal.Sync(io)
    self.len += i64(RecSize)
    self.values[Trunc[int](key)] = value
    self.has[Trunc[int](key)] = true
}

Store.Close(self, io: Io) {
    Store(wal, ..) := self
    wal.Close(io) else {
        wal.Abandon()
    }
}

Verify(st: &Store, want: &[7]u64, wanted: &[7]bool): int {
    var bad := 0
    for k in 0..Keys {
        if wanted[k] && (!st.has[k] || st.values[k] != want[k]) {
            bad += 1
        }
        if !wanted[k] && st.has[k] {
            bad += 1
        }
    }
    return bad
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    first := Cast[int](try sys.args[2].ParseInt())
    var out := sys.Stdout()
    var crashed := 0
    var bad := 0
    var seeds := 0
    for seed in first..first + n {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        defer s.Free()
        var a := try sys.arena.Sub()
        defer a.Free()
        var want: [7]u64 = [0; 7]
        var wanted: [7]bool = [false; 7]
        var io := s.Io()
        for life in 0..3 {
            if life < 2 {
                s.CrashAfterRandomOps()
            }
            var store := Store.Open(&a, io) else {
                crashed += 1
                io = s.Restart()
                continue
            }
            bad += Verify(&store, &want, &wanted)
            if life < 2 {
                for i in 0..Puts {
                    key := Cast[u64]((seed + i * 3 + life) % Keys)
                    value := Cast[u64](seed * 1000 + life * 100 + i)
                    store.Put(io, key, value) else {
                        crashed += 1
                        break
                    }
                    want[Trunc[int](key)] = value
                    wanted[Trunc[int](key)] = true
                }
            }
            store.Close(io)
            io = s.Restart()
        }
        seeds += 1
    }
    try Print(mut out, "seeds {seeds} crashed {crashed} bad {bad}\n", (seeds, crashed, bad))
}
