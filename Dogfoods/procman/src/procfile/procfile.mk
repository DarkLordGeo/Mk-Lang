// Why: the Procfile is procman's input: one entry a line, in the manner of
// foreman's, with options in brackets and one-off tasks.
//
// What: Parse, Entry, Kind, Restart and the parse errors.
//
// How: line by line, each error with its line; commands are split by
// words.mk. The grammar is in the program's README.

/// What procman does when a process ends.
export enum Restart {
    Never
    OnFailure
    Always
}

/// A process runs under `procman start`; a task runs once under `procman run`.
export enum Kind {
    Process
    Task
}

/// One line of a Procfile, parsed and expanded.
export struct Entry {
    export name: str
    export kind: Kind
    /// The program and its arguments, after quoting and `$` expansion
    export argv: []str
    export restart: Restart
    /// How many restarts before procman gives up
    export retries: int
    /// The first back-off; each restart doubles it
    export backoff_ms: i64
    /// The most output, stdout and stderr together, a run may write
    export limit: int
    /// When this process ends for good, procman stops every other one
    export main: bool
    /// The line of the Procfile, for messages
    export line: int
}

/// The line is not `[task] name [options]: command`; `col` is where it went wrong.
export error Syntax(line: int, col: int)
/// The quote at `col` is never closed.
export error UnclosedQuote(line: int, col: int)
/// An option procman does not know.
export error UnknownOption(line: int, col: int)
/// An option value out of its range or form.
export error BadValue(line: int, col: int)
/// A name declared again; `first` is its first line.
export error Duplicate(line: int, first: int)
/// A `$NAME` that the lookup does not know.
export error UnknownVariable(line: int, col: int)
/// The file declares no process or task.
export error NoEntries
/// Everything `Parse` can fail with.
export type Error = Syntax | UnclosedQuote | UnknownOption | BadValue | Duplicate | UnknownVariable | NoEntries | OutOfMemory

/// The options an entry gets when its line names none.
export const DefaultRetries: int = 3
export const DefaultBackoffMs: i64 = 100
export const DefaultLimit: int = 1_048_576

const NoWords: []str = []

/// Parses a whole Procfile. `lookup` answers `$NAME` for the names the text
/// uses, pushing the value onto its `Text` and returning false for a name it
/// does not know; `PROCMAN` is the caller's to answer, with procman's own path.
/// Fails at the first bad line with its line and column, `Duplicate` naming the
/// first line of the name, and `NoEntries` when no line declares anything.
/// O(n) in the text; allocates the entries and every expanded word in `a`.
export Parse(a: &Arena, text: str into a, const lookup: fn(str, mut Text): OutOfMemory!bool): Error!List[Entry] from a, text {
    var out := List[Entry].New(a)
    var n := 0
    for line in text.Lines() {
        n += 1
        body := StripComment(line).Trim()
        if body.Len() == 0 {
            continue
        }
        e := try ParseLine(a, body, Indent(line), n, lookup)
        for prev in &out {
            if prev.name == e.name {
                fail Duplicate(line: n, first: prev.line)
            }
        }
        try out.Push(e)
    }
    if out.Len() == 0 {
        fail NoEntries
    }
    return out
}

/// Finds the entry called `name`, of either kind. O(n).
export Find(entries: []Entry, name: str): ?Entry from entries {
    for e in entries {
        if e.name == name {
            return some(e)
        }
    }
    return none
}

// A `#` starts a comment only at the start of a word, so `a#b` stays a word.
StripComment(line: str): str {
    var open_quote: u8 = 0
    for i in 0..line.Len() {
        c := line.Byte(i)
        if open_quote != 0 {
            if c == open_quote {
                open_quote = 0
            }
            continue
        }
        if c == b'\'' || c == b'"' {
            open_quote = c
            continue
        }
        if c == b'#' && (i == 0 || IsSpace(line.Byte(i - 1))) {
            return line[..i]
        }
    }
    return line
}

// The column of the first byte that is not a space, so columns count from the
// line as written, before Trim.
Indent(line: str): int {
    var i := 0
    for i < line.Len() && IsSpace(line.Byte(i)) {
        i += 1
    }
    return i
}

IsSpace(c: u8): bool {
    return c == b' ' || c == b'\t'
}

IsNameByte(c: u8): bool {
    return (c >= b'a' && c <= b'z') || (c >= b'A' && c <= b'Z') || (c >= b'0' && c <= b'9') || c == b'_' || c == b'-' || c == b'.'
}

