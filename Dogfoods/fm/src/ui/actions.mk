// Why: what the keys of the browser do: open, move between directories, and
// the file operations that ask first.
// What: Open, SetDir, UpDir, GoTo, GoTemp, View, Ask and Perform, and the
// copy, move and link helpers.
// How: each action changes App and sets the status line; a file operation's
// error is shown, never returned.

import std.fs
import std.term
import src.files
import src.shell

// The largest file the pager opens.
const PageLimit: i64 = 4 << 20

// The selected entry of the active pane and its path, or none in an empty
// directory.
Selected(app: mut App, c: &Ctx): OutOfMemory!?(entry: files.Entry, path: str) from c.frame {
    entries := try app.Entries(c, app.active)
    k := app.PaneOf(app.active).cursor
    if k < 0 || k >= entries.Len() {
        return none
    }
    e := entries[k]
    dir := try app.Dir(c, app.active)
    return some((entry: e, path: try files.Join(c.frame, dir, e.name)))
}

// Enter opens a directory, or a link to one, in the pane, and any other
// entry in the pager.
Open(app: mut App, c: &Ctx): OutOfMemory!void {
    (entry, path) := try Selected(app, c) else {
        return
    }
    var is_dir := entry.kind == fs.Kind.Dir
    if entry.kind == fs.Kind.Link {
        // Stat follows the link; StatLink, which listed it, does not
        target := fs.Stat(c.io, path) else e {
            try app.Say(c, try c.frame.Fmt("{entry.name}: {e}", (entry, e)))
            return
        }
        is_dir = target.kind == fs.Kind.Dir
    }
    if is_dir {
        try SetDir(app, c, path)
        return
    }
    try View(app, c, path, entry.name)
}

// Makes `path`, absolute where the system can say (a simulated one cannot),
// the active pane's directory with the cursor on its first entry.
SetDir(app: mut App, c: &Ctx, path: str): OutOfMemory!void {
    abs := fs.Absolute(c.frame, c.io, path) else e {
        match e {
            OutOfMemory { fail OutOfMemory }
            _ { try files.Clean(c.frame, path) }
        }
    }
    var p := app.Pane(app.active)
    try p.dir.Set(c.al, abs)
    p.cursor = 0
    p.top = 0
}

// Backspace goes to the parent directory, with the cursor on the one it
// came from.
UpDir(app: mut App, c: &Ctx): OutOfMemory!void {
    dir := try app.Dir(c, app.active)
    parent := files.Parent(dir) else {
        try app.Say(c, "at the top")
        return
    }
    try SetDir(app, c, parent)
    entries := try app.Entries(c, app.active)
    if k := files.IndexOf(entries[..], files.BaseName(dir)) {
        app.Pane(app.active).cursor = k
    }
}

// Goes to a typed or pasted directory, taken from the active pane's
// directory when it is relative.
GoTo(app: mut App, c: &Ctx, where: str): OutOfMemory!void {
    if where.Len() == 0 {
        return
    }
    dir := try app.Dir(c, app.active)
    var path := where
    if !IsAbsolute(where) {
        path = try files.Clean(c.frame, try files.Join(c.frame, dir, where))
    }
    info := fs.Stat(c.io, path) else e {
        try app.Say(c, try c.frame.Fmt("{where}: {e}", (where, e)))
        return
    }
    if info.kind != fs.Kind.Dir {
        try app.Say(c, try c.frame.Fmt("{where}: not a directory", (where)))
        return
    }
    try SetDir(app, c, path)
}

IsAbsolute(p: str): bool {
    if p.StartsWith("/") || p.StartsWith("\\") {
        return true
    }
    return p.Len() >= 3 && p.Byte(1) == b':' && (p.Byte(2) == b'\\' || p.Byte(2) == b'/')
}

// `t` goes to the system's directory for temporary files.
GoTemp(app: mut App, c: &Ctx): OutOfMemory!void {
    tmp := fs.TempDir(c.frame, c.io) else e {
        try app.Say(c, try c.frame.Fmt("temporary directory: {e}", (e)))
        return
    }
    try SetDir(app, c, tmp)
    try app.Say(c, "temporary directory")
}

