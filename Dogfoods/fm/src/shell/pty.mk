// Why: `!` runs a shell command that must see a real terminal, as it would
// from a shell.
// What: RunOnPty and its Result, Forward, and the output cleaners Clean,
// TrimBlank and FirstLine.
// How: the command runs through the system shell on a new pseudo-terminal
// (proc.Pty); keys and resizes are forwarded to it, and its output is read
// up to OutputLimit with control sequences stripped.

import std.atomic
import std.proc
import std.term

/// What a command run on a terminal left: its exit code (`none` when a
/// signal ended it), the bytes it wrote, with the terminal's control
/// sequences removed, whether its output was cut at the buffer's end, and
/// the terminal's size when it ended (fm's terminal may be resized while
/// the command runs).
export struct Result {
    export code: ?int
    export output: str
    export cut: bool
    export cols: int
    export rows: int
}

/// The most output `RunOnPty` keeps.
export const OutputLimit: int = 65536

/// Runs `command` through the system shell (`cmd.exe /d /c` on Windows,
/// `/bin/sh -c` elsewhere) on a new pseudo-terminal of `cols` by `rows`
/// opened with `proc.Pty.Open`, in directory `dir`. The terminal is the
/// child's standard streams and controlling terminal (`Command.terminal`),
/// so the command sees a terminal, as it would under fm. While it runs,
/// what is typed on `t` is passed to it (`Pty.WriteAll`) and a resize of
/// `t` resizes its terminal (`Pty.Resize`); its output is read up to
/// `OutputLimit` bytes. A line goes to `log` when it starts and when it
/// ends, and after it ends the next key typed returns (it is not passed on). Errors are the
/// terminal's and the spawn's; a cancel kills the child. O(output);
/// allocates the output and the child's argument list in `a`, and each
/// event of `t` in an arena on `h`.
export RunOnPty(a: &Arena, h: &Heap, io: Io, t: mut term.Term, log: mut Writer, command: str, dir: str, cols: int, rows: int): proc.Error|WriteError|OutOfMemory!Result from a {
    pty := try proc.Pty.Open(io, cols, rows)
    onerr pty.Close()
    var c := proc.DefaultCommand
    c.args = try ShellArgs(a, command)
    c.dir = some(dir)
    c.terminal = some(&pty)
    (child, stdin, stdout, stderr) := try proc.Spawn(a, io, &c)
    onerr child.Detach()
    CloseEnds(stdin, stdout, stderr)
    try Print(log, "shell started: {command}\n", (command))
    try log.Flush()
    var buf := try a.Slice[u8](OutputLimit)
    var n := 0
    var cut := false
    var size := Size(cols: cols, rows: rows)
    ended := atomic.AtomicBool.New(false)
    var status: ?proc.Status = none
    scope s := io.Scope() {
        s.go Drain(io, &pty, mut buf[..], mut n, mut cut)
        s.go Forward(h, io, t, &pty, &ended, mut size)
        st := try child.Wait(io)
        status = some(st)
        ended.Store(true)
        code := st.code else { -1 }
        try Print(log, "shell ended: exit {code}; press a key\n", (code))
        try log.Flush()
    }
    pty.Close()
    // the scope set status before it could exit without an error
    st := status.Must()
    output := TrimBlank(try Clean(a, buf[..n]))
    return Result(code: st.code, output: output, cut: cut, cols: size.cols, rows: size.rows)
}

struct Size {
    cols: int
    rows: int
}

// Passes keys and pastes typed on fm's terminal to the child's, as a
// terminal sends them, and resizes the child's with fm's, until the first
// key after the child ended. It never cancels a terminal read: a key typed
// after a cancelled read is lost on Windows.
Forward(h: &Heap, io: Io, t: mut term.Term, pty: &proc.Pty, ended: &atomic.AtomicBool, size: mut Size) {
    var events := Arena.New(h) else {
        return
    }
    defer events.Free()
    var one: [8]u8 = [0; 8]
    for {
        events.Reset()
        ev := t.Read(&events, io) else {
            return
        }
        match ev {
            .Key(key, ch, mods) {
                if ended.Load() {
                    return
                }
                n := KeyBytes(key, ch, mods.ctrl, mut one)
                pty.WriteAll(io, one[..n]) else {}
            }
            .Paste(text) {
                pty.WriteAll(io, text.AsBytes()) else {}
            }
            .Resize(cols, rows) {
                pty.Resize(io, cols, rows) else {}
                size.cols = cols
                size.rows = rows
            }
            _ {}
        }
    }
}

// The bytes an xterm sends for a key, written into `out`, and how many.
KeyBytes(key: term.Key, ch: char, ctrl: bool, out: mut [8]u8): int {
    seq := match key {
        .Char {
            if ctrl && ch >= 'a' && ch <= 'z' {
                out[0] = Trunc[u8](u32(ch) - u32('a') + 1)
                return 1
            }
            return Utf8(ch, out)
        }
        .Enter { "\r" }
        .Escape { "\x1b" }
        .Backspace { "\x7f" }
        .Tab { "\t" }
        .Up { "\x1b[A" }
        .Down { "\x1b[B" }
        .Right { "\x1b[C" }
        .Left { "\x1b[D" }
        .Home { "\x1b[H" }
        .End { "\x1b[F" }
        _ { "" }
    }
    for i in 0..seq.Len() {
        out[i] = seq.Byte(i)
    }
    return seq.Len()
}

