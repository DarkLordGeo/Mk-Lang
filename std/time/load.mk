// Why: programs name zones ("Europe/Berlin"), and the zone data lives in
// different places on each system, or in the program itself.
//
// What: Zone.Load, Database, and the file and environment helpers
// ReadZoneFile, FindZoneFile, EnvValue and Simulated.
//
// How: Zone.Load checks the name, then looks in TZDIR, in
// /usr/share/zoneinfo on Linux, then in the Database the program passes; it
// never falls back to UTC. A Database is a lookup function, so a program
// links std.time.tzdata's embedded zones only when it names
// tzdata.Database. Files are read through the Io's own open, size and read
// entries, so a Sim's empty disk finds nothing.

import std.core
import std.rt

/// Zones built into the program: std.time.tzdata's Database, or one of the
/// program's own.
export struct Database {
    /// The TZif bytes of the zone named name, none when it has no such
    /// zone. Called only with a name Zone.Load has checked.
    export find: fn(str): ?[]u8
}

// ZoneDir is the system's zone database on Linux.
const ZoneDir: str = "/usr/share/zoneinfo"

// MaxPath is the longest directory and name Load joins; a longer TZDIR is
// skipped.
const MaxPath: int = 4096

/// Whether name is a zone name: one or more segments of ASCII letters,
/// digits, '.', '_', '+' and '-' joined by '/', no segment "." or "..", at
/// most 255 bytes. O(n); allocates nothing.
ValidName(name: str): bool {
    b := name.AsBytes()
    if b.Len() == 0 || b.Len() > 255 {
        return false
    }
    var seg := 0
    for i in 0..b.Len() + 1 {
        if i == b.Len() || b[i] == b'/' {
            n := i - seg
            if n == 0 || (n == 1 && b[seg] == b'.') || (n == 2 && b[seg] == b'.' && b[seg + 1] == b'.') {
                return false
            }
            seg = i + 1
            continue
        }
        c := b[i]
        if !IsAlpha(c) && !IsDigit(c) && c != b'.' && c != b'_' && c != b'+' && c != b'-' {
            return false
        }
    }
    return true
}

// The outcomes of reading one place, and Nowhere, a search's outcome when
// no place to look exists.
const Found: u8 = 0
const Absent: u8 = 1
const Unreadable: u8 = 2
const Nowhere: u8 = 3

// Read is a file's bytes, or why there are none.
struct Read {
    how: u8
    data: []u8
}

// ReadZoneFile reads the file at path into a: Absent when there is no
// such file (or it is a directory), Unreadable when it cannot be read, is
// over MaxTzif, or grew after its size was read. Canceled passes through.
// The handle is released on every path. O(n); allocates the file's size
// plus one byte, the byte that shows a file that grew.
ReadZoneFile(a: &Arena, io: Io, path: str): Canceled|OutOfMemory!Read from a {
    o := core.IoOpen(io, path, 0)
    if o.err == core.SysErr.Canceled {
        fail Canceled
    }
    if o.err == core.SysErr.NotFound || o.err == core.SysErr.NotDirectory || o.err == core.SysErr.IsDirectory || o.err == core.SysErr.InvalidPath {
        return Read(how: Absent, data: NoBytes)
    }
    if o.err != core.SysErr.None {
        return Read(how: FailedHow(io, path), data: NoBytes)
    }
    h := Trunc[uint](o.value)
    defer rt.Abandon(h)
    sz := core.IoSize(io, h)
    if sz.err != core.SysErr.None {
        return Read(how: FailedHow(io, path), data: NoBytes)
    }
    if sz.value < 0 || sz.value > Cast[i64](MaxTzif) {
        return Read(how: Unreadable, data: NoBytes)
    }
    size := Cast[int](sz.value)
    buf := try a.Slice[u8](size + 1)
    var n := 0
    for n < buf.Len() {
        r := core.IoRead(io, h, mut buf[n..], Cast[i64](n))
        if r.err == core.SysErr.Canceled {
            fail Canceled
        }
        if r.err != core.SysErr.None {
            return Read(how: FailedHow(io, path), data: NoBytes)
        }
        if r.value < 0 || r.value > Cast[i64](buf.Len() - n) {
            return Read(how: Unreadable, data: NoBytes)
        }
        if r.value == 0 {
            break
        }
        n += Cast[int](r.value)
    }
    if n > size {
        return Read(how: Unreadable, data: NoBytes)
    }
    if n == 0 && FailedHow(io, path) == Absent {
        // A directory the runtime opened and read as empty.
        return Read(how: Absent, data: NoBytes)
    }
    return Read(how: Found, data: buf[0..n])
}

// FailedHow is the outcome of an open or read of path that failed: Absent
// when path is a directory (Windows refuses to open one as a file, and
// Linux to read one), else Unreadable.
FailedHow(io: Io, path: str): u8 {
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    s := core.IoStatPath(io, path, mut st)
    return if s.err == core.SysErr.None && st.kind == core.IoKindDir { Absent } else { Unreadable }
}