// Opens `path` in the pager, refusing a file over PageLimit or one that
// is not UTF-8 text.
View(app: mut App, c: &Ctx, path: str, name: str): OutOfMemory!void {
    info := fs.Stat(c.io, path) else e {
        try app.Say(c, try c.frame.Fmt("{name}: {e}", (name, e)))
        return
    }
    if info.size > PageLimit {
        try app.Say(c, try c.frame.Fmt("{name}: too large to view", (name)))
        return
    }
    body := fs.ReadText(c.frame, c.io, path) else e {
        try app.Say(c, try c.frame.Fmt("{name}: {e}", (name, e)))
        return
    }
    try app.page.Set(c.al, body)
    try app.page_name.Set(c.al, name)
    app.page_top = 0
    app.mode = Mode.Pager
}

// Starts an action: the ones on the selected entry ask for a yes, the
// others for a line of text.
Ask(app: mut App, c: &Ctx, action: Action): OutOfMemory!void {
    app.action = action
    match action {
        .MakeDir | .Shell | .Goto {
            app.input.Clear()
            app.mode = Mode.Prompt
            return
        }
        _ {}
    }
    (entry, ..) := try Selected(app, c) else {
        try app.Say(c, "nothing selected")
        return
    }
    try app.subject.Set(c.al, entry.name)
    app.mode = Mode.Confirm
}

// Carries out the action, with `arg` the confirmed entry's name or the
// prompt's text, and says how it went on the status line.
Perform(app: mut App, c: &Ctx, t: mut term.Term, log: mut Writer, arg: str): Error!void {
    dir := try app.Dir(c, app.active)
    other := try app.Dir(c, 1 - app.active)
    src := try files.Join(c.frame, dir, arg)
    dst := try files.Join(c.frame, other, arg)
    msg := match app.action {
        .Copy { try CopyEntry(c, src, dst, arg) }
        .Move { try MoveEntry(c, src, dst, arg) }
        .Delete { try Delete(app, c, t, log, src, arg) }
        .Link { try LinkEntry(c, src, dst, arg) }
        .MakeDir { try MakeDir(c, src, arg) }
        .Shell { try RunShell(app, c, t, log, arg, dir) }
        .Goto {
            try GoTo(app, c, arg)
            return
        }
    }
    try app.Say(c, msg)
}

CopyEntry(c: &Ctx, src: str, dst: str, name: str): OutOfMemory!str from c.frame {
    done := files.CopyTree(c.frame, c.heap, c.io, src, dst) else e {
        return try c.frame.Fmt("copy {name}: {e}", (name, e))
    }
    return try c.frame.Fmt("copied {name}: {n} files, {b} bytes", (name, n: done.files, b: done.bytes))
}

MoveEntry(c: &Ctx, src: str, dst: str, name: str): OutOfMemory!str from c.frame {
    files.Move(c.frame, c.heap, c.io, src, dst) else e {
        return try c.frame.Fmt("move {name}: {e}", (name, e))
    }
    return try c.frame.Fmt("moved {name}", (name))
}

// Makes a link in the other pane to the selected entry's absolute path.
LinkEntry(c: &Ctx, src: str, dst: str, name: str): OutOfMemory!str from c.frame {
    target := fs.Absolute(c.frame, c.io, src) else e {
        return try c.frame.Fmt("link {name}: {e}", (name, e))
    }
    fs.Symlink(c.io, target, dst) else e {
        return try c.frame.Fmt("link {name}: {e}", (name, e))
    }
    return try c.frame.Fmt("linked {name}", (name))
}

MakeDir(c: &Ctx, path: str, name: str): OutOfMemory!str from c.frame {
    fs.MakeDirAll(c.io, path) else e {
        return try c.frame.Fmt("mkdir {name}: {e}", (name, e))
    }
    return try c.frame.Fmt("made {name}", (name))
}

