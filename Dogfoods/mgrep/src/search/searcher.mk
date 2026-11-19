// Why: one input's search and its report, in every output mode grep has.
// What: Config, Summary, Search and CountLines.
// How: lines that cannot match are skipped with vector byte searches, their
// newlines counted with simd.Count; context lines are kept in a queue, and
// colour is ANSI escapes.

import std.coll
import std.regex
import std.simd
import std.text

/// How a search reports what it finds.
export struct Config {
    /// Report lines that do not match
    export invert: bool
    /// Report the number of matching lines per input
    export count: bool
    /// Report the number of matches per input
    export count_matches: bool
    /// Report only the names of inputs that match
    export files_only: bool
    /// Report each match on its own line
    export only_matching: bool
    /// Prefix each line with its number
    export line_number: bool
    /// Prefix each match line with the display column of its first match
    export column: bool
    /// Print the name once above an input's lines
    export heading: bool
    /// Prefix each line with the input's name
    export with_filename: bool
    /// Colour names, numbers and matches with ANSI escapes
    export color: bool
    /// Lines of context before a match
    export before: int
    /// Lines of context after a match
    export after: int
    /// Stop an input after this many matching lines; 0 is no limit
    export max_count: int
    /// Print each match line with its matches replaced by this text
    export replace: ?str
    /// With only_matching, print this capture group of each match
    export group: ?int
    /// Print nothing and stop at the first match
    export quiet: bool
}

/// What searching one input found.
export struct Summary {
    export matched_lines: int
    export matches: int
    export lines: int
    export invalid_lines: int
}

// A line kept for the context before a match
struct Held {
    num: int
    text: str
}

// The state of one search over one input
struct Scan {
    printed: int
    after_left: int
    any: bool
    stop: bool
    sum: Summary
}

const Reset: str = "\x1b[0m"
const NameColor: str = "\x1b[35m"
const NumberColor: str = "\x1b[32m"
const MatchColor: str = "\x1b[1;31m"

/// Searches `buf`, the whole content of the input called `name`, and writes the
/// report `cfg` asks for to `w`. A buffer that is not valid UTF-8 is searched
/// line by line, and its invalid lines are skipped and counted. Without context
/// or inversion, lines that cannot match are skipped with vector byte searches,
/// their newlines counted with simd.Count. O(buffer length) for literal patterns,
/// O(buffer length times pattern states) for a regex; allocates the context
/// queue and match lists in `a`.
export Search(a: &Arena, m: &Matcher, cfg: &Config, name: str, buf: []u8 into a, w: mut Writer): WriteError|OutOfMemory!Summary {
    var scan := Scan(printed: 0, after_left: 0, any: false, stop: false, sum: Summary(matched_lines: 0, matches: 0, lines: 0, invalid_lines: 0))
    scan.sum.lines = CountLines(buf)
    if cfg.count_matches && !cfg.invert {
        if b := m.SingleByte() {
            scan.sum.matches = simd.Count(buf, b)
            scan.sum.matched_lines = scan.sum.matches
            try Finish(cfg, name, &scan.sum, w)
            return scan.sum
        }
    }
    var held := coll.Deque[Held].New(a)
    whole := str.From(buf) else { "" }
    valid := whole.Len() == buf.Len()
    skip := m.mode != Mode.Pattern && !cfg.invert && cfg.before == 0
    var pos := 0
    var num := 1
    for pos < buf.Len() && !scan.stop {
        if skip && scan.after_left == 0 {
            c := m.Candidate(buf, pos) else {
                break
            }
            start := LineStart(buf, pos, c)
            num += simd.Count(buf[pos..start], b'\n')
            pos = start
        }
        i := simd.IndexByte(buf[pos..], b'\n') else { buf.Len() - pos }
        var end := pos + i
        if end > pos && buf[end - 1] == b'\r' {
            end -= 1
        }
        if valid {
            try scan.Take(a, m, cfg, name, mut held, num, whole[pos..end], w)
        } else if simd.ValidUtf8(buf[pos..end]) {
            line := str.From(buf[pos..end]) else { "" }
            try scan.Take(a, m, cfg, name, mut held, num, line, w)
        } else {
            scan.sum.invalid_lines += 1
        }
        pos += i + 1
        num += 1
    }
    try Finish(cfg, name, &scan.sum, w)
    return scan.sum
}

/// The number of lines in `buf`, a last line without a newline included.
export CountLines(buf: []u8): int {
    n := simd.Count(buf, b'\n')
    if buf.Len() > 0 && buf[buf.Len() - 1] != b'\n' {
        return n + 1
    }
    return n
}

// The start of the line that holds byte `c`, no earlier than `lo`.
LineStart(buf: []u8, lo: int, c: int): int {
    var i := c
    for i > lo && buf[i - 1] != b'\n' {
        i -= 1
    }
    return i
}

// Writes the per-input totals that the count and file-name modes print.
Finish(cfg: &Config, name: str, sum: &Summary, w: mut Writer): WriteError!void {
    if cfg.quiet {
        return
    }
    if cfg.files_only {
        if sum.matched_lines > 0 {
            try WriteName(cfg, name, w)
            try w.Str("\n")
        }
        return
    }
    if cfg.count || cfg.count_matches {
        n := if cfg.count_matches { sum.matches } else { sum.matched_lines }
        if cfg.with_filename {
            try WriteName(cfg, name, w)
            try w.Str(":")
        }
        try Print(w, "{n}\n", (n))
    }
}

