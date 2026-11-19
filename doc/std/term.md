# std.term

`std.term` drives the controlling terminal: raw mode, keys, pastes, mouse and focus reports, the alternate screen, cursor movement, colour and styles. Reach for it to write an editor, a pager, a prompt or a full-screen tool. To run a program on a terminal of its own (a pseudo-terminal), use [`std.proc`](proc.md); to measure text in columns, use [`text.Width`](text.md#clusters-and-width).

Read the [library guide](README.md) first.

```mk
// verdict: parse
import std.term
```

## Contents

- [The main pieces](#the-main-pieces)
- [The terminal and raw mode](#the-terminal-and-raw-mode)
- [Reading events](#reading-events)
- [Mouse, focus and kitty keys](#mouse-focus-and-kitty-keys)
- [Writing to the terminal](#writing-to-the-terminal)
- [Colour](#colour)
- [Passwords](#passwords)
- [Errors](#errors)
- [Memory, cost and cancellation](#memory-cost-and-cancellation)
- [Under a simulated Io](#under-a-simulated-io)
- [Examples](#examples): [styles and escape sequences](#styles-and-escape-sequences), [read keys](#read-keys), [read a password](#read-a-password), [a full-screen program](#a-full-screen-program)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Open the terminal | `term.Open`, which returns a linear `Term`; end it with `Term.Close` |
| Keys one at a time, unechoed | `Term.Raw`, then `Term.Restore` |
| Read keys, pastes, resizes, mouse | `Term.Read`, which returns an `Event` |
| Read a password | `Term.ReadSecret` |
| Size in cells | `Term.Size` |
| Write to the terminal | `Term.Writer` |
| Move, clear, switch screens | `MoveTo`, `Clear`, `AltScreen`, `ShowCursor` |
| Turn on input reports | `BracketedPaste`, `MouseCapture`, `KittyKeys` |
| Colour and attributes | `Style`, `Color`, `Style.Format` |
| Decide whether to colour | `UseColor`, `IsTerminal`, `Std` |
| Print untrusted text | `Sanitize` |

The functions that write escape sequences (`MoveTo`, `Clear`, `AltScreen`, `Sanitize` and the rest) take any `Writer`, so they also write into a `Text` or a pipe. They allocate nothing and fail only with the `Writer`'s error.

## The terminal and raw mode

`term.Open` opens the process's controlling terminal (`/dev/tty` on Linux, `CONIN$` and `CONOUT$` on Windows), so a tool reads keys even when its standard input is a pipe. A process with no terminal gets `NotTerminal`. A `Term` is **linear**: every path must end it with `Close`, which cannot fail and puts the terminal back as it was. A `Term` is `Send` and `Share`; `Read` and `Writer` hold it exclusively, so a `Writer` must be done before the next `Read`.

`Term.Raw` puts the terminal in raw mode: keys arrive one by one, unechoed, with no line editing, and Ctrl+C and Ctrl+Z arrive as keys rather than signals. Output is not translated either, so end a line with `"\r\n"`. `Restore` puts back the mode found, and so does `Close`.

- The mode `Raw` saved is registered with the runtime. On a panic, a `proc.Exit` or a fatal signal the runtime handles, the runtime restores that mode, turns mouse capture off, pops the kitty flags, and leaves the alternate screen.
- The runtime's table holds four terminals' records. When it is full, a later one is not registered, and only `Restore` and `Close` put its mode back. The same holds when the process outlives such a restore, as when another console handler handles the event.
- On Linux, while raw mode is in force, the runtime sets raw mode again on SIGCONT when the process is in the terminal's foreground group. A program that was stopped and continued is raw again, whether or not it reads, so call `Restore` before you hand the terminal to another program.
- On Linux, a process outside the terminal's foreground group is stopped by SIGTTOU at `Raw`, as every Unix program is.
- On Windows, `Raw` is `Unsupported` on a console without virtual-terminal input.

## Reading events

`Term.Read` waits for the next input and returns one `Event`:

| Event | Fields |
|---|---|
| `Key` | `key: Key`, `ch: char`, `mods: Mods` |
| `Paste` | `text: str`, the text of a bracketed paste |
| `Resize` | `cols`, `rows`, the new size |
| `Mouse` | `button`, `row`, `col`, `mods` |
| `Focus` | `on`: true when the terminal gains focus |

Keys:

- `Key.Char` is any character, and `ch` holds it. For every other key (`Enter`, `Escape`, `Backspace`, `Tab`, the arrows, `Home`, `End`, `PageUp`, `PageDown`, `Insert`, `Delete`, `F(1)` to `F(20)`), `ch` is `'\0'`.
- Control bytes are keys, as the next table shows.
- A lone escape is the `Escape` key once 25 ms pass with nothing after it. An escape before a key adds alt. `ESC ]`, `ESC P`, `ESC _`, `ESC ^` and `ESC X` with nothing after them within that wait are those keys with alt.
- `Mods.shift` is set only where the terminal's sequence states it: an upper-case letter arrives as itself, without shift.
- `Read` decodes the kitty keyboard protocol's `CSI u` keys and xterm's modifyOtherKeys keys, and drops key releases in every form.
- Ill-formed UTF-8 becomes U+FFFD, one for each maximal ill-formed part.
- Any other control sequence or string the terminal sends (CSI, OSC, DCS, APC, PM, SOS) is dropped whole, however long, and never read as keys.

Control bytes:

| Byte | Event |
|---|---|
| `0x0D` | `Enter` |
| `0x09` | `Tab` |
| `0x7F` | `Backspace` |
| `0x00` | `Char` space with ctrl |
| the other bytes `0x01` to `0x1A` | `Char` `a` to `z` with ctrl |
| `0x1C` to `0x1F` | `Char` `4` to `7` with ctrl |

On Windows a console key record with no character is dropped, so no 0x00 arrives there.

Pastes:

- Turn bracketed paste on with `BracketedPaste` to get a paste as `Paste` events instead of keys.
- A paste longer than the `Term`'s 4,096-byte buffer arrives as several `Paste` events, in order, each cut on a UTF-8 boundary.
- On Windows the console removes the paste markers from a paste it receives while its input is not in virtual-terminal mode, so that paste arrives as keys. `Raw` sets that mode, so call `Raw` before a paste can come.

## Mouse, focus and kitty keys

`MouseCapture(w, true)` turns on mouse reports (button presses and releases, moves with a button held, the wheel) in xterm's SGR form, and focus reports; `false` turns them off. Written through a `Term`'s `Writer`, capture is recorded: `Restore` turns it off first, on every target, and `Close` and the runtime turn it off too.

- `Event.Mouse`'s `row` and `col` count from 0.
- `button` is one of these codes:

| `button` | Meaning |
|---|---|
| `0`, `1`, `2` | The left, middle and right buttons |
| `3` | A release |
| `64` to `67` | The wheel up, down, left and right |
| `128` to `131` | Buttons 8 to 11 |

32 is added while the pointer moves. `mods` holds the modifiers the terminal reports.

`KittyKeys(w, true)` pushes the kitty keyboard protocol's "disambiguate" flag, so the terminal reports Escape, and keys such as Ctrl+I and Tab, in a form `Read` tells apart; `false` pops it. A terminal without the protocol ignores both. The protocol keeps one stack per screen, so a `Term`'s `Writer` counts the pushes left on each screen, and `Close` pops them, the alternate screen's before it leaves that screen. `Restore` leaves them.

## Writing to the terminal

`Term.Writer(io, buf)` returns a `Writer` that buffers in `buf`; each flush writes every byte. It passes bytes as they are, so send untrusted text through `Sanitize`.

- `MoveTo(w, row, col)` counts from 0 at the top left and clamps both to 0 through 65,534.
- `AltScreen` enters or leaves the alternate screen, saving and restoring the cursor. Written through a `Term`'s `Writer`, the switch is recorded, so `Close`, and the runtime on a panic or an exit, leave it. Written to any other `Writer`, it is not.
- `Sanitize(w, s)` writes `s` with every control character replaced by U+FFFD: every C0 control except line feed and tab, DEL, and every C1 control. So a file name or a peer's message cannot move the cursor, change a mode, set the title or answer a query.

## Colour

A `Style` holds a foreground and a background `Color` (`none` keeps the terminal's default), bold, underline and reverse video. A `Color` is `Ansi(n)`, one of the 256 indexed colours, or `Rgb(r, g, b)`. `Style.Format(w)` writes the sequence that sets exactly that style: a reset, then each attribute. `Style` does not implement the `Format` interface, so call `Format` directly rather than putting a style in a format string's hole.

`UseColor(io, s)` says whether to write colour to the standard stream `s`, a `term.Std` (`Stdin`, `Stdout` or `Stderr`), as in `if term.UseColor(sys.io, term.Std.Stdout) { ... }`:

- It is false when the environment variable `NO_COLOR` is set to a non-empty value, or when `s` is not a terminal. It reads `NO_COLOR` as the process started, as `System.Env` does, and allocates nothing.
- On a Windows console, `UseColor(Stdout)` and `UseColor(Stderr)` turn on the output's virtual-terminal processing when it is off, and are false when the console cannot enable it. The mode is left on. Stdin's mode is never written.
- Asked while raw mode is in force, it sees the bit `Raw` set, which `Restore` takes away again on a console that lacked it. Ask before `Raw` or after `Restore`.

## Passwords

`Term.ReadSecret` reads one line in raw mode with nothing echoed, and puts back the mode it found when the line ends.

- Enter or a line feed (Ctrl+J) ends the line. Backspace and Ctrl+H erase one character, and Ctrl+U the whole line. Ctrl+C is `Canceled`.
- A paste is appended without its line ends. Other keys are ignored, and so is input past 1,024 bytes.
- A cancel or a deadline keeps what was typed for the next call, and `Close` wipes it. Raw mode stays in force with the kept line, so keys typed before the next call are neither echoed nor held for a line. The mode found is put back when the line ends, or by `Restore` or `Close`.

## Errors

Every operation of a `Term` fails with a member of `term.Error`:

| Member | Meaning |
|---|---|
| `NotTerminal` | No controlling terminal, or the handle or stream is not one |
| `Device(code)` | The terminal refused an operation, with the OS's code |
| `Unsupported` | This target or this `Io` cannot do it, such as a Windows console without virtual-terminal input |
| `Canceled` | The scope was cancelled or its deadline passed (from the prelude) |
| `OutOfMemory` | An arena or the process heap could not hold the result |

`term.Error` is open, so a `match` on it keeps a `_` arm. The escape-sequence functions fail only with `WriteError`.

## Memory, cost and cancellation

A `Term` owns one or two OS handles and a fixed input buffer, and no allocator memory. `Read` allocates only a paste's text, at most 4,096 bytes an event, in the arena you pass; `ReadSecret` allocates the line. `Open` takes one or two handle records from the process heap, and `Close` frees them. Everything else allocates nothing.

`Read` and `ReadSecret` can be cancelled. When the scope is cancelled or its deadline passes, `Read` fails with `Canceled` and nothing consumed, so the same call again returns what this one would have. `ReadSecret` keeps the line, as [Passwords](#passwords) says. `Open`, `IsTerminal`, `Raw`, `Restore`, `Size` and `Close` are not cancellation points.

## Under a simulated Io

Under a simulated `Io` from [`std.sim`](sim.md), `term.Open` opens the `Sim`'s one terminal, 80 by 24, so a terminal program can be tested without a terminal:

- What it writes is discarded. `Raw` and `Restore` record a mode and change nothing.
- Its input is what `Sim.TermInput` queued. Each call's bytes arrive together after its delay on the simulated clock, never before earlier input. They come in the target's own form: the bytes on Linux, and on Windows one key-down console record per UTF-16 unit.
- `Read` waits on that clock, and a deadline or a cancel cuts the wait with nothing consumed. When nothing is queued, no deadline is set and nothing in the run can queue more, `Read` and `ReadSecret` are `Canceled`.
- No standard stream of a `Sim` is a terminal, so `IsTerminal` and `UseColor` are false.
- Faults and recordings treat the terminal as std.sim says for every module: see [faults](sim.md#faults) and [recording and replay](sim.md#recording-and-replay).

## Examples

### Styles and escape sequences

These functions write to any `Writer`. Here they write into a `Text`, and the program prints the bytes with the escape byte spelled `ESC`.

```mk
// verdict: ok
import std.term

// Styled is a warning in bold yellow, then plain text.
Styled(w: mut Writer): WriteError!void {
    warn := term.Style(fg: some(term.Color.Ansi(n: 3)), bg: none, bold: true, underline: false, reverse: false)
    plain := term.Style(fg: none, bg: none, bold: false, underline: false, reverse: false)
    try warn.Format(w) // a Style writes the escape sequence that sets it
    try w.Str("warning")
    try plain.Format(w) // nothing set: back to the terminal's default
    try w.Str(" disk almost full")
}

// Home moves the cursor to row 2, column 10, and clears the screen.
Home(w: mut Writer): WriteError!void {
    try term.MoveTo(w, 2, 10) // row and col count from 0
    try term.Clear(w)
}

// Untrusted writes a file name that tries to set the window title.
Untrusted(w: mut Writer): WriteError!void {
    try term.Sanitize(w, "report.txt\x1b]0;owned\x07")
}

// Show runs out into a text and prints it with the escape byte spelled ESC.
Show(sys: &System, out: fn(mut Writer): WriteError!void): OutOfMemory|WriteError!void {
    var t := Text.New(sys.arena)
    var w := t.Writer()
    try out(mut w)
    try w.Flush()
    shown := try t.Str().Replace(sys.arena, "\x1b", "ESC")
    try sys.Print("{shown}\n", (shown))
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    try Show(sys, Styled)
    try Show(sys, Home)
    try Show(sys, Untrusted)
}
```

```text
$ mk run style.mk
ESC[0;1;33mwarningESC[0m disk almost full
ESC[3;11HESC[HESC[2J
report.txt�]0;owned�
```

- `Sanitize` replaced the escape byte and the BEL byte (0x07) with U+FFFD, so the title change is plain text.

### Read keys

This program reads keys from a simulated terminal, so it runs the same everywhere. With `sys.io` in place of `s.Io()`, the same loop reads the real terminal.

```mk
// verdict: ok
import std.sim
import std.term
import std.time

// Describe names one event.
Describe(a: &Arena, ev: term.Event): OutOfMemory!str from a {
    return match ev {
        term.Event.Key(key, ch, mods) {
            name := match key {
                .Char { try a.Fmt("char {ch}", (ch)) }
                .Enter { "Enter" }
                .Escape { "Escape" }
                .Up { "Up" }
                .F(n) { try a.Fmt("F{n}", (n)) }
                _ { "other key" }
            }
            if mods.ctrl {
                return try a.Fmt("ctrl+{name}", (name))
            }
            if mods.alt {
                return try a.Fmt("alt+{name}", (name))
            }
            name
        }
        term.Event.Paste(text) { try a.Fmt("paste {text}", (text)) }
        term.Event.Resize(cols, rows) { try a.Fmt("resize {cols}x{rows}", (cols, rows)) }
        _ { "mouse or focus" }
    }
}

export Main(sys: &System): term.Error|OutOfMemory|WriteError!void {
    var s := try sim.Sim.New(sys.arena, 1) // a simulated Io with one terminal
    defer s.Free()
    io := s.Io()
    // what a user types, as the terminal sends it
    try s.TermInput("h".AsBytes(), time.Millis(10))
    try s.TermInput("\x1b[A".AsBytes(), time.Millis(10))         // the Up arrow
    try s.TermInput("\x01".AsBytes(), time.Millis(10))           // Ctrl+A
    try s.TermInput("\x1bx".AsBytes(), time.Millis(10))          // Alt+x
    try s.TermInput("\x1bOP".AsBytes(), time.Millis(10))         // F1
    try s.TermInput("\x1b[200~two words\x1b[201~".AsBytes(), time.Millis(10)) // a bracketed paste
    try s.TermInput("\r".AsBytes(), time.Millis(10))             // Enter
    try s.TermInput("q".AsBytes(), time.Millis(10))
    var t := try term.Open(io)
    onerr t.Close(io)
    (cols, rows) := try t.Size(io)
    try sys.Print("terminal {cols}x{rows}\n", (cols, rows))
    try t.Raw(io)
    for {
        ev := try t.Read(sys.arena, io)
        d := try Describe(sys.arena, ev)
        if d == "char q" { // q quits
            break
        }
        try sys.Print("{d}\n", (d))
    }
    try t.Restore(io)
    t.Close(io)
}
```

```text
$ mk run keys.mk
terminal 80x24
char h
Up
ctrl+char a
alt+char x
F1
paste two words
Enter
```

See also the [Read keys from the terminal](../cookbook.md#read-keys-from-the-terminal) recipe.

### Read a password

```mk
// verdict: ok
import std.sim
import std.term
import std.time

export Main(sys: &System): term.Error|OutOfMemory|WriteError!void {
    var s := try sim.Sim.New(sys.arena, 1)
    defer s.Free()
    io := s.Io()
    try s.TermInput("hunter3\x7f2\r".AsBytes(), time.Millis(50)) // 3, Backspace, 2, Enter
    var t := try term.Open(io)
    onerr t.Close(io)
    try sys.Print("password: ")
    pw := try t.ReadSecret(sys.arena, io) // nothing is echoed
    try sys.Print("\nread {n} characters\n", (n: pw.Len()))
    t.Close(io)
}
```

```text
$ mk run secret.mk
password: 
read 7 characters
```

Backspace erased the `3`, so the line is `hunter2`. On a real terminal `ReadSecret` writes the line break after Enter itself; the simulated terminal discards writes, so the program prints its own.

### A full-screen program

This program needs a real terminal, so its output is not shown. It switches to the alternate screen, draws a line in the middle, redraws on a resize, and quits on `q`.

```mk
// verdict: ok
import std.term

// Draw clears the screen and writes msg in its middle. The Writer
// borrows t only while it draws, so Read can use t between draws.
Draw(t: mut term.Term, io: Io, msg: str): term.Error|WriteError!void {
    (cols, rows) := try t.Size(io)
    var buf: [1024]u8 = [0; 1024]
    var w := t.Writer(io, mut buf[..])
    try term.Clear(mut w)
    try term.MoveTo(mut w, rows / 2, (cols - msg.Len()) / 2)
    try w.Str(msg)
    try w.Flush()
}

// Screen enters (on) or leaves the alternate screen, with the cursor
// hidden while it is on. Written through t's Writer, the switch is
// recorded, so Close leaves the alternate screen on every path.
Screen(t: mut term.Term, io: Io, on: bool): WriteError!void {
    var buf: [64]u8 = [0; 64]
    var w := t.Writer(io, mut buf[..])
    try term.AltScreen(mut w, on)
    try term.ShowCursor(mut w, !on)
    try w.Flush()
}

export Main(sys: &System): term.Error|WriteError!void {
    var t := try term.Open(sys.io) // the terminal itself, even when stdin is a pipe
    onerr t.Close(sys.io)          // Close puts the terminal back on every path
    try t.Raw(sys.io)
    try Screen(mut t, sys.io, true)
    try Draw(mut t, sys.io, "press q to quit")
    for {
        match try t.Read(sys.arena, sys.io) {
            term.Event.Key(ch) {
                if ch == 'q' {
                    break
                }
            }
            term.Event.Resize {
                try Draw(mut t, sys.io, "press q to quit")
            }
            _ {}
        }
    }
    try Screen(mut t, sys.io, false)
    try t.Restore(sys.io)
    t.Close(sys.io)
}
```

- `Close` in the `onerr` line puts the terminal back on an error path too: it leaves the alternate screen and restores the mode.
- A `Writer` holds the `Term` exclusively, so each draw makes its own `Writer` and is done with it before the next `Read`.

## API

<!-- api:begin -->
Stability: Stable.

### `AltScreen`

```
export AltScreen(w: mut Writer, on: bool): WriteError!void
```

- Errors: WriteError

Enters (on) or leaves the alternate screen, the full-screen buffer that
editors and pagers draw on, saving and restoring the cursor (xterm's
mode 1049). Written through Term.Writer, the switch is recorded, so
Term.Close, and the runtime on a panic or an exit, leave the alternate
screen; written to any other Writer, it is not. Errors are w's. O(1);
allocates nothing.

### `BracketedPaste`

```
export BracketedPaste(w: mut Writer, on: bool): WriteError!void
```

- Errors: WriteError

Turns bracketed paste on or off (mode 2004). On, Term.Read returns a
paste as Event.Paste events rather than as keys. On Windows the console
removes the paste markers while its input is not in virtual-terminal
mode, so the paste arrives as keys: call Term.Raw, which sets that mode,
before a paste can come. Errors are w's. O(1); allocates nothing.

### `Clear`

```
export Clear(w: mut Writer): WriteError!void
```

- Errors: WriteError

Clears the whole screen and moves the cursor to the top left (CUP, then
ED 2). Errors are w's. O(1); allocates nothing.

### `Color`

```
export enum Color
```

A colour: one of the 256 indexed colours (0 to 7 the standard ones, 8 to
15 their bright forms, 16 to 231 a 6 x 6 x 6 colour cube and 232 to 255
greys), or a 24-bit colour.

Variants:

- `Ansi(n: u8)`: One of the 256 indexed colours, n.
- `Rgb(r: u8, g: u8, b: u8)`: A 24-bit colour.

### `Device`

```
export error Device(code: i64)
```

The terminal refused an operation, with the OS's code.

### `Error`

```
export open type Error = NotTerminal | Device | Unsupported | Canceled | OutOfMemory
```

Every way a std.term operation fails.

### `Event`

```
export enum Event
```

One input event, from Term.Read.

- Key: a key with its modifiers. ch is the character for Key.Char, and
  '\0' for every other key.
- Paste: the text of a bracketed paste. A long paste arrives as several
  Paste events, in order.
- Resize: the terminal's new size.
- Mouse: a mouse report. button is 0 left, 1 middle, 2 right, 3 a
  release, 64 to 67 the wheel up, down, left and right, and 128 to 131
  the buttons 8 to 11, plus 32 while the pointer moves. row and col
  count from 0.
- Focus: on is true when the terminal gained focus and false when it
  lost it.

Mouse and Focus come only after MouseCapture turned them on.

Variants:

- `Key(key: Key, ch: char, mods: Mods)`: A key with its modifiers; ch is the character for Key.Char.
- `Paste(text: str)`: The text of a bracketed paste, or a part of a long one.
- `Resize(cols: int, rows: int)`: The terminal's new size.
- `Mouse(button: int, row: int, col: int, mods: Mods)`: A mouse report, once MouseCapture turned them on.
- `Focus(on: bool)`: The terminal gained focus (on) or lost it, once MouseCapture turned it
  on.

### `IsTerminal`

```
export IsTerminal(io: Io, s: Std): bool
```

Whether the standard stream s is a terminal (on Windows, a console).
False for a pipe or a file, and under a simulated Io. Not a
cancellation point. O(1); allocates nothing.

### `Key`

```
export open enum Key
```

A key: Char for any character (the character is Event.Key's ch), the
editing and cursor keys, and F(n) for the function keys F1 to F20.

Variants:

- `Char`: Any character; Event.Key's ch holds it.
- `Enter`
- `Escape`
- `Backspace`
- `Tab`
- `Up`
- `Down`
- `Left`
- `Right`
- `Home`
- `End`
- `PageUp`
- `PageDown`
- `Insert`
- `Delete`
- `F(n: int)`: Function key n, F1 to F20.

### `KittyKeys`

```
export KittyKeys(w: mut Writer, on: bool): WriteError!void
```

- Errors: WriteError

Turns the kitty keyboard protocol's "disambiguate escape codes" mode on
or off. On, a terminal that has the protocol reports Escape, and keys
with ctrl or alt that the usual encoding confuses (Ctrl+I and Tab,
Ctrl+Enter), in a form Term.Read tells apart. A terminal without the
protocol ignores both calls.

On pushes the mode on the terminal's stack (CSI > 1 u) and off pops it
(CSI < u), so pair each on with an off. Written through a Term's
Writer, the pushes left on each screen are counted, and Term.Close pops
them, as the runtime does on a panic, proc.Exit or a fatal signal. Only
pushes and pops are counted: flags a program sets with the protocol's
set form (CSI = flags ; mode u) are not popped for it.

Errors are w's. O(1); allocates nothing.

### `Mods`

```
export struct Mods
```

The modifiers held with a key or a mouse report, as far as the terminal
reports them. shift is set only where the terminal's sequence states
it: an upper-case letter arrives as itself, without shift. alt is set
for a key that followed an escape, and ctrl for a control character.

Fields:

- `shift: bool`: Shift, set only where the terminal's sequence states it.
- `alt: bool`: Alt, set for a key that followed an escape.
- `ctrl: bool`: Ctrl, set for a control character.

### `MouseCapture`

```
export MouseCapture(w: mut Writer, on: bool): WriteError!void
```

- Errors: WriteError

Turns mouse and focus reports on or off. On, Term.Read returns a
button's press and release, a move with a button held, and the wheel as
Event.Mouse, and the terminal gaining or losing focus as Event.Focus.
The sequences are xterm's modes 1000, 1002, 1006 and 1004; off turns
all four off.

Written through Term.Writer, capture is recorded: Term.Restore turns it
off first, and Term.Close and the runtime, on a panic, proc.Exit or a
fatal signal it handles, turn it off before they leave the alternate
screen. Errors are w's. O(1); allocates nothing.

### `MoveTo`

```
export MoveTo(w: mut Writer, row: int, col: int): WriteError!void
```

- Errors: WriteError

Moves the cursor to row and col, counted from 0 at the top left (the
sequence is CUP, which counts from 1). A negative row or col is 0, and
one past 65,534 is 65,534. Errors are w's. O(1); allocates nothing.

### `NotTerminal`

```
export error NotTerminal
```

No terminal: the process has no controlling terminal, or the handle or
stream is not one.

### `Open`

```
export Open(io: Io): Error!Term
```

- Errors: Error (Canceled, OutOfMemory, Device(code: i64), NotTerminal, Unsupported)

Opens this process's controlling terminal: the terminal itself, even
when the standard streams are pipes or files. NotTerminal when there is
none (a service, a daemon, a job with no console); OutOfMemory when the
process heap refuses a handle record; Device for another refusal. Under
a simulated Io it opens the Sim's one terminal, 80 by 24, whose input
Sim.TermInput queues. Not a cancellation point. O(1); allocates one or
two runtime handle records from the process heap.

### `Sanitize`

```
export Sanitize(w: mut Writer, s: str): WriteError!void
```

- Errors: WriteError

Writes s with every control character replaced by U+FFFD, so untrusted
text (a file name, a peer's message) cannot move the cursor, change a
mode, set the title or answer a query. Replaced are U+0000 to U+001F
except line feed and tab, DEL (U+007F), and U+0080 to U+009F, which
some terminals obey; every other scalar is written as it is.

Errors are w's. O(n): short safe runs and the replacements reach w in
blocks of up to 512 bytes, and a run of 510 bytes or more in one
w.Bytes call. It allocates nothing; the block is on the stack.

### `ShowCursor`

```
export ShowCursor(w: mut Writer, on: bool): WriteError!void
```

- Errors: WriteError

Shows (on) or hides the cursor (mode 25). Errors are w's. O(1);
allocates nothing.

### `Std`

```
export enum Std
```

A standard stream of this process.

Variants:

- `Stdin`
- `Stdout`
- `Stderr`

### `Style`

```
export struct Style
```

How text is drawn: a foreground and a background colour (none keeps the
terminal's default), bold, underline and reverse video.

Fields:

- `fg: ?Color`: The foreground colour; none keeps the terminal's default.
- `bg: ?Color`: The background colour; none keeps the terminal's default.
- `bold: bool`: Whether text is bold.
- `underline: bool`: Whether text is underlined.
- `reverse: bool`: Whether foreground and background are swapped.

### `Style.Format`

```
export Style.Format(&self, w: mut Writer): WriteError!void
```

- Errors: WriteError

Writes the SGR escape sequence that sets exactly this style: a reset,
then each attribute. A style with nothing set writes the reset alone
("\x1b[0m"), which returns to the terminal's default. Style does not
implement the Format interface, so a Style cannot fill a hole of a
format string: call this method. Errors are w's. O(1), one w.Bytes
call; allocates nothing.

### `Term`

```
export linear struct Term
```

The controlling terminal, open: read events with Read, write through
Writer, then Close it. Linear: every path must end it with Close. Send
and Share; only Size takes &self, while Read and Writer hold it
exclusively. It owns one or two OS handles and a fixed input buffer,
and no allocator memory.

### `Term.Close`

```
export Term.Close(self, io: Io)
```

Closes the terminal and puts it back as it was. In order: it turns
mouse capture off and leaves the alternate screen if a Writer turned
them on, popping first the kitty keyboard flags a Writer left pushed on
that screen and then those left on the main screen, each sequence
written whole; it puts back a saved mode, wipes a secret a cancelled
ReadSecret left, and releases the handles. It cannot fail and cannot be
cancelled: what the terminal refuses here is ignored. O(1), with at
most 255 kitty pops a screen in at most 16 writes each; frees the
handle records.

### `Term.Raw`

```
export Term.Raw(mut self, io: Io): Error!void
```

- Errors: Error (Canceled, OutOfMemory, Device(code: i64), NotTerminal, Unsupported)

Puts the terminal in raw mode: keys arrive one by one, unechoed, with
no line editing and no signal from Ctrl+C or Ctrl+Z (Read returns them
as keys). Output is not translated, so write "\r\n" to end a line. The
mode in force before is saved for Restore and Close. Calling Raw again
while raw does nothing, except that raw mode a cancelled ReadSecret left
in force then becomes the program's, and that line's end keeps it.

Unsupported on a Windows console without virtual-terminal input; Device
for another refusal.

On Linux, a process outside its terminal's foreground group is stopped
by SIGTTOU here, as every Unix program is. While raw mode is in force, a
SIGCONT that finds the process in the terminal's foreground group sets
raw mode again, whether or not the program reads again, since the shell
that stopped it may have put line mode back; Read also sets it again
before each wait. So call Restore before handing the terminal to
another program: a child in this process's group (one spawned with no
group and no terminal of its own) would have its mode set raw on such a
SIGCONT.

Not a cancellation point. O(1); allocates nothing.

### `Term.Read`

```
export Term.Read(mut self, a: &Arena, io: Io): Error!Event from a
```

- Flows: The result borrows from a.
- Errors: Error (Canceled, OutOfMemory, Device(code: i64), NotTerminal, Unsupported)

Waits for the next input event and returns it: a key, a bracketed
paste, a resize, or a mouse or focus report the program turned on.

Canceled in a cancelled scope or past a deadline, with nothing
consumed: the same call again returns what this one would have. Under a
simulated Io, also Canceled when nothing is queued and nothing in the
run can queue more (a deadlock). OutOfMemory when a cannot hold a
paste's text, which stays unread. Device when the terminal fails a
read.

O(bytes read); allocates a paste's text in a, at most 4,096 bytes an
event, and nothing for any other event.

### `Term.ReadSecret`

```
export Term.ReadSecret(mut self, a: &Arena, io: Io): Error!str from a
```

- Flows: The result borrows from a.
- Errors: Error (Canceled, OutOfMemory, Device(code: i64), NotTerminal, Unsupported)

Reads one line with nothing echoed, for a password or a key. Enter (or
a line feed, Ctrl+J) ends it, Backspace erases a character and Ctrl+U
the whole line. A paste is taken as typed; other keys are ignored.
Characters past 1,024 bytes are ignored. Raw mode is set for the line
when it is not on, and the mode before is put back when the line ends
or fails. A line break is written after Enter and Ctrl+C.

Canceled on Ctrl+C, with what was typed wiped. Canceled in a cancelled
scope or past a deadline, with what was typed kept in the terminal's
state, so the next call goes on with it, and raw mode left in force, so
keys typed before that call are neither echoed nor held for a line. The
next call puts the mode back when its line ends, as Restore and Close
do. Until then the terminal is raw for every purpose: a Ctrl+C typed
meanwhile raises no signal, and the next call reads it and is Canceled.
Close wipes the line. Under a simulated Io, also Canceled, with the line
kept, in a deadlock, as Read is.

O(n); allocates the line in a, and nothing for a paste, whose bytes go
straight into the line.

### `Term.Restore`

```
export Term.Restore(mut self, io: Io): Error!void
```

- Errors: Error (Canceled, OutOfMemory, Device(code: i64), NotTerminal, Unsupported)

Puts back the mode that was in force before Raw (or ReadSecret). First
it turns mouse capture off when a Writer turned it on, on every target,
since on Windows the console forwards no mouse-mode change once its
virtual-terminal input is off. With no mode saved, there is nothing
more to do. Device when the terminal refuses either step: the mode is
put back even when turning capture off failed, and a mode the terminal
refused stays saved, for Close. Not a cancellation point. O(1);
allocates nothing.

### `Term.Size`

```
export Term.Size(&self, io: Io): Error!(cols: int, rows: int)
```

- Errors: Error (Canceled, OutOfMemory, Device(code: i64), NotTerminal, Unsupported)

The terminal's size in character cells. NotTerminal when no size can
be read; 80 by 24 under a simulated Io. Not a cancellation point.
O(1); allocates nothing.

### `Term.Writer`

```
export Term.Writer(mut self, io: Io, buf: mut []u8): Writer from self, buf
```

- Flows: The result borrows from self and buf.

A Writer to the terminal that buffers in buf. Each flush writes until
every byte is out. The Writer passes every byte as it is: send
untrusted text through term.Sanitize, never straight to it.

The Writer records the modes written through it. After term.AltScreen,
Close, and the runtime on a panic or an exit, leave the alternate
screen. After term.MouseCapture, they and Restore turn capture off.
Kitty keyboard flags pushed with term.KittyKeys and left pushed are
popped by Close and the runtime; Restore leaves them.

Errors are WriteError. O(1); allocates nothing.

### `Unsupported`

```
export error Unsupported
```

This target or this Io cannot do it: for example, a Windows console
without virtual-terminal input.

### `UseColor`

```
export UseColor(io: Io, s: Std): bool
```

Whether a program should write colour to the standard stream s. False
when the environment variable NO_COLOR is set and not empty
(no-color.org), when s is not a terminal (IsTerminal), or when s is
Stdout or Stderr on a Windows console that cannot read escape
sequences; true otherwise. Never fails: under a simulated Io, s is no
terminal.

NO_COLOR is read from the environment as the process started, so a
later change to the environment is not seen.

On a Windows console whose output mode lacks virtual-terminal
processing (ENABLE_VIRTUAL_TERMINAL_PROCESSING), UseColor(Stdout) or
UseColor(Stderr) turns it on, and is false only when the console
refuses it (a classic console without virtual-terminal support). The
mode stays on after the process ends. Asked while a Term's Raw mode is
in force, it sees the mode Raw set, which Restore and the exit take
away again on a console that lacked it: ask before Raw or after
Restore. A standard output stream that is the console's input buffer
answers by its echo setting, with nothing written. Do not call it while
another task changes the terminal's mode: the mode is read and written
back with the bit, so a change between the two is lost. Stdin's mode is
never written.

O(environment) to find NO_COLOR, then one system call, or three when
the mode is off (the one time it is turned on, or every time on a
console that refuses it); allocates nothing.

<!-- api:end -->
