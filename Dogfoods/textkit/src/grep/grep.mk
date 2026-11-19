// Why: `textkit grep` prints the lines that match a regular expression.
// What: Grep, GrepOptions, GrepState and Matcher.
// How: lines from Reader.Line, matched by std.regex or as a fixed text;
// prints lines, matches, a group or counts.

import std.cli
import std.regex
import std.text
import src.input

/// `textkit grep`: prints the lines that match a regular expression.
struct GrepOptions {
    /// Match without regard to case
    export ignore_case: bool
    /// Print the lines that do not match
    export invert: bool
    /// Print only how many lines matched, per input
    export count: bool
    /// Put the line number before each line
    export line_number: bool
    /// Print each match on a line of its own
    export only: bool
    /// Take the pattern as text, not as a regular expression
    export fixed: bool
    /// Print the text of this group of the first match, from 1
    export group: ?int
    /// Print each matching line with every match replaced by this
    export replace: ?str
    /// Write each line out as soon as it is found
    export flush: bool
    /// The read buffer, in bytes, which also bounds a line (default 16384)
    export buffer: ?int
    /// The pattern, then the files to read; no file, or -, is standard input
    export args: []str
}

gen cli.Args(GrepOptions)

/// How a line is matched: a compiled expression, or a fixed text.
enum Matcher {
    Pattern(re: &regex.Regex)
    Fixed(needle: str)
}

struct GrepState {
    matcher: Matcher
    fold: bool
    invert: bool
    count: bool
    numbers: bool
    only: bool
    group: int
    replace: ?str
    flush: bool
    size: int
    names: bool
    matched: int
}

/// Runs `textkit grep` with `args`. Returns the exit status: 0 when a line
/// was printed or counted, 1 when none matched, as grep does.
export Grep(c: &input.Ctx, s: mut input.Streams, stdin: mut Reader, args: []str into c): int {
    synopsis := "grep [--ignore-case] [--invert] [--count] [--line-number] [--only] [--fixed] [--group n] [--replace text] [--flush] pattern [file...]"
    if input.WantsHelp(args) {
        return input.Help(s, synopsis, GrepOptions.Usage)
    }
    opts := GrepOptions.Parse(c.arena, args) else e {
        return input.BadArgs(s, "grep", args, &e, GrepOptions.Usage)
    }
    if opts.args.Len() == 0 {
        Print(mut s.err, "textkit grep: no pattern\n") else {}
        GrepOptions.Usage(mut s.err) else {}
        return input.Usage
    }
    pattern := opts.args[0]
    files := opts.args[1..]
    group := opts.group else { 0 }
    if group < 0 || (opts.group.IsSome() && group == 0) {
        Print(mut s.err, "textkit grep: --group counts from 1, not {group}\n", (group)) else {}
        return input.Usage
    }
    if opts.fixed && (opts.group.IsSome() || opts.replace.IsSome()) {
        Print(mut s.err, "textkit grep: --fixed takes no --group or --replace\n") else {}
        return input.Usage
    }
    matcher := if opts.fixed {
        needle := if opts.ignore_case { text.FoldCase(c.arena, pattern) else { return input.NoMemory } } else { pattern }
        Matcher.Fixed(needle: needle)
    } else {
        source := if opts.ignore_case { c.arena.Fmt("(?i){pattern}", (pattern)) else { return input.NoMemory } } else { pattern }
        compiled := regex.Compile(c.arena, source) else e {
            BadPattern(mut s.err, pattern, source.Len() - pattern.Len(), &e)
            return match e {
                OutOfMemory { input.NoMemory }
                _ { input.Usage }
            }
        }
        // in the arena, not a local: the state that holds it is stored into
        // what the arena reaches
        re := c.arena.Put(compiled) else { return input.NoMemory }
        Matcher.Pattern(re: &*re)
    }
    size := input.Size(s, "grep", "--buffer", opts.buffer, input.BufferSize, 1) else {
        return input.Usage
    }
    var st := GrepState(
        matcher: matcher,
        fold: opts.ignore_case,
        invert: opts.invert,
        count: opts.count,
        numbers: opts.line_number,
        only: opts.only,
        group: group,
        replace: opts.replace,
        flush: opts.flush,
        size: size,
        names: files.Len() > 1,
        matched: 0,
    )
    status := input.ForEach(c, s, stdin, files, st.size, mut st, fn(ctx: &input.Ctx, state: mut GrepState, streams: mut input.Streams, reader: mut Reader, name_of: str): input.Error!void { try GrepInput(ctx, state, streams, reader, name_of) })
    if status != input.Ok {
        return status
    }
    return if st.matched > 0 { input.Ok } else { input.NoMatch }
}