WriteName(cfg: &Config, name: str, w: mut Writer): WriteError!void {
    if cfg.color {
        try Print(w, "{c}{name}{r}", (c: NameColor, name, r: Reset))
        return
    }
    try w.Str(name)
}

// Judges one line, and prints it or keeps it as context.
Scan.Take(mut self, a: &Arena, m: &Matcher, cfg: &Config, name: str, held: mut coll.Deque[Held], num: int, line: str into held, w: mut Writer): WriteError|OutOfMemory!void {
    hit := m.IsMatch(line) != cfg.invert
    if !hit {
        if self.after_left > 0 {
            self.after_left -= 1
            try self.Emit(a, m, cfg, name, num, line, false, w)
            return
        }
        if cfg.before > 0 {
            try held.Push(Held(num: num, text: line))
            if held.Len() > cfg.before {
                _ = held.PopFront()
            }
        }
        return
    }
    self.sum.matched_lines += 1
    if cfg.quiet || cfg.files_only {
        self.stop = true
        return
    }
    if cfg.count || cfg.count_matches {
        if !cfg.invert {
            matches := try m.All(a, line)
            self.sum.matches += matches.Len()
        }
        self.stop = cfg.max_count > 0 && self.sum.matched_lines >= cfg.max_count
        return
    }
    for i in 0..held.Len() {
        h := held.Get(i) else {
            break
        }
        if h.num > self.printed {
            try self.Emit(a, m, cfg, name, h.num, h.text, false, w)
        }
    }
    for held.Len() > 0 {
        _ = held.Pop()
    }
    try self.Emit(a, m, cfg, name, num, line, true, w)
    self.after_left = cfg.after
    self.stop = cfg.max_count > 0 && self.sum.matched_lines >= cfg.max_count
}

// Prints one line: a match line when `hit`, else a context line.
Scan.Emit(mut self, a: &Arena, m: &Matcher, cfg: &Config, name: str, num: int, line: str, hit: bool, w: mut Writer): WriteError|OutOfMemory!void {
    if cfg.heading && !self.any {
        try WriteName(cfg, name, w)
        try w.Str("\n")
    }
    context := cfg.before > 0 || cfg.after > 0
    if context && self.any && num > self.printed + 1 {
        try w.Str("--\n")
    }
    self.any = true
    self.printed = num
    sep := if hit { ":" } else { "-" }
    if !hit || cfg.invert {
        try Prefix(cfg, name, num, none, sep, w)
        try w.Str(line)
        try w.Str("\n")
        return
    }
    matches := try m.All(a, line)
    self.sum.matches += matches.Len()
    if cfg.only_matching {
        for x in matches {
            try Prefix(cfg, name, num, some(text.Width(line[..x.lo]) + 1), sep, w)
            part := try Part(m, cfg, line, x)
            try Colored(cfg, part, w)
            try w.Str("\n")
        }
        return
    }
    first := m.Find(line, 0)
    column := if f := first { some(text.Width(line[..f.lo]) + 1) } else { none }
    try Prefix(cfg, name, num, column, sep, w)
    if r := cfg.replace {
        try w.Str(try Replaced(a, m, line, matches[..], r))
        try w.Str("\n")
        return
    }
    var at := 0
    for x in matches {
        try w.Str(line[at..x.lo])
        try Colored(cfg, line[x.lo..x.hi], w)
        at = x.hi
    }
    try w.Str(line[at..])
    try w.Str("\n")
}

// The text that only-matching prints for one match: the match, or its group.
Part(m: &Matcher, cfg: &Config, line: str, x: regex.Match): OutOfMemory!str from line {
    g := cfg.group else {
        return line[x.lo..x.hi]
    }
    re := m.Regex() else {
        return line[x.lo..x.hi]
    }
    return re.Group(line, x, g) else { "" }
}

// The line with each match replaced; a regex expands `$1` and `$name`.
Replaced(a: &Arena, m: &Matcher, line: str, matches: []regex.Match, with: str): OutOfMemory!str from a, line {
    if re := m.Regex() {
        return try re.Replace(a, line, with)
    }
    var out := Text.New(a)
    var at := 0
    for x in matches {
        try out.Push(line[at..x.lo])
        try out.Push(with)
        at = x.hi
    }
    try out.Push(line[at..])
    return out.Done()
}

Colored(cfg: &Config, s: str, w: mut Writer): WriteError!void {
    if cfg.color && s.Len() > 0 {
        try Print(w, "{c}{s}{r}", (c: MatchColor, s, r: Reset))
        return
    }
    try w.Str(s)
}

// Writes the name, line number and column that start an output line.
Prefix(cfg: &Config, name: str, num: int, column: ?int, sep: str, w: mut Writer): WriteError!void {
    if cfg.with_filename && !cfg.heading {
        try WriteName(cfg, name, w)
        try w.Str(sep)
    }
    if cfg.line_number {
        if cfg.color {
            try Print(w, "{c}{num}{r}{sep}", (c: NumberColor, num, r: Reset, sep))
        } else {
            try Print(w, "{num}{sep}", (num, sep))
        }
    }
    if cfg.column {
        if col := column {
            try Print(w, "{col}{sep}", (col, sep))
        }
    }
}
