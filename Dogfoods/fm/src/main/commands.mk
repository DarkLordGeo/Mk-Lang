// Why: every file operation of the UI is also a scriptable command.
// What: Exec and the commands ls, view, cp, mv, rm, mkdir, link, stat, touch,
// du, pwd and batch, with Confirm for the questions rm and cp ask.
// How: each command runs against an Env (arena, heap, Io, zone) and writes
// to a Writer; errors are one CmdError set that main reports.

import std.fs
import std.mem
import std.time
import src.clock
import src.files
import src.pager

// What a command reports with, besides its own errors: bad usage (exit
// 2), a memory budget too small for the work (3), and a deadline (124).
error Usage
error Budget
type CmdError = files.OpError | WriteError | ReadError | time.Error | clock.BadZone | Usage | Budget | time.TimedOut | ParseError

// The capabilities a command needs: an arena for the command's work, the
// heap for walkers and tasks, the zone times show in, and `--root`, the
// directory relative paths are taken from (none: the current one).
struct Env {
    arena: &Arena
    heap: &Heap
    io: Io
    zone: &time.Zone
    root: ?str
}

// A relative path taken from env.root, or `p` as it is.
Env.At(&self, p: str): OutOfMemory!str from self.arena, p {
    root := self.root else {
        return p
    }
    if p.StartsWith("/") || p.StartsWith("\\") || (p.Len() >= 2 && p.Byte(1) == b':') {
        return p
    }
    return try files.Join(self.arena, root, p)
}

// Runs one command; its words are `args.words`, shown as given in what it
// prints, and used as paths through env.At.
Exec(env: &Env, args: &Args, input: mut Reader, out: mut Writer): CmdError!void {
    w := args.words[..]
    p0 := if w.Len() > 0 { try env.At(w[0]) } else { "" }
    p1 := if w.Len() > 1 { try env.At(w[1]) } else { "" }
    match args.command {
        "ls" {
            try Want(w, 1)
            try ListTree(env, args, p0, out)
        }
        "view" {
            try Want(w, 1)
            try ViewFile(env, args, p0, out)
        }
        "cp" {
            try Want(w, 2)
            try CopyCmd(env, args, w, p0, p1, input, out)
        }
        "mv" {
            try Want(w, 2)
            try files.Move(env.arena, env.heap, env.io, p0, p1)
            try Print(out, "moved {src} to {dst}\n", (src: w[0], dst: w[1]))
        }
        "rm" {
            try Want(w, 1)
            try Remove(env, args, w[0], p0, input, out)
        }
        "mkdir" {
            try Want(w, 1)
            try fs.MakeDirAll(env.io, p0)
            try Print(out, "made {p}\n", (p: w[0]))
        }
        "link" {
            // the target is stored as written: a relative one is taken from the link's directory
            try Want(w, 2)
            try fs.Symlink(env.io, w[0], p1)
            try Print(out, "linked {link} -> {target}\n", (link: w[1], target: w[0]))
        }
        "stat" {
            try Want(w, 1)
            try Stat(env, p0, out)
        }
        "touch" {
            try Want(w, 2)
            try Touch(env, args.bytes, w[0], w[1], p1, out)
        }
        "du" {
            if w.Len() == 0 {
                fail Usage
            }
            try DiskUsage(env, args, w, out)
        }
        "pwd" {
            try Want(w, 0)
            cwd := try fs.Cwd(env.arena, env.io)
            try Print(out, "{cwd}\n", (cwd))
        }
        _ { fail Usage }
    }
}

Want(words: []str, n: int): Usage!void {
    if words.Len() != n {
        fail Usage
    }
}

// Reads one line for a question and says whether it was yes.
Confirm(env: &Env, question: str, input: mut Reader, out: mut Writer): ReadError|WriteError|OutOfMemory!bool {
    try Print(out, "{question} [y/N] ", (question))
    try out.Flush()
    answer := try input.Line(env.arena) else {
        try out.Str("\n")
        return false
    }
    try out.Str("\n")
    t := answer.Trim()
    return t == "y" || t == "Y" || t == "yes"
}