// Deletes `path` with fs.RemoveAll. A directory that holds anything needs
// its name typed again, unechoed, so a stray `y` cannot remove a tree.
Delete(app: mut App, c: &Ctx, t: mut term.Term, log: mut Writer, path: str, name: str): Error!str from c.frame {
    info := fs.StatLink(c.io, path) else e {
        return try c.frame.Fmt("delete {name}: {e}", (name, e))
    }
    if info.kind == fs.Kind.Dir {
        inside := files.ListDir(c.frame, c.heap, c.io, path) else e {
            return try c.frame.Fmt("delete {name}: {e}", (name, e))
        }
        if inside.Len() > 0 {
            try app.Say(c, try c.frame.Fmt("{name} holds {n} entries: type its name to delete it", (name, n: inside.Len())))
            try Draw(app, c, t)
            try Print(log, "secret: type {name}\n", (name))
            try log.Flush()
            typed := t.ReadSecret(c.frame, c.io) else e {
                return try c.frame.Fmt("kept {name}: {e}", (name, e))
            }
            if typed != name {
                return try c.frame.Fmt("kept {name}: the name did not match", (name))
            }
        }
    }
    fs.RemoveAll(c.io, path) else e {
        return try c.frame.Fmt("delete {name}: {e}", (name, e))
    }
    return try c.frame.Fmt("deleted {name}", (name))
}

// Runs a command line on a pseudo-terminal in the pane's directory, with
// what is typed passed to it, and shows what it wrote in the pager.
RunShell(app: mut App, c: &Ctx, t: mut term.Term, log: mut Writer, command: str, dir: str): OutOfMemory!str from c.frame {
    r := shell.RunOnPty(c.frame, c.heap, c.io, t, log, command, dir, app.cols, app.rows) else e {
        return try c.frame.Fmt("shell: {e}", (e))
    }
    code := r.code else { -1 }
    app.cols = r.cols
    app.rows = r.rows
    title := try c.frame.Fmt("$ {command}  (exit {code})", (command, code))
    try app.page.Set(c.al, r.output)
    try app.page_name.Set(c.al, title)
    app.page_top = 0
    app.mode = Mode.Pager
    first := shell.FirstLine(r.output)
    return try c.frame.Fmt("exit {code}: {first}", (code, first))
}

// The result of one pane's size, filled in by its task.
struct Sized {
    ok: bool
    usage: files.Usage
}

SizeTask(h: &Heap, io: Io, dir: str, out: mut Sized) {
    u := files.TreeUsage(h, io, dir) else {
        return
    }
    out.ok = true
    out.usage = u
}

// `s` adds up both panes' trees at once, one task each.
Sizes(app: mut App, c: &Ctx): OutOfMemory!void {
    left := try app.Dir(c, 0)
    right := try app.Dir(c, 1)
    var l := Sized(ok: false, usage: files.Usage(bytes: 0, files: 0, dirs: 0))
    var r := Sized(ok: false, usage: files.Usage(bytes: 0, files: 0, dirs: 0))
    scope s := c.io.Scope() {
        s.go SizeTask(c.heap, c.io, left, mut l)
        s.go SizeTask(c.heap, c.io, right, mut r)
    }
    ls := try SizeText(c, &l)
    rs := try SizeText(c, &r)
    try app.Say(c, try c.frame.Fmt("sizes: left {ls}; right {rs}", (ls, rs)))
}

SizeText(c: &Ctx, s: &Sized): OutOfMemory!str from c.frame {
    if !s.ok {
        return "unreadable"
    }
    return try c.frame.Fmt("{f} files, {b} bytes", (f: s.usage.files, b: s.usage.bytes))
}

// `=` shows the active pane's directory in the other pane too, with the
// same start directory, so the two can be worked on side by side.
Mirror(app: mut App, c: &Ctx): OutOfMemory!void {
    from := app.active
    root := try c.frame.Dup(app.PaneOf(from).root.Str())
    dir := try app.Dir(c, from)
    var p := app.Pane(1 - from)
    try p.root.Set(c.al, root)
    try p.dir.Set(c.al, dir)
    p.cursor = 0
    p.top = 0
}
