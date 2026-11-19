// Why: the event loop that runs fm on the terminal.
// What: Run, and the transcript helpers Describe, KeyName, Summary and
// PaneText.
// How: raw mode on the alternate screen with bracketed paste, mouse, focus
// and the kitty keyboard protocol; after each event one transcript line
// records the event and the state it left, which is how its tests see it.

import std.term
import std.time
import src.clock
import std.fs
import src.files
import src.pager
import src.shell

/// Runs the two-pane file manager on the controlling terminal
/// (`term.Open`), in raw mode on the alternate screen with bracketed
/// paste, mouse and focus reports and the kitty keyboard protocol on,
/// until `q` (exit code 0) or Ctrl+C (130). After each event it writes one
/// line to `log`, the transcript: the event, then the state it left.
/// The terminal's modes are put back on every exit, and by the runtime on
/// a panic. Errors are the terminal's, the transcript's and memory's; a
/// file operation's error is shown and logged, never returned. O(entries
/// listed) per event; allocates a frame arena on `h`, reset per event.
export Run(h: &Heap, io: Io, o: &Options, log: mut Writer): Error!int {
    var t := try term.Open(io)
    onerr t.Close(io)
    (cols, rows) := try t.Size(io)
    var zones := try Arena.New(h)
    defer zones.Free()
    zone := clock.LoadZone(&zones, io, o.zone) else {
        time.Zone.Utc()
    }
    al := h.Alloc()
    var frame := try Arena.New(h)
    defer frame.Free()
    left := try Absolute(&frame, io, o.left)
    right := try Absolute(&frame, io, o.right)
    var app := try NewApp(al, left, right, cols, rows, term.UseColor(io, term.Std.Stdout))
    onerr app.Free(al)
    try t.Raw(io)
    try Screen(mut t, io, true)
    c := Ctx(frame: &frame, heap: h, al: al, io: io, zone: &zone)
    try app.Settle(&c)
    try Draw(mut app, &c, mut t)
    try Print(log, "start | {s}\n", (s: try Summary(mut app, &c)))
    try log.Flush()
    for app.quit.IsNone() {
        frame.Reset()
        c2 := Ctx(frame: &frame, heap: h, al: al, io: io, zone: &zone)
        ev := try t.Read(&frame, io)
        what := try Describe(&frame, ev)
        if !try Handle(mut app, &c2, mut t, log, ev) {
            continue
        }
        try app.Settle(&c2)
        if app.quit.IsNone() {
            try Draw(mut app, &c2, mut t)
        }
        try Print(log, "{what} | {s}\n", (what, s: try Summary(mut app, &c2)))
        try log.Flush()
    }
    // the loop above ends only once quit holds a code
    code := app.quit.Must()
    app.Free(al)
    try Screen(mut t, io, false)
    try t.Restore(io)
    t.Close(io)
    return code
}

// The modes fm runs in, turned on in this order and off in the reverse.
Screen(t: mut term.Term, io: Io, on: bool): term.Error|WriteError!void {
    var buf: [256]u8 = [0; 256]
    var w := t.Writer(io, mut buf[..])
    if on {
        try term.AltScreen(mut w, true)
        try term.ShowCursor(mut w, false)
        try term.BracketedPaste(mut w, true)
        try term.MouseCapture(mut w, true)
        try term.KittyKeys(mut w, true)
    } else {
        try term.KittyKeys(mut w, false)
        try term.MouseCapture(mut w, false)
        try term.BracketedPaste(mut w, false)
        try term.ShowCursor(mut w, true)
        try term.AltScreen(mut w, false)
    }
    try w.Flush()
}

// A start directory made absolute, so a pane can always go up from it;
// one fs.Absolute cannot resolve is kept as given.
Absolute(a: &Arena, io: Io, p: str): OutOfMemory!str from a, p {
    return fs.Absolute(a, io, p) else e {
        match e {
            OutOfMemory { fail OutOfMemory }
            _ { p }
        }
    }
}

