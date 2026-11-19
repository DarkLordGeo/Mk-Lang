// Why: the state of the two-pane file manager between events.
// What: Options, Error, App, Pane, Ctx, Mode and Action, with NewApp, Settle
// and Entries.
// How: everything read from disk is read again for each event, so what fm
// shows is never older than the last key; Ctx's frame arena is reset per
// event.

import std.fs
import std.term
import std.time
import src.clock
import src.files

/// What the interactive file manager is started with.
export struct Options {
    /// The directory the left pane starts in.
    export left: str
    /// The directory the right pane starts in.
    export right: str
    /// The zone file times are shown in.
    export zone: clock.ZoneChoice
}

/// What the event loop fails with: the terminal's errors, the
/// transcript's, and memory. A file operation's error is shown on the
/// status line and is never one of these.
export type Error = term.Error | fs.Error | WriteError | OutOfMemory

enum Mode {
    Browse
    Pager
    Prompt
    Confirm
}

enum Action {
    Copy
    Move
    Delete
    MakeDir
    Link
    Shell
    Goto
}

// One pane: the directory it shows, the one it started in (names are
// shown relative to it), the selected row and the first row on screen.
struct Pane {
    root: HeapText
    dir: HeapText
    cursor: int
    top: int
}

// The whole state the event loop keeps between events. Everything that
// is read from disk (a pane's entries) is read again for each event, so
// what fm shows is never older than the last key.
struct App {
    left: Pane
    right: Pane
    active: int
    mode: Mode
    action: Action
    // what a prompt has typed so far, and what a prompt or confirmation is about
    input: HeapText
    subject: HeapText
    message: HeapText
    // the pager's text and its title
    page: HeapText
    page_name: HeapText
    page_top: int
    cols: int
    rows: int
    focused: bool
    color: bool
    quit: ?int
}

// The capabilities one event's work needs: a frame arena reset before
// each event, the heap for walkers and tasks, its allocator for the
// state's texts, and the Io.
struct Ctx {
    frame: &Arena
    heap: &Heap
    al: &Alloc
    io: Io
    zone: &time.Zone
}

NewPane(al: &Alloc, dir: str): OutOfMemory!Pane from al {
    var root := HeapText.New(al)
    onerr root.Free(al)
    try root.Set(al, dir)
    var here := HeapText.New(al)
    onerr here.Free(al)
    try here.Set(al, dir)
    return Pane(root: root, dir: here, cursor: 0, top: 0)
}

impl Free for Pane {
    Free(self, a: &Alloc) {
        Pane(root, dir, ..) := self
        root.Free(a)
        dir.Free(a)
    }
}

NewApp(al: &Alloc, left: str, right: str, cols: int, rows: int, color: bool): OutOfMemory!App from al {
    l := try NewPane(al, left)
    onerr l.Free(al)
    r := try NewPane(al, right)
    return App(
        left: l,
        right: r,
        active: 0,
        mode: Mode.Browse,
        action: Action.Goto,
        input: HeapText.New(al),
        subject: HeapText.New(al),
        message: HeapText.New(al),
        page: HeapText.New(al),
        page_name: HeapText.New(al),
        page_top: 0,
        cols: cols,
        rows: rows,
        focused: true,
        color: color,
        quit: none,
    )
}

impl Free for App {
    Free(self, a: &Alloc) {
        App(left, right, input, subject, message, page, page_name, ..) := self
        left.Free(a)
        right.Free(a)
        input.Free(a)
        subject.Free(a)
        message.Free(a)
        page.Free(a)
        page_name.Free(a)
    }
}

// Sets the status line's message.
App.Say(mut self, c: &Ctx, s: str): OutOfMemory!void {
    try self.message.Set(c.al, s)
}

// The active pane's directory, copied into the frame so the state stays
// free to change.
App.Dir(&self, c: &Ctx, i: int): OutOfMemory!str from c.frame {
    return try c.frame.Dup(self.PaneOf(i).dir.Str())
}

// The rows a pane lists entries in: the screen less its header, the
// detail line and the status line.
App.ListRows(&self): int {
    n := self.rows - 3
    return if n < 1 { 1 } else { n }
}

// The entries of pane i, or an empty list with the error on the status
// line when its directory cannot be read.
App.Entries(mut self, c: &Ctx, i: int): OutOfMemory!List[files.Entry] from c.frame {
    dir := try self.Dir(c, i)
    return files.ListDir(c.frame, c.heap, c.io, dir) else e {
        rel := try Rel(c.frame, self.PaneOf(i).root.Str(), dir)
        msg := try c.frame.Fmt("{rel}: {e}", (rel, e))
        try self.Say(c, msg)
        List[files.Entry].New(c.frame)
    }
}

// Keeps each pane's cursor on an entry and on screen.
App.Settle(mut self, c: &Ctx): OutOfMemory!void {
    rows := self.ListRows()
    for i in 0..2 {
        n := (try self.Entries(c, i)).Len()
        var p := self.Pane(i)
        if p.cursor >= n {
            p.cursor = n - 1
        }
        if p.cursor < 0 {
            p.cursor = 0
        }
        if p.cursor < p.top {
            p.top = p.cursor
        }
        if p.cursor >= p.top + rows {
            p.top = p.cursor - rows + 1
        }
    }
}

/// `dir` shown relative to `root`: `.` for the root itself, `./rest` under
/// it, `../rest` beside it, and the whole path when the two share no
/// element (two drives), with `/` for every separator so one transcript
/// reads the same on every target. O(n); allocates the result in `a`.
export Rel(a: &Arena, root: str, dir: str): OutOfMemory!str from a {
    r := try Elements(a, root)
    d := try Elements(a, dir)
    var same := 0
    for same < r.Len() && same < d.Len() && r[same] == d[same] {
        same += 1
    }
    var t := Text.New(a)
    if same == 0 {
        if dir.StartsWith("/") || dir.StartsWith("\\") {
            try t.Push("/")
        }
    } else if same == r.Len() {
        try t.Push(".")
    } else {
        try t.Push("..")
        for _ in same + 1..r.Len() {
            try t.Push("/..")
        }
    }
    for i in same..d.Len() {
        if i > 0 || t.Str().Len() > 0 {
            if !t.Str().EndsWith("/") {
                try t.Push("/")
            }
        }
        try t.Push(d[i])
    }
    return t.Done()
}

// The elements of a path between its separators, as views of a copy of it
// in `a`.
Elements(a: &Arena, path: str): OutOfMemory!List[str] from a {
    p := try a.Dup(path)
    var out := List[str].New(a)
    var start := 0
    for i in 0..p.Len() + 1 {
        if i == p.Len() || p.Byte(i) == b'/' || p.Byte(i) == b'\\' {
            if i > start {
                try out.Push(p[start..i])
            }
            start = i + 1
        }
    }
    return out
}

// Pane i, 0 the left and 1 the right.
App.Pane(mut self, i: int): mut Pane {
    if i == 0 {
        return mut self.left
    }
    return mut self.right
}

// Pane i, to read.
App.PaneOf(&self, i: int): &Pane {
    if i == 0 {
        return &self.left
    }
    return &self.right
}
