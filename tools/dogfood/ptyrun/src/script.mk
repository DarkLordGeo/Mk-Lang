// Why: a terminal case's .pty script is a small language of its own; its
// parser is apart from ptyrun's process handling so it can be tested alone.
//
// What: Parse into a Script of Steps (Act: Input, Resize, Expect, Sleep),
// the ScriptError set, and Unescape, Keys, KeyBytes and Paste for the bytes
// a step types.
//
// How: the format is Dogfoods/README.md's ("Terminal cases"): one step a
// line, `#` comments and blank lines skipped. `size C R` before any other
// step sets the terminal's first size (80 by 24 when absent); `keys TEXT`
// types TEXT, escapes decoded; `key NAME...` types named keys; `paste TEXT`
// types TEXT as one bracketed paste; `resize C R` changes the size; `expect
// N` waits until the transcript holds N complete lines; `sleep MS` waits.
// Keys are the bytes xterm sends, which a Windows pseudo-console turns into
// the console's key records.

/// What one step does.
enum Act {
    /// Writes these bytes as the terminal's input, in one write.
    Input(bytes: []u8)
    /// Sets the terminal's size.
    Resize(cols: int, rows: int)
    /// Waits until the transcript holds at least this many complete lines.
    Expect(lines: int)
    /// Waits this many milliseconds.
    Sleep(ms: int)
}

/// One step and the script line it came from (1-based).
struct Step {
    line: int
    act: Act
}

/// A parsed script: the terminal's first size and its steps.
struct Script {
    cols: int
    rows: int
    steps: []Step
}

/// A line that is no step: an unknown word, or a step missing its operands.
error BadStep(line: int)
/// A number out of its step's range, or no number.
error BadNumber(line: int)
/// A backslash escape other than \e \r \n \t \\ \xHH, or an empty text.
error BadEscape(line: int)
/// A key name the table below does not hold.
error UnknownKey(line: int)
/// A `size` step after another step.
error LateSize(line: int)

/// Every way a script fails to parse.
type ScriptError = BadStep | BadNumber | BadEscape | UnknownKey | LateSize | OutOfMemory

/// The largest terminal side a pseudo-terminal takes on both targets.
const MaxSide: int = 32767

/// The longest `sleep`, in milliseconds.
const MaxSleep: int = 60000

/// Parses a script's text. Errors name the 1-based line. O(len(text));
/// allocates the steps and their input bytes in a.
Parse(a: &Arena, text: str): ScriptError!Script from a {
    var steps := List[Step].New(a)
    var cols := 80
    var rows := 24
    var line := 0
    for l in text.Lines() {
        line += 1
        t := l.Trim()
        if t.Len() == 0 || t.StartsWith("#") {
            continue
        }
        (word, rest) := Head(t)
        if word == "size" {
            if steps.Len() > 0 {
                fail LateSize(line: line)
            }
            (c, r) := try Pair(rest, line, MaxSide)
            cols = c
            rows = r
            continue
        }
        act := match word {
            "keys" { Act.Input(bytes: try Unescape(a, rest, line)) }
            "paste" { Act.Input(bytes: try Paste(a, try Unescape(a, rest, line))) }
            "key" { Act.Input(bytes: try Keys(a, rest, line)) }
            "resize" {
                (c, r) := try Pair(rest, line, MaxSide)
                Act.Resize(cols: c, rows: r)
            }
            "expect" { Act.Expect(lines: try Number(rest, line, 1, 1000000)) }
            "sleep" { Act.Sleep(ms: try Number(rest, line, 0, MaxSleep)) }
            _ { fail BadStep(line: line) }
        }
        try steps.Push(Step(line: line, act: act))
    }
    return Script(cols: cols, rows: rows, steps: steps.Done()[..])
}

/// Splits a trimmed line at its first space: the step's word and the rest,
/// the rest without that one space (a text keeps every later space).
Head(t: str): (word: str, rest: str) from t {
    i := t.Find(" ") else {
        return (word: t, rest: t[t.Len()..])
    }
    return (word: t[..i], rest: t[i + 1..])
}

/// One number in lo to hi, the whole of s.
Number(s: str, line: int, lo: int, hi: int): BadNumber!int {
    n := s.Trim().ParseInt() else {
        fail BadNumber(line: line)
    }
    if n < i64(lo) || n > i64(hi) {
        fail BadNumber(line: line)
    }
    return Trunc[int](n)
}