// ls: the tree under `root`, walked with fs.Walk, each entry with its kind
// from fs.StatLink (a link is listed as a link, with its target) and its
// size; with --times its modification time in --zone. --depth stops the
// walk at that depth and --skip at directories of that name, with
// Walker.SkipDir. Entries are sorted by path, so the listing is the same
// whatever order the system reads a directory in.
ListTree(env: &Env, args: &Args, root: str, out: mut Writer): CmdError!void {
    var rows := List[Row].New(env.arena)
    var w := try fs.Walk(env.heap, env.io, root)
    onerr w.Close()
    for e := try w.Next(env.arena, env.io) {
        if e.depth == 0 {
            continue
        }
        p := e.path.Str() else {
            // a name that is not UTF-8: shown escaped, stated by its bytes
            base := e.path.Base()
            name := try Escaped(env.arena, base.AsBytes())
            rel := try Escaped(env.arena, e.path.AsBytes()[root.Len()..])
            info := try fs.StatPath(env.io, &e.path)
            if e.kind == fs.Kind.Dir {
                w.SkipDir()
            }
            try rows.Push(Row(key: try SortKey(env.arena, rel), name: name, depth: e.depth, info: info, target: ""))
            continue
        }
        name := files.BaseName(p)
        if e.kind == fs.Kind.Dir {
            if args.depth >= 0 && e.depth >= args.depth {
                w.SkipDir()
            }
            if s := args.skip {
                if name == s {
                    w.SkipDir()
                }
            }
        }
        info := try fs.StatLink(env.io, p)
        target := if info.kind == fs.Kind.Link { try fs.ReadLink(env.arena, env.io, p) } else { "" }
        key := try SortKey(env.arena, p[root.Len()..])
        try rows.Push(Row(key: key, name: name, depth: e.depth, info: info, target: target))
    }
    w.Close()
    rows.Sort(fn(x: &Row, y: &Row): bool { return x.key < y.key })
    try out.Str(".\n")
    for r in &rows {
        try Indent(out, r.depth)
        try Print(out, "{r.name}{mark}", (r, mark: files.Mark(r.info.kind)))
        match r.info.kind {
            .File { try Print(out, "  {size}", (size: r.info.size)) }
            .Link { try Print(out, " -> {r.target}", (r)) }
            _ {}
        }
        if args.times {
            try out.Str("  ")
            try clock.WriteWhen(out, r.info.modified, env.zone)
        }
        try out.Str("\n")
    }
}

struct Row {
    export key: str
    export name: str
    export depth: int
    export info: fs.Info
    export target: str
}

// A path's sort key: its separators made byte 1, so a directory sorts
// before its children and siblings sort by name.
SortKey(a: &Arena, rest: str): OutOfMemory!str from a {
    var t := Text.New(a)
    for ch in rest.Scalars() {
        try t.PushChar(if ch == '/' || ch == '\\' { '\u{1}' } else { ch })
    }
    return t.Done()
}

Indent(out: mut Writer, depth: int): WriteError!void {
    for _ in 0..depth {
        try out.Str("  ")
    }
}

// view: the file wrapped to --width columns at word boundaries. With
// --mem, the index of display lines lives in a budget of that many bytes,
// and a file that needs more fails with Budget.
ViewFile(env: &Env, args: &Args, path: str, out: mut Writer): CmdError!void {
    body := try fs.ReadText(env.arena, env.io, path)
    budget := args.mem else {
        for s in pager.Wrap(body, args.width) {
            try out.Str(body[s.start..s.end])
            try out.Str("\n")
        }
        return
    }
    var buf := try env.arena.Slice[u8](budget)
    var fixed := mem.Fixed.New(mut buf[..])
    al := fixed.Alloc()
    var index := HeapList[pager.Span].New(al)
    onerr index.Free(al)
    for s in pager.Wrap(body, args.width) {
        index.Push(al, s) else {
            fail Budget
        }
    }
    for s in &index {
        try out.Str(body[s.start..s.end])
        try out.Str("\n")
    }
    index.Free(al)
}