// The UTF-8 encoding of `ch`, written into `out`, and its length.
Utf8(ch: char, out: mut [8]u8): int {
    c := u32(ch)
    if c < 0x80 {
        out[0] = Trunc[u8](c)
        return 1
    }
    if c < 0x800 {
        out[0] = Trunc[u8](0xC0 | c >> 6)
        out[1] = Trunc[u8](0x80 | c & 0x3F)
        return 2
    }
    if c < 0x10000 {
        out[0] = Trunc[u8](0xE0 | c >> 12)
        out[1] = Trunc[u8](0x80 | c >> 6 & 0x3F)
        out[2] = Trunc[u8](0x80 | c & 0x3F)
        return 3
    }
    out[0] = Trunc[u8](0xF0 | c >> 18)
    out[1] = Trunc[u8](0x80 | c >> 12 & 0x3F)
    out[2] = Trunc[u8](0x80 | c >> 6 & 0x3F)
    out[3] = Trunc[u8](0x80 | c & 0x3F)
    return 4
}

ShellArgs(a: &Arena, command: str): OutOfMemory![]str from a {
    var args := List[str].New(a)
    if Target.os == Os.Windows {
        try args.Push("cmd.exe")
        try args.Push("/d")
        try args.Push("/c")
    } else {
        try args.Push("/bin/sh")
        try args.Push("-c")
    }
    try args.Push(try a.Dup(command))
    done := args.Done()
    return done[..]
}

CloseEnds(i: ?proc.PipeWriter, o: ?proc.PipeReader, e: ?proc.PipeReader) {
    if w := i {
        w.Close()
    }
    if r := o {
        r.Close()
    }
    if r := e {
        r.Close()
    }
}

// Reads the terminal until the child is gone (Pty.Read returns 0) or the
// buffer is full; past that it reads and drops, so the child never blocks
// on a full terminal.
Drain(io: Io, pty: &proc.Pty, buf: mut []u8, n: mut int, cut: mut bool) {
    var spill: [512]u8 = [0; 512]
    for {
        got := if *n < buf.Len() {
            pty.Read(io, mut buf[*n..]) else {
                return
            }
        } else {
            pty.Read(io, mut spill[..]) else {
                return
            }
        }
        if got == 0 {
            return
        }
        if *n < buf.Len() {
            *n += got
        } else {
            *cut = true
        }
    }
}

/// Removes what a terminal would act on rather than show from `raw`: CSI
/// sequences (ESC `[` to a final byte), OSC, DCS, APC, PM and SOS strings
/// (to BEL or ESC `\`), two-byte escapes, and carriage returns; other
/// control bytes but newline and tab become spaces. Bytes that are not
/// UTF-8 become `?`. O(n); allocates the result in `a`.
export Clean(a: &Arena, raw: []u8): OutOfMemory!str from a {
    out := try a.Slice[u8](raw.Len())
    var n := 0
    var i := 0
    for i < raw.Len() {
        b := raw[i]
        if b == 0x1B && i + 1 < raw.Len() {
            i = SkipEscape(raw, i)
            continue
        }
        i += 1
        if b == b'\r' || b == 0x1B {
            continue
        }
        out[n] = if b < 0x20 && b != b'\n' && b != b'\t' { b' ' } else { b }
        n += 1
    }
    return str.From(out[..n]) else {
        for j in 0..n {
            if out[j] >= 0x80 {
                out[j] = b'?'
            }
        }
        // every byte at or past 0x80 was made `?`, so what is left is ASCII
        str.From(out[..n]).Must()
    }
}

// The index just past the escape sequence that starts at raw[i], an ESC.
SkipEscape(raw: []u8, i: int): int {
    kind := raw[i + 1]
    var j := i + 2
    if kind == b'[' {
        for j < raw.Len() && (raw[j] < 0x40 || raw[j] > 0x7E) {
            j += 1
        }
        return if j < raw.Len() { j + 1 } else { j }
    }
    if kind == b']' || kind == b'P' || kind == b'_' || kind == b'^' || kind == b'X' {
        for j < raw.Len() {
            if raw[j] == 0x07 {
                return j + 1
            }
            if raw[j] == 0x1B && j + 1 < raw.Len() && raw[j + 1] == b'\\' {
                return j + 2
            }
            j += 1
        }
        return j
    }
    return i + 2
}

/// The first line of `s` that holds more than spaces, trimmed, or `""`.
/// O(n); allocates nothing.
export FirstLine(s: str): str {
    for line in s.Lines() {
        t := line.Trim()
        if t.Len() > 0 {
            return t
        }
    }
    return ""
}

/// `s` without the lines of nothing but spaces at its start and end, which
/// a pseudo-console adds as it redraws its screen. O(n); allocates nothing.
export TrimBlank(s: str): str {
    var start := 0
    var end := s.Len()
    for line in s.Lines() {
        if line.Trim().Len() > 0 {
            break
        }
        start += line.Len()
        if start < s.Len() && s.Byte(start) == b'\r' {
            start += 1
        }
        if start < s.Len() && s.Byte(start) == b'\n' {
            start += 1
        }
    }
    for end > start && IsSpace(s.Byte(end - 1)) {
        end -= 1
    }
    return s[start..end]
}

IsSpace(b: u8): bool {
    return b == b' ' || b == b'\n' || b == b'\r' || b == b'\t'
}