/// Searches every line of `r`, read with `Reader.Line`, so a line is bounded
/// by the read buffer.
GrepInput(c: &input.Ctx, st: mut GrepState, s: mut input.Streams, r: mut Reader, name: str): input.Error!void {
    var scratch := try Arena.New(c.heap)
    defer scratch.Free()
    var n := 0
    var hits := 0
    for {
        // Line fails with one ReadError on a line longer than the buffer and on bytes that are not UTF-8
        got := r.Line(&scratch) else e {
            return match e {
                OutOfMemory { fail OutOfMemory }
                ReadError { fail input.Unreadable(line: n + 1, limit: if name == "-" { 0 } else { st.size }) }
            }
        }
        line := got else { break }
        n += 1
        printed := try st.Search(&scratch, mut s.out, line, name, n)
        if printed {
            hits += 1
        }
        scratch.Reset()
    }
    st.matched += hits
    if st.count {
        if st.names {
            try Print(mut s.out, "{name}:", (name))
        }
        try Print(mut s.out, "{hits}\n", (hits))
    }
}

/// Matches one line and prints what the options ask for. Returns whether
/// the line counts as a hit.
GrepState.Search(&self, a: &Arena, w: mut Writer, line: str, name: str, n: int): OutOfMemory|WriteError!bool {
    var hit := false
    match self.matcher {
        .Fixed(needle) {
            hay := if self.fold { try text.FoldCase(a, line) } else { line }
            hit = hay.Has(needle)
        }
        .Pattern(re) {
            hit = re.IsMatch(line)
        }
    }
    if hit == self.invert {
        return false
    }
    if self.count {
        return true
    }
    if self.only && !self.invert {
        try self.PrintMatches(a, w, line, name, n)
    } else if self.group > 0 && !self.invert {
        try self.PrintGroup(w, line, name, n)
    } else {
        try self.Prefix(w, name, n)
        if r := self.replace {
            try w.Str(try self.Replaced(a, line, r))
        } else {
            try w.Str(line)
        }
        try w.Str("\n")
    }
    if self.flush {
        try w.Flush()
    }
    return true
}

GrepState.Prefix(&self, w: mut Writer, name: str, n: int): WriteError!void {
    if self.names {
        try Print(w, "{name}:", (name))
    }
    if self.numbers {
        try Print(w, "{n}:", (n))
    }
}

/// Each match of the line on a line of its own. A fixed text is found by
/// `str.Find`, past each match in turn.
GrepState.PrintMatches(&self, a: &Arena, w: mut Writer, line: str, name: str, n: int): OutOfMemory|WriteError!void {
    match self.matcher {
        .Pattern(re) {
            for m in try re.FindAll(a, line) {
                if m.hi > m.lo {
                    try self.Prefix(w, name, n)
                    try w.Str(Matched(line, m))
                    try w.Str("\n")
                }
            }
        }
        .Fixed(needle) {
            hay := if self.fold { try text.FoldCase(a, line) } else { line }
            var from := 0
            for needle.Len() > 0 {
                at := hay[from..].Find(needle) else { break }
                try self.Prefix(w, name, n)
                // a folded line can differ in length from the line, so print what was found
                try w.Str(hay[from + at..from + at + needle.Len()])
                try w.Str("\n")
                from += at + needle.Len()
            }
        }
    }
}

/// The text of group `self.group` of the line's first match, when it took part.
GrepState.PrintGroup(&self, w: mut Writer, line: str, name: str, n: int): WriteError!void {
    match self.matcher {
        .Pattern(re) {
            m := re.Find(line) else { return }
            g := re.Group(line, m, self.group) else { return }
            try self.Prefix(w, name, n)
            try w.Str(g)
            try w.Str("\n")
        }
        .Fixed {}
    }
}

GrepState.Replaced(&self, a: &Arena, line: str, with: str): OutOfMemory!str from a, line {
    return match self.matcher {
        .Pattern(re) { try re.Replace(a, line, with) }
        .Fixed { line }
    }
}

/// The text of `line` that `m` spans.
Matched(line: str, m: regex.Match): str from line {
    return line[m.lo..m.hi]
}

/// Reports a pattern `regex.Compile` refused. A syntax error shows the
/// pattern with a caret under the offending place, measured in terminal
/// columns; `shift` is the length of the prefix that preceded `pattern` in
/// what was compiled.
BadPattern(w: mut Writer, pattern: str, shift: int, e: &regex.Error) {
    match *e {
        regex.Syntax(offset) {
            var at := offset - shift
            if at < 0 {
                at = 0
            }
            if at > pattern.Len() {
                at = pattern.Len()
            }
            Print(w, "textkit grep: bad pattern at byte {at}:\n    {pattern}\n    ", (at, pattern)) else {}
            for _ in 0..text.Width(pattern[..at]) {
                w.Str(" ") else {}
            }
            w.Str("^\n") else {}
        }
        regex.TooLarge {
            Print(w, "textkit grep: pattern {pattern} is too large to compile\n", (pattern)) else {}
        }
        OutOfMemory {
            w.Str("textkit grep: out of memory compiling the pattern\n") else {}
        }
        _ {
            Panic("regex.Error gained a member this match does not handle")
        }
    }
}