// cp: a file, a link or a whole tree. Copying a file over one that exists
// asks first, unless --yes.
CopyCmd(env: &Env, args: &Args, w: []str, p0: str, p1: str, input: mut Reader, out: mut Writer): CmdError!void {
    if !args.yes {
        if Exists(env.io, p1) {
            q := try env.arena.Fmt("overwrite {dst}?", (dst: w[1]))
            if !try Confirm(env, q, input, out) {
                try out.Str("kept\n")
                return
            }
        }
    }
    done := try files.CopyTree(env.arena, env.heap, env.io, p0, p1)
    try Print(out, "copied {src}: {f} files, {d} directories, {b} bytes\n", (src: w[0], f: done.files, d: done.dirs, b: done.bytes))
}

// rm: removes a file, a link or a tree with fs.RemoveAll, after asking,
// unless --yes. A path that does not exist is NotFound.
Remove(env: &Env, args: &Args, path: str, at: str, input: mut Reader, out: mut Writer): CmdError!void {
    info := try fs.StatLink(env.io, at)
    if !args.yes {
        what := if info.kind == fs.Kind.Dir { "directory" } else { "file" }
        q := try env.arena.Fmt("remove {what} {path}?", (what, path))
        if !try Confirm(env, q, input, out) {
            try out.Str("kept\n")
            return
        }
    }
    try fs.RemoveAll(env.io, at)
    try Print(out, "removed {path}\n", (path))
}

// stat: what fs.StatLink says of one path, with its absolute name's last
// element, and a link's target.
Stat(env: &Env, path: str, out: mut Writer): CmdError!void {
    info := try fs.StatLink(env.io, path)
    abs := try fs.Absolute(env.arena, env.io, path)
    kind := match info.kind {
        .File { "file" }
        .Dir { "directory" }
        .Link { "link" }
        .Other { "other" }
        _ {
            Panic("fs.Kind gained a member this match does not handle")
        }
    }
    writable := if info.permissions.mode & 0o200 != 0 { "yes" } else { "no" }
    try Print(out, "name: {n}\nkind: {kind}\nsize: {s}\nwritable: {writable}\nmodified: ", (n: files.BaseName(abs), kind, s: info.size, writable))
    try clock.WriteWhen(out, info.modified, env.zone)
    try out.Str("\n")
    if info.kind == fs.Kind.Link {
        target := try fs.ReadLink(env.arena, env.io, path)
        try Print(out, "target: {target}\n", (target))
    }
}

// touch: sets a file's modification time to an RFC 3339 time, making an
// empty file first when there is none. With --bytes, PATH's last element
// (after its last /) may hold \xHH escapes and the file is made from
// those bytes, so a script can make a name that is not UTF-8 (as `touch
// $'caf\xe9'` does in a shell) and see how the other commands show it; std
// sets a time only by a str path, so that file keeps the time it was made at.
Touch(env: &Env, bytes: bool, when: str, path: str, at: str, out: mut Writer): CmdError!void {
    t := try time.ParseRfc3339(when)
    if !bytes {
        _ = fs.StatLink(env.io, at) else e {
            match e {
                fs.NotFound { try fs.WriteAll(env.io, at, b"") }
                _ { fail e }
            }
        }
        try fs.SetModified(env.io, at, t)
        try out.Str("touched ")
        try out.Str(path)
        try out.Str(" at ")
        try clock.WriteWhen(out, t, env.zone)
        try out.Str("\n")
        return
    }
    // at is the directory env.At put before path, then path; only path's
    // last element is unescaped, and only / separates it, since an escape
    // starts with a backslash
    last := path[LastElement(path)..]
    raw := try Unescape(env.arena, last)
    keep := at.Len() - last.Len()
    var whole := try env.arena.Slice[u8](keep + raw.Len())
    (mut whole[..keep]).CopyFrom(at.AsBytes()[..keep])
    (mut whole[keep..]).CopyFrom(raw)
    p := try Path.New(env.arena, whole[..])
    var o := fs.DefaultOptions
    o.write = true
    o.create = true
    var f := try fs.OpenPath(env.io, &p, o)
    f.Close(env.io) else e {
        f.Abandon()
        fail e
    }
    try Print(out, "made {path}; its time stays, since std sets one only by a str path\n", (path))
}

