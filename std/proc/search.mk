// Why: on Windows the OS's own search would find a program in the current
// directory, so std finds the program itself before the runtime starts it;
// the search reads the file system through the Io, so it lives in Mk.
//
// What: FindWindows, the PATH and PATHEXT search, with SpawnSearched and
// SpawnJoined, which start the path found or a relative path joined to dir,
// and their helpers (HasSeparator, HasExt, Join, IsProgram, CandLen).
//
// How: a name with no separator is looked for in PATH only, with PATHEXT,
// never in the current directory, and the path found is what the runtime
// starts. Go's exec.LookPath is the reference (its PATHEXT rule and default
// list; it skips empty entries), with one difference: a relative entry of
// PATH, which LookPath answers with ErrDot, is skipped here, as the Linux
// runtime skips it (std.sys's FindProgram). On Linux the runtime does the
// search itself before its vfork, since that search reads no file through
// the Io.

import std.core
import std.fs
import std.rt

// CandBytes bounds one candidate path: Windows' 32,767 UTF-16 units, at
// most 3 bytes of UTF-8 each.
const CandBytes: int = 98304

// HasSeparator: name names a path (a slash, a backslash or a drive
// colon), not a program to search for.
HasSeparator(name: str): bool {
    for b in name.AsBytes() {
        if b == 47 || b == 92 || b == 58 {
            return true
        }
    }
    return false
}

// HasExt: the name holds a dot (it has no separator), so it is tried as
// written before PATHEXT's extensions are appended.
HasExt(name: str): bool {
    for b in name.AsBytes() {
        if b == 46 {
            return true
        }
    }
    return false
}

// IsAbsolute: p starts with a drive and a separator (C:\ or C:/) or with
// two separators (a UNC path).
IsAbsolute(p: []u8): bool {
    if p.Len() >= 3 && p[1] == 58 && (p[2] == 92 || p[2] == 47) {
        c := p[0] | 0x20
        return c >= 97 && c <= 122
    }
    return p.Len() >= 2 && (p[0] == 92 || p[0] == 47) && (p[1] == 92 || p[1] == 47)
}

// ListEnd is the index of the ';' that ends the PATH entry starting at
// start in p, or p.Len(): a ';' between double quotes is part of the
// entry (Go's filepath.SplitList on Windows), so a quoted entry is one
// directory, never two.
ListEnd(p: []u8, start: int): int {
    var quo := false
    var i := start
    for i < p.Len() {
        if p[i] == 34 {
            quo = !quo
        } else if p[i] == 59 && !quo {
            return i
        }
        i += 1
    }
    return i
}

// Join writes dir (its quotes removed) and name, with one backslash
// between them, to buf: the bytes written, or -1 when dir is not an
// absolute path or the two do not fit.
Join(buf: mut []u8, dir: []u8, name: str): int {
    var n := 0
    for b in dir {
        if b == 34 {
            continue
        }
        if n == buf.Len() {
            return -1
        }
        buf[n] = b
        n += 1
    }
    if !IsAbsolute(buf[0..n]) {
        return -1
    }
    if buf[n - 1] != 92 && buf[n - 1] != 47 {
        if n == buf.Len() {
            return -1
        }
        buf[n] = 92
        n += 1
    }
    nb := name.AsBytes()
    if n + nb.Len() > buf.Len() {
        return -1
    }
    (mut buf[n..n + nb.Len()]).CopyFrom(nb)
    return n + nb.Len()
}

// IsProgram: p names something that is not a directory (exec.LookPath's
// chkStat). Canceled in a cancelled scope, Unsupported under an Io with
// no file system (Spawn never searches under an Io that is not the OS's);
// any other failure of the stat is "no".
IsProgram(io: Io, p: []u8): Error!bool {
    s := str.From(p) else {
        return false
    }
    info := fs.Stat(io, s) else e {
        match e {
            Canceled { fail Canceled }
            fs.Unsupported { fail Unsupported }
            _ { return false }
        }
    }
    return info.kind != fs.Kind.Dir
}