// The event, as the transcript names it.
Describe(a: &Arena, ev: term.Event): OutOfMemory!str from a {
    return match ev {
        .Key(key, ch, mods) {
            var t := Text.New(a)
            if mods.ctrl {
                try t.Push("ctrl-")
            }
            if mods.alt {
                try t.Push("alt-")
            }
            if mods.shift {
                try t.Push("shift-")
            }
            try t.Push(try KeyName(a, key, ch))
            try a.Fmt("key {k}", (k: t.Done()))
        }
        .Paste(text) { try a.Fmt("paste {n} bytes", (n: text.Len())) }
        .Resize(cols, rows) { try a.Fmt("resize {cols}x{rows}", (cols, rows)) }
        .Mouse(button, row, col) { try a.Fmt("mouse {button} at {row},{col}", (button, row, col)) }
        .Focus(on) {
            if on { "focus in" } else { "focus out" }
        }
    }
}

KeyName(a: &Arena, key: term.Key, ch: char): OutOfMemory!str from a {
    return match key {
        .Char {
            if ch == ' ' { "space" } else { try a.Fmt("{ch}", (ch)) }
        }
        .Enter { "enter" }
        .Escape { "esc" }
        .Backspace { "backspace" }
        .Tab { "tab" }
        .Up { "up" }
        .Down { "down" }
        .Left { "left" }
        .Right { "right" }
        .Home { "home" }
        .End { "end" }
        .PageUp { "pgup" }
        .PageDown { "pgdn" }
        .Insert { "insert" }
        .Delete { "delete" }
        .F(n) { try a.Fmt("f{n}", (n)) }
        _ { "other" }
    }
}

// The state after an event, as the transcript records it: each pane as
// `[*]dir:selected#k/n`, or the prompt, the question or the pager's view,
// then the status message.
Summary(app: mut App, c: &Ctx): OutOfMemory!str from c.frame {
    var t := Text.New(c.frame)
    match app.mode {
        .Browse {
            for i in 0..2 {
                if i == 1 {
                    try t.Push("  ")
                }
                try t.Push(try PaneText(app, c, i))
            }
        }
        .Prompt {
            try t.Push(try c.frame.Fmt("prompt {verb} \"{text}\"", (verb: Verb(app.action), text: app.input.Str())))
        }
        .Confirm {
            try t.Push(try c.frame.Fmt("confirm {verb} \"{name}\"", (verb: Verb(app.action), name: app.subject.Str())))
        }
        .Pager {
            if app.page_name.Str().StartsWith("$ ") {
                // a command's screen is the pseudo-console's rendering, whose line
                // breaks and padding vary from run to run: only its title and first
                // line are stable
                first := shell.FirstLine(app.page.Str())
                try t.Push(try c.frame.Fmt("output \"{name}\" \"{first}\"", (name: app.page_name.Str(), first)))
                return t.Done()
            }
            lines := try pager.WrapAll(c.frame, app.page.Str(), app.cols)
            fit := app.rows - 1
            first := if app.page_top < lines.Len() { lines[app.page_top] } else { "" }
            last := if app.page_top + fit < lines.Len() { app.page_top + fit } else { lines.Len() }
            try t.Push(
                try c.frame.Fmt(
                    "pager \"{name}\" {top}-{last}/{n} \"{first}\"",
                    (name: app.page_name.Str(), top: app.page_top + 1, last, n: lines.Len(), first),
                ),
            )
        }
    }
    if app.message.Len() > 0 {
        try t.Push(" -- ")
        try t.Push(app.message.Str())
    }
    return t.Done()
}

PaneText(app: mut App, c: &Ctx, i: int): OutOfMemory!str from c.frame {
    entries := try app.Entries(c, i)
    p := app.PaneOf(i)
    rel := try Rel(c.frame, p.root.Str(), p.dir.Str())
    star := if i == app.active { "*" } else { "" }
    if entries.Len() == 0 {
        return try c.frame.Fmt("{star}{rel}:-#0/0", (star, rel))
    }
    e := &entries[p.cursor]
    return try c.frame.Fmt(
        "{star}{rel}:{name}{mark}#{k}/{n}",
        (star, rel, name: e.name, mark: files.Mark(e.kind), k: p.cursor + 1, n: entries.Len()),
    )
}