/// Two numbers, each in 1 to hi: columns and rows.
Pair(s: str, line: int, hi: int): (BadNumber | BadStep)!(c: int, r: int) {
    var w := s.Words()
    a := w.Next() else {
        fail BadStep(line: line)
    }
    b := w.Next() else {
        fail BadStep(line: line)
    }
    if w.Next().IsSome() {
        fail BadStep(line: line)
    }
    return (c: try Number(a, line, 1, hi), r: try Number(b, line, 1, hi))
}

/// The value of a hexadecimal digit.
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

/// The bytes of s with its escapes decoded: \e (escape), \r, \n, \t, \\
/// and \xHH. Any other byte, UTF-8 included, is itself. An empty s is
/// BadEscape. O(len(s)); allocates the bytes in a.
Unescape(a: &Arena, s: str, line: int): (BadEscape | OutOfMemory)![]u8 from a {
    if s.Len() == 0 {
        fail BadEscape(line: line)
    }
    var out := List[u8].New(a)
    var i := 0
    for i < s.Len() {
        c := s.Byte(i)
        i += 1
        if c != b'\\' {
            try out.Push(c)
            continue
        }
        if i >= s.Len() {
            fail BadEscape(line: line)
        }
        e := s.Byte(i)
        i += 1
        v := match e {
            b'e' { 0x1b }
            b'r' { b'\r' }
            b'n' { b'\n' }
            b't' { b'\t' }
            b'\\' { b'\\' }
            b'x' {
                if i + 2 > s.Len() {
                    fail BadEscape(line: line)
                }
                hi := HexDigit(s.Byte(i)) else {
                    fail BadEscape(line: line)
                }
                lo := HexDigit(s.Byte(i + 1)) else {
                    fail BadEscape(line: line)
                }
                i += 2
                hi * 16 + lo
            }
            _ { fail BadEscape(line: line) }
        }
        try out.Push(v)
    }
    return out.Done()[..]
}

/// text inside the bracketed-paste markers ESC [200~ and ESC [201~.
Paste(a: &Arena, text: []u8): OutOfMemory![]u8 from a {
    var out := List[u8].New(a)
    try PushStr(mut out, "\x1b[200~")
    for c in text {
        try out.Push(c)
    }
    try PushStr(mut out, "\x1b[201~")
    return out.Done()[..]
}

PushStr(out: mut List[u8], s: str): OutOfMemory!void {
    for c in s.AsBytes() {
        try out.Push(c)
    }
}

/// The bytes of the named keys in s, separated by spaces.
Keys(a: &Arena, s: str, line: int): (UnknownKey | BadStep | OutOfMemory)![]u8 from a {
    var out := List[u8].New(a)
    var n := 0
    for name in s.Words() {
        n += 1
        if name.StartsWith("ctrl-") && name.Len() == 6 {
            c := name.Byte(5)
            if c < b'a' || c > b'z' {
                fail UnknownKey(line: line)
            }
            try out.Push(c - b'a' + 1)
            continue
        }
        k := KeyBytes(name) else {
            fail UnknownKey(line: line)
        }
        try PushStr(mut out, k)
    }
    if n == 0 {
        fail BadStep(line: line)
    }
    return out.Done()[..]
}

/// What xterm sends for a named key, in its normal cursor-key mode.
KeyBytes(name: str): ?str {
    return match name {
        "enter" { some("\r") }
        "tab" { some("\t") }
        "space" { some(" ") }
        "esc" { some("\x1b") }
        "backspace" { some("\x7f") }
        "up" { some("\x1b[A") }
        "down" { some("\x1b[B") }
        "right" { some("\x1b[C") }
        "left" { some("\x1b[D") }
        "home" { some("\x1b[H") }
        "end" { some("\x1b[F") }
        "insert" { some("\x1b[2~") }
        "delete" { some("\x1b[3~") }
        "pgup" { some("\x1b[5~") }
        "pgdn" { some("\x1b[6~") }
        "f1" { some("\x1bOP") }
        "f2" { some("\x1bOQ") }
        "f3" { some("\x1bOR") }
        "f4" { some("\x1bOS") }
        "f5" { some("\x1b[15~") }
        "f6" { some("\x1b[17~") }
        "f7" { some("\x1b[18~") }
        "f8" { some("\x1b[19~") }
        "f9" { some("\x1b[20~") }
        "f10" { some("\x1b[21~") }
        "f11" { some("\x1b[23~") }
        "f12" { some("\x1b[24~") }
        _ { none }
    }
}