// AddExt writes the PATHEXT entry e after the n bytes of a candidate in
// buf, with a dot before it when e has none (exec.LookPath's pathExt): the
// candidate's new length, or -1 when it does not fit.
AddExt(buf: mut []u8, n: int, e: str): int {
    var k := n
    if e.Byte(0) != 46 {
        if k == buf.Len() {
            return -1
        }
        buf[k] = 46
        k += 1
    }
    if k + e.Len() > buf.Len() {
        return -1
    }
    (mut buf[k..k + e.Len()]).CopyFrom(e.AsBytes())
    return k + e.Len()
}

// AnyExt: exts, PATHEXT's value, names at least one extension.
AnyExt(exts: str): bool {
    for e in exts.Split(";") {
        if e.Len() > 0 {
            return true
        }
    }
    return false
}

// CandLen is the most bytes a candidate of FindWindows can take: the
// longest entry of the PATH bytes pb (its quotes counted, which Join
// drops), a backslash, name, a dot and the longest of PATHEXT's exts;
// at most CandBytes. O(PATH + PATHEXT); allocates nothing.
CandLen(pb: []u8, name: str, exts: str): int {
    var entry := 0
    var start := 0
    for start <= pb.Len() {
        end := ListEnd(pb, start)
        if end - start > entry {
            entry = end - start
        }
        start = end + 1
    }
    var ext := 0
    for e in exts.Split(";") {
        if e.Len() > ext {
            ext = e.Len()
        }
    }
    n := entry + 1 + name.Len() + 1 + ext
    return if n > CandBytes { CandBytes } else { n }
}

// FindWindows is the path of the program name (no separator) in PATH, in
// x: for each absolute entry, in order (a ';' inside double quotes does
// not end an entry), dir\name when name has an extension, or PATHEXT names
// none, and is a program there, else dir\name with each of PATHEXT's
// extensions, a dot put before one written without it
// (".com;.exe;.bat;.cmd" when PATHEXT is unset or empty): exec.LookPath's
// rules. NotFound when no entry has it; Canceled in a cancelled scope;
// OutOfMemory when x cannot give the candidate's buffer. O(entries x
// extensions) stats; allocates one candidate buffer in x, CandLen bytes
// (the longest PATH entry, the name and the longest PATHEXT extension,
// plus 2).
FindWindows(x: &Arena, io: Io, name: str): Error!str from x {
    sys := core.NewSystem(rt.Args(), rt.NativeIo(), x, rt.ProcessHeap(), rt.Proc())
    path := sys.Env("PATH") else {
        fail NotFound
    }
    var exts := sys.Env("PATHEXT") else { "" }
    if exts.Len() == 0 {
        exts = ".com;.exe;.bat;.cmd"
    }
    pb := path.AsBytes()
    var buf := try x.Slice[u8](CandLen(pb, name, exts))
    asis := HasExt(name) || !AnyExt(exts)
    var start := 0
    for start <= pb.Len() {
        end := ListEnd(pb, start)
        n := Join(mut buf[..], pb[start..end], name)
        start = end + 1
        if n < 0 {
            continue
        }
        if asis && try IsProgram(io, buf[0..n]) {
            return str.From(buf[0..n]) else { fail NotFound }
        }
        for e in exts.Split(";") {
            if e.Len() == 0 {
                continue
            }
            k := AddExt(mut buf[..], n, e)
            if k < 0 {
                continue
            }
            if try IsProgram(io, buf[0..k]) {
                return str.From(buf[0..k]) else { fail NotFound }
            }
        }
    }
    fail NotFound
}

// SpawnSearched is rt.Spawn of args with args[0] replaced by the path
// FindWindows finds for it, built in a sub-arena of a freed before it
// returns.
SpawnSearched(a: &Arena, io: Io, args: []str, vars: []str, dir: str, s: mut core.IoSpawnReq, named: []uint): Error!core.IoResult {
    var x := try a.Sub()
    defer x.Free()
    found := try FindWindows(&x, io, args[0])
    var l := List[str].New(&x)
    try l.Push(found)
    for w in args[1..] {
        try l.Push(w)
    }
    return rt.SpawnNamed(io, l.Items(), vars, dir, s, named)
}

// Fold is the ASCII letter b in lower case, any other byte as it is.
Fold(b: u8): u8 {
    return if b >= 65 && b <= 90 { b + 32 } else { b }
}

