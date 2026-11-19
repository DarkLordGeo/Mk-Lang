// Why: keys, pastes and mouse events mean different things in each mode.
// What: Handle, and BrowseKey, PagerKey, PromptKey, ConfirmKey, OnPaste and
// OnMouse.
// How: Handle dispatches on App's mode; a paste in the browser goes to the
// pasted directory.

import std.term

// Applies one event to the state, and says whether it was one fm acts on:
// a resize to the size the terminal already has is not (a Windows console
// reports its size once when it starts). File operations report on the
// status line; only the terminal, the transcript and memory fail the loop.
Handle(app: mut App, c: &Ctx, t: mut term.Term, log: mut Writer, ev: term.Event): Error!bool {
    match ev {
        .Resize(cols, rows) {
            if cols == app.cols && rows == app.rows {
                return false
            }
            app.cols = cols
            app.rows = rows
        }
        .Focus(on) {
            app.focused = on
        }
        .Mouse(button, row, col) {
            try OnMouse(app, c, button, row, col)
        }
        .Paste(text) {
            try OnPaste(app, c, text)
        }
        .Key(key, ch, mods) {
            if mods.ctrl && ch == 'c' {
                app.quit = some(130)
                return true
            }
            match app.mode {
                .Browse { try BrowseKey(app, c, key, ch) }
                .Pager { try PagerKey(app, c, key, ch) }
                .Prompt { try PromptKey(app, c, t, log, key, ch, mods.ctrl) }
                .Confirm { try ConfirmKey(app, c, t, log, ch) }
            }
        }
    }
    return true
}

BrowseKey(app: mut App, c: &Ctx, key: term.Key, ch: char): OutOfMemory!void {
    try app.Say(c, "")
    page := app.ListRows()
    match key {
        .Up { app.Pane(app.active).cursor -= 1 }
        .Down { app.Pane(app.active).cursor += 1 }
        .PageUp { app.Pane(app.active).cursor -= page }
        .PageDown { app.Pane(app.active).cursor += page }
        .Home { app.Pane(app.active).cursor = 0 }
        // Settle clamps it to the last entry
        .End { app.Pane(app.active).cursor = 1 << 30 }
        .Enter | .Right { try Open(app, c) }
        .Backspace | .Left { try UpDir(app, c) }
        .Tab { app.active = 1 - app.active }
        .Delete { try Ask(app, c, Action.Delete) }
        .F(n) {
            match n {
                3 { try Open(app, c) }
                5 { try Ask(app, c, Action.Copy) }
                6 { try Ask(app, c, Action.Move) }
                7 { try Ask(app, c, Action.MakeDir) }
                8 { try Ask(app, c, Action.Delete) }
                10 { app.quit = some(0) }
                _ {}
            }
        }
        .Char {
            match ch {
                'k' { app.Pane(app.active).cursor -= 1 }
                'j' { app.Pane(app.active).cursor += 1 }
                'h' { try UpDir(app, c) }
                'v' { try Open(app, c) }
                'c' { try Ask(app, c, Action.Copy) }
                'm' { try Ask(app, c, Action.Move) }
                'd' { try Ask(app, c, Action.Delete) }
                'n' { try Ask(app, c, Action.MakeDir) }
                'l' { try Ask(app, c, Action.Link) }
                '!' { try Ask(app, c, Action.Shell) }
                'g' { try Ask(app, c, Action.Goto) }
                's' { try Sizes(app, c) }
                't' { try GoTemp(app, c) }
                '=' { try Mirror(app, c) }
                'r' { try app.Say(c, "reloaded") }
                'q' { app.quit = some(0) }
                _ {}
            }
        }
        _ {}
    }
}

PagerKey(app: mut App, c: &Ctx, key: term.Key, ch: char): OutOfMemory!void {
    (lines, fit) := try PageLines(&*app, c)
    var top := app.page_top
    match key {
        .Up { top -= 1 }
        .Down | .Enter { top += 1 }
        .PageUp { top -= fit }
        .PageDown { top += fit }
        .Home { top = 0 }
        .End { top = lines }
        .Escape | .Left { app.mode = Mode.Browse }
        .Char {
            match ch {
                'k' { top -= 1 }
                'j' { top += 1 }
                ' ' { top += fit }
                'b' { top -= fit }
                'g' { top = 0 }
                'G' { top = lines }
                'q' { app.mode = Mode.Browse }
                _ {}
            }
        }
        _ {}
    }
    last := if lines > fit { lines - fit } else { 0 }
    app.page_top = if top > last { last } else if top < 0 { 0 } else { top }
}

PromptKey(app: mut App, c: &Ctx, t: mut term.Term, log: mut Writer, key: term.Key, ch: char, ctrl: bool): Error!void {
    match key {
        .Enter {
            app.mode = Mode.Browse
            input := try c.frame.Dup(app.input.Str())
            try Perform(app, c, t, log, input)
        }
        .Escape {
            app.mode = Mode.Browse
            try app.Say(c, "cancelled")
        }
        .Backspace {
            s := try c.frame.Dup(app.input.Str())
            try app.input.Set(c.al, s[..LastScalar(s)])
        }
        .Char {
            if !ctrl {
                var one := Text.New(c.frame)
                try one.PushChar(ch)
                try app.input.Push(c.al, one.Str())
            }
        }
        _ {}
    }
}

// Where the last scalar of `s` starts, 0 for an empty `s`.
LastScalar(s: str): int {
    var i := s.Len()
    if i == 0 {
        return 0
    }
    i -= 1
    for i > 0 && s.Byte(i) & 0xC0 == 0x80 {
        i -= 1
    }
    return i
}

ConfirmKey(app: mut App, c: &Ctx, t: mut term.Term, log: mut Writer, ch: char): Error!void {
    app.mode = Mode.Browse
    if ch != 'y' && ch != 'Y' {
        try app.Say(c, "cancelled")
        return
    }
    subject := try c.frame.Dup(app.subject.Str())
    try Perform(app, c, t, log, subject)
}

// A paste goes into a prompt's text, up to its first line end; in the
// panes it is a directory to go to.
OnPaste(app: mut App, c: &Ctx, pasted: str): Error!void {
    var first := pasted
    if i := pasted.Find("\n") {
        first = pasted[..i]
    }
    if first.EndsWith("\r") {
        first = first[..first.Len() - 1]
    }
    match app.mode {
        .Prompt { try app.input.Push(c.al, first) }
        .Browse {
            try app.Say(c, "")
            try GoTo(app, c, first.Trim())
        }
        _ {}
    }
}

// A left click selects the row under the pointer in either pane, and the
// wheel moves the selection or the page by three rows.
OnMouse(app: mut App, c: &Ctx, button: int, row: int, col: int): OutOfMemory!void {
    if app.mode == Mode.Pager {
        if button == 64 || button == 65 {
            (lines, fit) := try PageLines(&*app, c)
            step := if button == 64 { -3 } else { 3 }
            var top := app.page_top + step
            last := if lines > fit { lines - fit } else { 0 }
            if top > last {
                top = last
            }
            app.page_top = if top < 0 { 0 } else { top }
        }
        return
    }
    if app.mode != Mode.Browse {
        return
    }
    match button {
        0 {
            if row < 1 || row > app.ListRows() {
                return
            }
            app.active = if col < app.cols / 2 { 0 } else { 1 }
            var p := app.Pane(app.active)
            p.cursor = p.top + row - 1
        }
        64 { app.Pane(app.active).cursor -= 3 }
        65 { app.Pane(app.active).cursor += 3 }
        _ {}
    }
}