// Where the last element of a path starts: after its last `/`.
LastElement(path: str): int {
    var i := path.Len()
    for i > 0 {
        c := path.Byte(i - 1)
        if c == b'/' {
            return i
        }
        i -= 1
    }
    return 0
}

// The bytes a name with \xHH escapes spells; Usage for an escape that is
// not two hex digits. O(name).
Unescape(a: &Arena, name: str): CmdError![]u8 from a {
    var out := try a.Slice[u8](name.Len())
    var n := 0
    var i := 0
    for i < name.Len() {
        b := name.Byte(i)
        if b == b'\\' && i + 3 < name.Len() && name.Byte(i + 1) == b'x' {
            hi := HexDigit(name.Byte(i + 2)) else {
                fail Usage
            }
            lo := HexDigit(name.Byte(i + 3)) else {
                fail Usage
            }
            out[n] = hi * 16 + lo
            i += 4
        } else {
            out[n] = b
            i += 1
        }
        n += 1
    }
    return out[..n]
}

HexDigit(c: u8): ?u8 {
    if c >= b'0' && c <= b'9' {
        return some(c - b'0')
    }
    if c >= b'a' && c <= b'f' {
        return some(c - b'a' + 10)
    }
    if c >= b'A' && c <= b'F' {
        return some(c - b'A' + 10)
    }
    return none
}

// A name that is not UTF-8 as `ls -b` shows one: each byte outside
// printable ASCII as \xHH, so the line is the same on every target
// (a backslash stays, as the separator of a Windows path).
Escaped(a: &Arena, bytes: []u8): OutOfMemory!str from a {
    digits := "0123456789abcdef"
    var out := try a.Slice[u8](bytes.Len() * 4)
    var n := 0
    for b in bytes {
        if b >= 0x20 && b < 0x7f {
            out[n] = b
            n += 1
        } else {
            out[n] = b'\\'
            out[n + 1] = b'x'
            out[n + 2] = digits.Byte(Cast[int](b >> 4))
            out[n + 3] = digits.Byte(Cast[int](b & 15))
            n += 4
        }
    }
    return str.From(out[..n]) else {
        return ""
    }
}

// One directory's total, filled in by its task.
struct Total {
    done: bool
    usage: files.Usage
}

DuTask(h: &Heap, io: Io, dir: str, out: mut Total) {
    u := files.TreeUsage(h, io, dir) else {
        return
    }
    out.done = true
    out.usage = u
}

// du: adds up each directory's tree, one task per directory, all at once;
// the totals print in the order given. --timeout puts a deadline on the
// whole: when it fires first, du fails with TimedOut.
DiskUsage(env: &Env, args: &Args, dirs: []str, out: mut Writer): CmdError!void {
    var totals := try env.arena.Slice[Total](dirs.Len())
    var expired := false
    scope s := env.io.Scope() {
        if ms := args.timeout {
            s.CancelAfter(time.Millis(i64(ms)))
        }
        // the tasks join in an inner scope, so the deadline is read after them
        scope tasks := env.io.Scope() {
            var i := 0
            for t in mut totals {
                tasks.go DuTask(env.heap, env.io, try env.At(dirs[i]), t)
                i += 1
            }
        }
        expired = s.Expired()
    }
    if expired {
        fail time.TimedOut
    }
    for i, d in dirs {
        t := &totals[i]
        if !t.done {
            try Print(out, "unreadable  {d}\n", (d))
            continue
        }
        try Print(out, "{b} bytes  {f} files  {n} dirs  {d}\n", (b: t.usage.bytes, f: t.usage.files, n: t.usage.dirs, d))
    }
}

// Whether anything, a dangling link included, is at `p`.
Exists(io: Io, p: str): bool {
    _ = fs.StatLink(io, p) else {
        return false
    }
    return true
}
