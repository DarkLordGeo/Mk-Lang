// verdict: reject E1101
// rule: D-516
// note: every call of fs.Map outside an unsafe block is E1101, in a method and in a plain function, whether the call is under try or else
import std.fs

struct Snapshot {
    path: str
}

Snapshot.Len(&self, io: Io, f: &fs.File): fs.Error!int {
    m := try fs.Map(io, f, 0, 4_096) // <- E1101
    n := m.Bytes().Len()
    m.Unmap()
    return n
}

Count(io: Io, f: &fs.File): int {
    m := fs.Map(io, f, 0, 64) else { // <- E1101
        return 0
    }
    n := m.Bytes().Len()
    m.Unmap()
    return n
}
