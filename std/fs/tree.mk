// Why: creating or removing a whole tree must not be carried out of the
// tree by a rename or a link that another process swaps in.
//
// What: MakeDirAll and RemoveAll, as Go's os.MkdirAll and os.RemoveAll.
//
// How: MakeDirAll creates from the deepest directory that exists down,
// over slices of its argument. RemoveAll works relative to held directories
// (Go's removeall_at.go and Rust's remove_dir_all): on Linux
// openat(O_NOFOLLOW) and unlinkat on the held descriptor, on Windows a held
// directory, which NTFS will not let be renamed or moved while it is open,
// and POSIX-semantics deletes inside it (std.rt's DirOpenAt and RemoveAt),
// p's own parent held from the first listing to the last removal. An
// interrupted call of either leaves what it did done.

import std.core
import std.rt

/// Creates the directory p and every missing parent. It succeeds when p is
/// already a directory, or a link to one, and does nothing when p names
/// only a root. NotFound for an empty p. NotDirectory when p or a parent is
/// something else, a dangling link included. Denied, InvalidPath or Os as
/// the OS answers. Canceled when cancelled; the directories made so far
/// stay. O(len(p)), with at most two runtime calls per element of p, one
/// when p's parent exists; allocates nothing.
export MakeDirAll(io: Io, p: str): Error!void {
    win := Target.os == core.Os.Windows
    b := p.AsBytes()
    end := TrimSeps(b, b.Len(), win)
    if end == 0 {
        // p is empty or only a root: nothing to make (Go's MkdirAll).
        if b.Len() == 0 {
            fail NotFound
        }
        return
    }
    // Back up to the deepest directory that exists, trying to make each.
    var k := end
    for try MakeOne(io, p, k) == ParentMissing {
        k = ParentEnd(b, k, win)
        if k == 0 {
            fail NotFound
        }
    }
    // Then make each element after it, in order. A parent removed in
    // between by another process is NotFound.
    for k < end {
        k = NextEnd(b, k, end, win)
        if try MakeOne(io, p, k) == ParentMissing {
            fail NotFound
        }
    }
}

// MakeOne's answers: p[0..k] is now a directory, or its parent is missing.
const MadeDir = 0
const ParentMissing = 1

// MakeOne makes p[0..k], a directory already there included (a racing
// creator, or a link to one), and is NotDirectory for anything else there.
MakeOne(io: Io, p: str, k: int): Error!int {
    q := p[0..k]
    MakeDir(io, q) else e {
        match e {
            NotFound {
                return ParentMissing
            }
            Exists {
                // A link to a directory counts as one; a dangling link,
                // which Stat cannot follow, is something else (Go's
                // MkdirAll reports the mkdir's EEXIST there).
                st := Stat(io, q) else se {
                    match se {
                        NotFound {
                            fail NotDirectory
                        }
                        _ {
                            fail se
                        }
                    }
                }
                if st.kind != Kind.Dir {
                    fail NotDirectory
                }
                return MadeDir
            }
            _ {
                fail e
            }
        }
    }
    return MadeDir
}

// TrimSeps is end less the separators that end b[0..end], but never into
// the volume name or the root separator after it.
TrimSeps(b: []u8, end: int, win: bool): int {
    root := RootLen(b, win)
    var e := end
    for e > root && IsSep(b[e - 1], win) {
        e -= 1
    }
    return if e == root { 0 } else { e }
}

// RootLen is the length of b's volume name and the separator after it, if
// any: `/`, `C:\`, `\\host\share\`, `C:`.
RootLen(b: []u8, win: bool): int {
    v := VolumeLen(b, win)
    return if v < b.Len() && IsSep(b[v], win) { v + 1 } else { v }
}

// ParentEnd is the end of b[0..k]'s parent, trailing separators removed,
// or 0 when it has none that is not a root.
ParentEnd(b: []u8, k: int, win: bool): int {
    root := RootLen(b, win)
    var i := k
    for i > root && !IsSep(b[i - 1], win) {
        i -= 1
    }
    return TrimSeps(b, i, win)
}

// NextEnd is the end of the element after b[0..k], at most end.
NextEnd(b: []u8, k: int, end: int, win: bool): int {
    var i := k
    for i < end && IsSep(b[i], win) {
        i += 1
    }
    for i < end && !IsSep(b[i], win) {
        i += 1
    }
    return i
}