// One non-blank line, without its comment: `[task] name [options]: command`.
// `base` is the column of the body's first byte in the line.
ParseLine(a: &Arena, body: str, base: int, n: int, const lookup: fn(str, mut Text): OutOfMemory!bool): Error!Entry from a, body {
    var pos := 0
    var kind := Kind.Process
    if body.StartsWith("task ") || body.StartsWith("task\t") {
        kind = Kind.Task
        pos = SkipSpace(body, 4)
    }
    start := pos
    for pos < body.Len() && IsNameByte(body.Byte(pos)) {
        pos += 1
    }
    if pos == start {
        fail Syntax(line: n, col: base + pos + 1)
    }
    name := body[start..pos]
    var e := Entry(name: name, kind: kind, argv: NoWords, restart: Restart.Never, retries: DefaultRetries, backoff_ms: DefaultBackoffMs, limit: DefaultLimit, main: false, line: n)
    pos = SkipSpace(body, pos)
    if pos < body.Len() && body.Byte(pos) == b'[' {
        close := body[pos..].Find("]") else {
            fail Syntax(line: n, col: base + body.Len() + 1)
        }
        try ParseOptions(mut e, body[pos + 1..pos + close], base + pos + 1, n)
        pos = SkipSpace(body, pos + close + 1)
    }
    if pos >= body.Len() || body.Byte(pos) != b':' {
        fail Syntax(line: n, col: base + pos + 1)
    }
    pos = SkipSpace(body, pos + 1)
    if pos == body.Len() {
        fail Syntax(line: n, col: base + pos + 1)
    }
    e.argv = try Words(a, body[pos..], base + pos, n, lookup)
    return e
}

SkipSpace(s: str, from: int): int {
    var i := from
    for i < s.Len() && IsSpace(s.Byte(i)) {
        i += 1
    }
    return i
}

// `opts` is the text between the brackets, which starts at column `col0`.
ParseOptions(e: mut Entry, opts: str, col0: int, n: int): Error!void {
    var pos := 0
    for {
        pos = SkipSpace(opts, pos)
        if pos == opts.Len() {
            return
        }
        start := pos
        for pos < opts.Len() && !IsSpace(opts.Byte(pos)) {
            pos += 1
        }
        word := opts[start..pos]
        col := col0 + start + 1
        eq := word.Find("=") else {
            if word != "main" {
                fail UnknownOption(line: n, col: col)
            }
            e.main = true
            continue
        }
        key := word[..eq]
        value := word[eq + 1..]
        vcol := col + eq + 1
        if key == "restart" {
            e.restart = match value {
                "never" { Restart.Never }
                "on-failure" { Restart.OnFailure }
                "always" { Restart.Always }
                _ { fail BadValue(line: n, col: vcol) }
            }
        } else if key == "retries" {
            e.retries = ParseCount(value) else {
                fail BadValue(line: n, col: vcol)
            }
        } else if key == "backoff" {
            e.backoff_ms = ParseMillis(value) else {
                fail BadValue(line: n, col: vcol)
            }
        } else if key == "limit" {
            e.limit = ParseSize(value) else {
                fail BadValue(line: n, col: vcol)
            }
        } else {
            fail UnknownOption(line: n, col: col)
        }
    }
}

/// A count from 0 to 1,000,000, digits only.
export ParseCount(s: str): ?int {
    if s.Len() == 0 || s.Len() > 7 {
        return none
    }
    v := s.ParseInt() else {
        return none
    }
    if v < 0 || v > 1_000_000 || s.Byte(0) == b'+' {
        return none
    }
    return some(Trunc[int](v))
}

/// A duration: digits then `ms`, `s` or `m`, from 0 to one hour.
export ParseMillis(s: str): ?i64 {
    var unit: i64 = 1
    var digits := s
    if s.EndsWith("ms") {
        digits = s[..s.Len() - 2]
    } else if s.EndsWith("s") {
        unit = 1000
        digits = s[..s.Len() - 1]
    } else if s.EndsWith("m") {
        unit = 60_000
        digits = s[..s.Len() - 1]
    }
    v := ParseCount(digits) else {
        return none
    }
    total := i64(v) * unit
    if total > 3_600_000 {
        return none
    }
    return some(total)
}

/// A byte count: digits, then optionally `k` or `m` for 1,024 or 1,048,576,
/// from 0 to 1 GiB.
export ParseSize(s: str): ?int {
    var unit := 1
    var digits := s
    if s.EndsWith("k") {
        unit = 1024
        digits = s[..s.Len() - 1]
    } else if s.EndsWith("m") {
        unit = 1_048_576
        digits = s[..s.Len() - 1]
    }
    v := ParseCount(digits) else {
        return none
    }
    if v > (1 << 30) / unit {
        return none
    }
    return some(v * unit)
}