// NoBytes is the empty data of a Read that found nothing.
const NoBytes: []u8 = []

// ReadUnder reads the file of zone name under dir, Absent when the path
// would be longer than MaxPath.
ReadUnder(a: &Arena, io: Io, dir: str, name: str): Canceled|OutOfMemory!Read from a {
    if dir.Len() + 1 + name.Len() > MaxPath {
        return Read(how: Absent, data: NoBytes)
    }
    path := try a.Fmt("{dir}/{name}", (dir: dir, name: name))
    return try ReadZoneFile(a, io, path)
}

// IsDir is whether path is a directory. Canceled passes through.
IsDir(io: Io, path: str): Canceled!bool {
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    s := core.IoStatPath(io, path, mut st)
    if s.err == core.SysErr.Canceled {
        fail Canceled
    }
    return s.err == core.SysErr.None && st.kind == core.IoKindDir
}

// FindZoneFile reads the file of zone name, a ValidName, from the first
// place that has it: TZDIR when it is set, then /usr/share/zoneinfo on
// Linux. Found or Unreadable from that place; Absent when no place has it;
// Nowhere when no place is a directory, or io is simulated: a simulation
// reads no zone database of the host, so it makes no operation on any
// target and a seed's run does not depend on the target. A place
// is checked for being a directory only after it came back Absent, so a
// found zone costs no extra call. O(n) in the file's bytes; allocates the
// path and the file in a.
FindZoneFile(a: &Arena, io: Io, name: str): Canceled|OutOfMemory!Read from a {
    if Simulated(io) {
        return Read(how: Nowhere, data: NoBytes)
    }
    var looked := false
    dir := try EnvOf(a, io, "TZDIR")
    if dir.Len() > 0 {
        r := try ReadUnder(a, io, dir, name)
        if r.how != Absent {
            return r
        }
        looked = try IsDir(io, dir)
    }
    if Target.os == Os.Linux {
        r := try ReadUnder(a, io, ZoneDir, name)
        if r.how != Absent {
            return r
        }
        if !looked {
            looked = try IsDir(io, ZoneDir)
        }
    }
    return Read(how: if looked { Absent } else { Nowhere }, data: NoBytes)
}

/// The zone named name, read as TZif: from TZDIR when it is set, then from
/// /usr/share/zoneinfo on Linux, then from embedded; under a simulated Io
/// from embedded alone, on every target. Fails with BadName
/// when name is not a zone name (checked before any file is opened);
/// NoDatabase when there was nowhere to look (TZDIR unset or not a
/// directory, no /usr/share/zoneinfo directory or not Linux, and no
/// embedded Database); NotFound when no place has the zone; BadData when
/// the first place that has it holds bad data, a file over 1 MiB or one it
/// cannot read; Canceled when the scope is cancelled; OutOfMemory when a
/// cannot hold the zone. Never falls back to UTC. O(n) in the zone's bytes;
/// allocates its transitions in a, and the file's bytes while it is read,
/// freed before it returns.
export Zone.Load(a: &Arena, io: Io, name: str, embedded: ?Database): Error|OutOfMemory!Zone from a {
    if !ValidName(name) {
        fail BadName
    }
    var tmp := try a.Sub()
    defer tmp.Free()
    r := try FindZoneFile(&tmp, io, name)
    if r.how == Found {
        return try Zone.FromTzif(a, name, r.data)
    }
    if r.how == Unreadable {
        fail BadData
    }
    match embedded {
        some(db) {
            data := db.find(name) else {
                fail NotFound
            }
            return try Zone.FromTzif(a, name, data)
        }
        none {}
    }
    if r.how == Nowhere {
        fail NoDatabase
    }
    fail NotFound
}

// EnvValue is the value of the process's environment variable name copied
// into a, none when it is unset or io is a simulated one (a Sim's or a
// Tape's flags carry a token), so a simulation reads no
// variable of the real process. A variable set to "" is some("").
EnvValue(a: &Arena, io: Io, name: str): OutOfMemory!?str from a {
    if Simulated(io) {
        return none
    }
    sys := core.NewSystem(rt.Args(), rt.NativeIo(), a, rt.ProcessHeap(), rt.Proc())
    v := sys.Env(name) else {
        return none
    }
    return some(try a.Dup(v))
}

// EnvOf is EnvValue, "" when the variable is unset.
EnvOf(a: &Arena, io: Io, name: str): OutOfMemory!str from a {
    match try EnvValue(a, io, name) {
        some(v) {
            return v
        }
        none {}
    }
    return ""
}

// SimBits are the flags' token bits (1 to 31) and slot bits (48 to 63);
// on the native table bits 48 to 63 name a pinned carrier instead
// (runtime.Dedicated), so only a non-native Io's bits say simulated.
const SimBits: uint = 0xFFFF_0000_FFFF_FFFE

// Simulated is whether io is a simulated one, a Sim's or a Tape's: its
// flags carry a token.
Simulated(io: Io): bool {
    return (!core.IsNative(io) && (core.IoFlags(io) & SimBits) != 0)
}
