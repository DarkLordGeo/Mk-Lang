// Why: the screen is redrawn from App after every event.
// What: Draw, DrawPanes, DrawPage, Detail and StatusLine.
// How: the frame is written to one buffer and sent in one write; colours,
// when on, come from each entry's kind.

import std.fs
import std.term
import std.text
import src.clock
import src.files
import src.pager

const Spaces: str = "                                                                "

// Draws the whole screen for the state `app` is in, in one write.
Draw(app: mut App, c: &Ctx, t: mut term.Term): Error!void {
    var buf: [32768]u8 = [0; 32768]
    var w := t.Writer(c.io, mut buf[..])
    try term.Clear(mut w)
    match app.mode {
        .Pager { try DrawPage(app, c, mut w) }
        _ { try DrawPanes(app, c, mut w) }
    }
    try term.MoveTo(mut w, app.rows - 1, 0)
    status := try StatusLine(&*app, c)
    try Cell(mut w, status, app.cols, false, false)
    try w.Flush()
}

DrawPanes(app: mut App, c: &Ctx, w: mut Writer): Error!void {
    half := app.cols / 2
    for i in 0..2 {
        col := if i == 0 { 0 } else { half }
        width := if i == 0 { half - 1 } else { app.cols - half }
        entries := try app.Entries(c, i)
        p := app.PaneOf(i)
        rel := try Rel(c.frame, p.root.Str(), p.dir.Str())
        title := try c.frame.Fmt("{mark}{rel}", (mark: if i == app.active { "* " } else { "  " }, rel))
        try term.MoveTo(w, 0, col)
        try Cell(w, title, width, false, app.color)
        for r in 0..app.ListRows() {
            k := p.top + r
            if k >= entries.Len() {
                break
            }
            e := &entries[k]
            name := try c.frame.Fmt("{e.name}{mark}", (e, mark: files.Mark(e.kind)))
            try term.MoveTo(w, r + 1, col)
            selected := k == p.cursor && i == app.active
            try Paint(w, e.kind, selected, app.color)
            try Cell(w, name, width, selected, app.color)
            if app.color {
                try NoStyle().Format(w)
            }
        }
    }
    try term.MoveTo(w, app.rows - 2, 0)
    try Detail(app, c, w)
}

// The detail line: the selected entry's size and modification time in the
// zone fm was started with.
Detail(app: mut App, c: &Ctx, w: mut Writer): Error!void {
    entries := try app.Entries(c, app.active)
    p := app.PaneOf(app.active)
    if p.cursor >= entries.Len() {
        try w.Str("(empty)")
        return
    }
    e := &entries[p.cursor]
    try Print(w, "{size:>12} B  ", (size: e.size))
    try clock.WriteWhen(w, e.modified, c.zone)
}

DrawPage(app: mut App, c: &Ctx, w: mut Writer): Error!void {
    lines := try pager.WrapAll(c.frame, app.page.Str(), app.cols)
    for r in 0..app.rows - 1 {
        k := app.page_top + r
        if k >= lines.Len() {
            break
        }
        try term.MoveTo(w, r, 0)
        try term.Sanitize(w, lines[k])
    }
}

// The pager's lines at the current width, and how many fit on a screen.
PageLines(app: &App, c: &Ctx): OutOfMemory!(lines: int, fit: int) {
    lines := try pager.WrapAll(c.frame, app.page.Str(), app.cols)
    fit := if app.rows > 2 { app.rows - 1 } else { 1 }
    return (lines: lines.Len(), fit: fit)
}

StatusLine(app: &App, c: &Ctx): OutOfMemory!str from c.frame {
    return match app.mode {
        .Browse {
            if app.message.Len() > 0 {
                try c.frame.Dup(app.message.Str())
            } else {
                "q quit  Tab pane  Enter open  c copy  m move  d delete  n mkdir  l link  ! shell  s sizes"
            }
        }
        .Prompt {
            try c.frame.Fmt("{verb}: {text}_", (verb: Verb(app.action), text: app.input.Str()))
        }
        .Confirm {
            try c.frame.Fmt("{verb} {name}? (y/n)", (verb: Verb(app.action), name: app.subject.Str()))
        }
        .Pager {
            (lines, fit) := try PageLines(app, c)
            last := if app.page_top + fit < lines { app.page_top + fit } else { lines }
            try c.frame.Fmt(
                "{name}  {first}-{last} of {lines}  (q closes)",
                (name: app.page_name.Str(), first: app.page_top + 1, last, lines),
            )
        }
    }
}

Verb(a: Action): str {
    return match a {
        .Copy { "copy" }
        .Move { "move" }
        .Delete { "delete" }
        .MakeDir { "mkdir" }
        .Link { "link" }
        .Shell { "shell" }
        .Goto { "goto" }
    }
}

// Writes `s` cut to `width` columns between grapheme clusters, its control
// bytes made harmless by `term.Sanitize`, and pads the rest with spaces.
Cell(w: mut Writer, s: str, width: int, selected: bool, color: bool): WriteError!void {
    if selected && !color {
        try w.Str(">")
    }
    room := if selected && !color { width - 1 } else { width }
    var used := 0
    var end := 0
    for g in text.Graphemes(s) {
        gw := text.Width(g)
        if used + gw > room {
            break
        }
        used += gw
        end += g.Len()
    }
    try term.Sanitize(w, s[..end])
    var pad := room - used
    for pad > 0 {
        n := if pad > Spaces.Len() { Spaces.Len() } else { pad }
        try w.Str(Spaces[..n])
        pad -= n
    }
}

// Sets the style of a row: reverse video for the selection, bold blue for
// a directory, cyan for a link; nothing when colour is off (NO_COLOR).
Paint(w: mut Writer, kind: fs.Kind, selected: bool, color: bool): WriteError!void {
    if !color {
        return
    }
    fg := match kind {
        .Dir { some(term.Color.Ansi(n: 4)) }
        .Link { some(term.Color.Ansi(n: 6)) }
        _ { none }
    }
    s := term.Style(fg: fg, bg: none, bold: kind == fs.Kind.Dir, underline: false, reverse: selected)
    try s.Format(w)
}

// The style with every attribute off, which resets the terminal's.
NoStyle(): term.Style {
    return term.Style(fg: none, bg: none, bold: false, underline: false, reverse: false)
}