// JoinDir writes the program path p (it has a separator and is not
// absolute) taken in the directory dir, as os/exec's joinExeDirAndFName
// does, to buf: dir\p for a relative p; dir's drive then p for a p rooted
// with no drive (\x\p.exe); dir\rest for a p on dir's drive with no root
// (C:rest). dir's drive is its own, or, for a dir without one (relative
// or rooted, not UNC), cwd: the current directory's drive letter (0 for
// none), which is the drive os/exec's normalizeDir finds by making dir
// absolute; a relative dir joined stays relative, which the
// system resolves in the current directory as os/exec's full path does.
// The bytes written, or -1 when p stays as it is (a p on another drive,
// or a dir without a drive for a rooted p) or the two do not fit.
// O(dir + p); allocates nothing.
JoinDir(buf: mut []u8, dir: str, p: str, cwd: u8): int {
    d := dir.AsBytes()
    b := p.AsBytes()
    var tail := b
    var n := 0
    if b[0] == 92 || b[0] == 47 {
        if d.Len() < 2 || d[1] != 58 || 2 + b.Len() > buf.Len() {
            return -1
        }
        (mut buf[0..2]).CopyFrom(d[0..2])
        (mut buf[2..2 + b.Len()]).CopyFrom(b)
        return 2 + b.Len()
    }
    if b.Len() >= 2 && b[1] == 58 {
        var dd: u8 = 0
        if d.Len() >= 2 && d[1] == 58 {
            dd = d[0]
        } else if !(d.Len() >= 2 && (d[0] == 92 || d[0] == 47) && (d[1] == 92 || d[1] == 47)) {
            dd = cwd
        }
        if dd == 0 || Fold(dd) != Fold(b[0]) {
            return -1
        }
        tail = b[2..]
    }
    if d.Len() + 1 + tail.Len() > buf.Len() {
        return -1
    }
    (mut buf[0..d.Len()]).CopyFrom(d)
    n = d.Len()
    if n > 0 && d[n - 1] != 92 && d[n - 1] != 47 {
        buf[n] = 92
        n += 1
    }
    (mut buf[n..n + tail.Len()]).CopyFrom(tail)
    return n + tail.Len()
}

// CwdDrive is the drive letter of the current directory (fs.Cwd, in x),
// or 0 when it has none or cannot be read; OutOfMemory and Canceled pass
// through. One runtime call; allocates the directory's path in x.
CwdDrive(x: &Arena, io: Io): Error!u8 {
    w := fs.Cwd(x, io) else e {
        match e {
            OutOfMemory { fail OutOfMemory }
            Canceled { fail Canceled }
            _ { return 0 }
        }
    }
    b := w.AsBytes()
    return if b.Len() >= 2 && b[1] == 58 { b[0] } else { 0 }
}

// SpawnJoined is rt.Spawn of args with args[0], a path with a separator
// that is not absolute, taken in dir (JoinDir), so the program is looked
// for where the child runs, as on Linux and in os/exec; built in a
// sub-arena of a freed before it returns. args[0] as it is when JoinDir
// leaves it. A drive-relative args[0] (C:rest) beside a dir with no drive
// reads the current directory's drive first (CwdDrive).
SpawnJoined(a: &Arena, io: Io, args: []str, vars: []str, dir: str, s: mut core.IoSpawnReq, named: []uint): Error!core.IoResult {
    var x := try a.Sub()
    defer x.Free()
    b := args[0].AsBytes()
    d := dir.AsBytes()
    var cwd: u8 = 0
    if b.Len() >= 2 && b[1] == 58 && (d.Len() < 2 || d[1] != 58) {
        cwd = try CwdDrive(&x, io)
    }
    var buf := try x.Slice[u8](dir.Len() + 1 + args[0].Len())
    n := JoinDir(mut buf[..], dir, args[0], cwd)
    if n < 0 {
        return rt.SpawnNamed(io, args, vars, dir, s, named)
    }
    joined := str.From(buf[0..n]) else { fail Os(code: 0) }
    var l := List[str].New(&x)
    try l.Push(joined)
    for w in args[1..] {
        try l.Push(w)
    }
    return rt.SpawnNamed(io, l.Items(), vars, dir, s, named)
}