// RmDepth bounds the directories RemoveAll holds open at once (one per
// level below p), and RmNames the bytes of their names together: the
// frame holds both (about 50 KiB), so RemoveAll takes no allocator. A tree
// deeper than RmDepth levels below p, or whose path below p passes RmNames
// bytes at its deepest, is InvalidPath, as a path too long to name is.
const RmDepth = 1024
const RmNames = 32768

// RmRelists bounds how often one RemoveAll call lists a directory again
// after its removal found it not empty (a racing creator, or a file system
// whose listing skips entries removed during it), over the whole tree, so
// a creator that refills nested directories cannot multiply it level by
// level; then the removal's own error stands, as Go's os.RemoveAll
// reports ENOTEMPTY, and a creator that never stops cannot keep the call
// running.
const RmRelists = 256

/// Removes p and everything under it. It succeeds when nothing is at p,
/// and does nothing for an empty p. A file or a link at p is removed
/// itself, never a link's target, even with a trailing separator after it.
/// Every entry is removed relative to its open parent directory and no
/// link below p is followed, so another process that renames a directory
/// or swaps in a link cannot carry the removal out of the tree.
///
/// InvalidPath when p's last element is `.` or `..`, when p names only a
/// root, or when the tree is deeper than 1,024 levels or its names below p
/// pass 32 KiB at once. Denied, Os and the rest as the OS answers for the
/// first entry it cannot remove. Os with the OS's not-empty code when
/// directories are still not empty after 256 more listings in all, because
/// another process keeps filling them. Canceled when cancelled, with what
/// it removed gone.
///
/// Each directory is listed once, and again only when its removal finds it
/// not empty. O(entries) runtime calls: one per entry, about three per
/// directory (open, listing, removal), and up to three more for p (its
/// removal tried first, then its parent opened); allocates nothing, and
/// uses about 50 KiB of stack.
///
/// On Windows, when other calls or tasks remove the same tree at once
/// (another RemoveAll, or Remove of its entries), any of them can fail with
/// Denied, and when every one fails part of the tree can be left; Linux and
/// std.sim answer Ok to two RemoveAll calls. A call made once the others
/// have returned removes what is left, unless another handle holds an
/// entry of it open.
export RemoveAll(io: Io, p: str): Error!void {
    win := Target.os == core.Os.Windows
    b := p.AsBytes()
    if b.Len() == 0 {
        return
    }
    if DotTail(b, win) {
        fail InvalidPath
    }
    end := TrimSeps(b, b.Len(), win)
    if end == 0 {
        fail InvalidPath
    }
    q := b[0..end]
    // Go's simple case first: a file, a link or an empty directory goes
    // with one or two calls, an empty directory that cannot be listed
    // included.
    first := rt.RemoveAt(io, 0, q, false)
    if first.err == core.SysErr.None || first.err == core.SysErr.NotFound {
        return
    }
    if first.err == core.SysErr.Canceled || first.err == core.SysErr.Unsupported {
        _ = try Check(first)
    }
    if first.err == core.SysErr.IsDirectory {
        empty := rt.RemoveAt(io, 0, q, true)
        if empty.err == core.SysErr.None || empty.err == core.SysErr.NotFound {
            return
        }
        if empty.err == core.SysErr.Canceled {
            _ = try Check(empty)
        }
    }
    // A directory with entries: its walk, every name taken relative to a
    // held directory, p's own relative to its parent, which is held for
    // the whole call (opened following links, as Go's os.RemoveAll opens
    // it), so no rename of an ancestor can redirect a later pass.
    var s := end
    vol := VolumeLen(b, win)
    for s > vol && !IsSep(b[s - 1], win) {
        s -= 1
    }
    pr := core.IoOpenDir(io, if s == 0 { "." } else { p[0..s] })
    if pr.err == core.SysErr.NotFound {
        return
    }
    ph := Trunc[uint](try Check(pr))
    defer rt.Abandon(ph)
    base := b[s..end]
    r := rt.DirOpenAt(io, ph, base)
    if r.err == core.SysErr.NotFound {
        return
    }
    if r.err == core.SysErr.NotDirectory {
        // Not a directory now (a link, or a file put in its place).
        gone := rt.RemoveAt(io, ph, base, false)
        if gone.err != core.SysErr.NotFound {
            _ = try Check(gone)
        }
        return
    }
    var hs: [1024]uint = [0; 1024]
    var ends: [1024]int = [0; 1024]
    var relists := 0
    var names: [32768]u8 = [0; 32768]
    hs[0] = Trunc[uint](try Check(r))
    var depth := 0
    for {
        off := ends[depth]
        var st := core.IoStat(kind: 0, size: 0, modified: 0)
        rd := core.IoReadDir(io, hs[depth], mut names[off..], mut st)
        if rd.err == core.SysErr.NoSpace {
            RmRelease(hs[..], depth)
            fail InvalidPath
        }
        // A directory another remover removed while it was held lists
        // nothing more (Linux's getdents answers ENOENT): its listing ends.
        var n := 0
        if rd.err != core.SysErr.NotFound {
            n = CheckCount(rd, RmNames - off) else e {
                RmRelease(hs[..], depth)
                fail e
            }
        }
        if n > 0 {
            name := names[off..off + n]
            listed_dir := st.kind == core.IoKindDir
            if !listed_dir {
                gone := rt.RemoveAt(io, hs[depth], name, false)
                if gone.err == core.SysErr.None || gone.err == core.SysErr.NotFound {
                    continue
                }
                if gone.err != core.SysErr.IsDirectory {
                    _ = Check(gone) else e {
                        RmRelease(hs[..], depth)
                        fail e
                    }
                }
                // A directory now, though listed as something else: it is
                // descended below.
            }
            if depth + 1 == RmDepth {
                RmRelease(hs[..], depth)
                fail InvalidPath
            }
            c := rt.DirOpenAt(io, hs[depth], name)
            if c.err == core.SysErr.NotFound {
                continue
            }
            if c.err == core.SysErr.NotDirectory && listed_dir {
                // Listed as a directory, opened as none: a link or a
                // junction (DirOpenAt follows neither) or a file put in
                // its place. It is removed as an entry, its target kept.
                gone := rt.RemoveAt(io, hs[depth], name, false)
                if gone.err == core.SysErr.None || gone.err == core.SysErr.NotFound {
                    continue
                }
                RmRelease(hs[..], depth)
                _ = try Check(gone)
                fail NotDirectory
            }
            // Any other failure ends the call, a kind that flips back and
            // forth included, so no listing repeats without bound.
            h := Check(c) else e {
                RmRelease(hs[..], depth)
                fail e
            }
            depth += 1
            hs[depth] = Trunc[uint](h)
            ends[depth] = off + n
            continue
        }
        // The listing of hs[depth] ended: close it (Windows removes no
        // directory held open) and remove it inside the directory above,
        // p's parent for p itself.
        rt.Abandon(hs[depth])
        hs[depth] = 0
        above := if depth == 0 { ph } else { hs[depth - 1] }
        own := if depth == 0 { base } else { names[ends[depth - 1]..ends[depth]] }
        gone := rt.RemoveAt(io, above, own, true)
        if gone.err == core.SysErr.None || gone.err == core.SysErr.NotFound {
            if depth == 0 {
                return
            }
            depth -= 1
            continue
        }
        if !rt.RemoveNotEmpty(gone) || relists == RmRelists {
            RmRelease(hs[..], depth - 1)
            _ = try Check(gone)
            fail Os(code: gone.os)
        }
        // Filled during its walk (a racing creator), or listed by a file
        // system that skipped entries removed meanwhile: list it again.
        relists += 1
        o := rt.DirOpenAt(io, above, own)
        if o.err == core.SysErr.NotFound {
            // Removed meanwhile by another remover: done with it.
            if depth == 0 {
                return
            }
            depth -= 1
            continue
        }
        h := Check(o) else e {
            RmRelease(hs[..], depth - 1)
            fail e
        }
        hs[depth] = Trunc[uint](h)
    }
}

// RmRelease closes the held handles hs[0..depth], deepest first.
RmRelease(hs: []uint, depth: int) {
    var i := depth
    for i >= 0 {
        if hs[i] != 0 {
            rt.Abandon(hs[i])
        }
        i -= 1
    }
}

// DotTail: b's last element is `.` or `..`, which RemoveAll refuses (Go
// refuses `.`; `..` would remove a parent of the path as written).
DotTail(b: []u8, win: bool): bool {
    var e := b.Len()
    for e > 0 && IsSep(b[e - 1], win) {
        e -= 1
    }
    var s := e
    for s > 0 && !IsSep(b[s - 1], win) {
        s -= 1
    }
    v := VolumeLen(b, win)
    if s < v {
        s = v
    }
    n := e - s
    return (n == 1 && b[s] == 0x2E) || (n == 2 && b[s] == 0x2E && b[s + 1] == 0x2E)
}
