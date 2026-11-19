// Why: `textkit wc` counts what Unicode text is made of.
// What: Wc, Counts, Columns and WcOptions.
// How: lines, words, scalars, bytes and grapheme clusters in one pass.

import std.cli
import std.text
import src.input

/// `textkit wc`: counts lines, words, scalars, bytes and grapheme clusters.
struct WcOptions {
    /// Count lines: the newlines
    export lines: bool
    /// Count words: runs of characters between whitespace
    export words: bool
    /// Count characters: Unicode scalar values
    export chars: bool
    /// Count bytes
    export bytes: bool
    /// Count grapheme clusters: what a reader sees as one character
    export clusters: bool
    /// Print the widest line's width in terminal columns
    export width: bool
    /// The read buffer, in bytes (default 16384)
    export buffer: ?int
    /// Files to read; none, or -, is standard input
    export files: []str
}

gen cli.Args(WcOptions)

/// The counts of one input, or of all of them.
struct Counts {
    export lines: int
    export words: int
    export chars: int
    export bytes: int
    export clusters: int
    export width: int
}

/// Which counts to print, in this order.
struct Columns {
    lines: bool
    words: bool
    chars: bool
    bytes: bool
    clusters: bool
    width: bool
}

struct WcState {
    show: Columns
    size: int
    total: Counts
    inputs: int
}

/// Runs `textkit wc` with `args`. Returns the exit status.
export Wc(c: &input.Ctx, s: mut input.Streams, stdin: mut Reader, args: []str): int {
    synopsis := "wc [--lines] [--words] [--chars] [--bytes] [--clusters] [--width] [--buffer n] [file...]"
    if input.WantsHelp(args) {
        return input.Help(s, synopsis, WcOptions.Usage)
    }
    opts := WcOptions.Parse(c.arena, args) else e {
        return input.BadArgs(s, "wc", args, &e, WcOptions.Usage)
    }
    any := opts.lines || opts.words || opts.chars || opts.bytes || opts.clusters || opts.width
    show := if any {
        Columns(lines: opts.lines, words: opts.words, chars: opts.chars, bytes: opts.bytes, clusters: opts.clusters, width: opts.width)
    } else {
        Columns(lines: true, words: true, chars: true, bytes: true, clusters: false, width: false)
    }
    size := input.Size(s, "wc", "--buffer", opts.buffer, input.BufferSize, 1) else {
        return input.Usage
    }
    var st := WcState(show: show, size: size, total: Zero(), inputs: 0)
    status := input.ForEach(c, s, stdin, opts.files, st.size, mut st, fn(ctx: &input.Ctx, state: mut WcState, streams: mut input.Streams, reader: mut Reader, name_of: str): input.Error!void { try CountInput(ctx, state, streams, reader, name_of) })
    if st.inputs > 1 {
        Row(mut s.out, &st.show, &st.total, "total") else {
            return input.Failed
        }
    }
    return status
}

Zero(): Counts {
    return Counts(lines: 0, words: 0, chars: 0, bytes: 0, clusters: 0, width: 0)
}

/// Counts one input line by line, so memory stays at the longest line.
CountInput(c: &input.Ctx, st: mut WcState, s: mut input.Streams, r: mut Reader, name: str): input.Error!void {
    var scratch := try Arena.New(c.heap)
    defer scratch.Free()
    var lines := try input.LineSource.New(c.arena, r, st.size)
    var n := Zero()
    for {
        line := try lines.Next(&scratch) else { break }
        for _ in line.Words() {
            n.words += 1
        }
        for _ in line.Scalars() {
            n.chars += 1
        }
        for _ in text.Graphemes(line) {
            n.clusters += 1
        }
        w := text.Width(line)
        if w > n.width {
            n.width = w
        }
        scratch.Reset()
    }
    (bytes, newlines, returns) := lines.Counts()
    n.lines = newlines
    n.bytes = bytes
    // each `\n` and each `\r` before it is a scalar, and `\r\n` is one cluster
    n.chars += newlines + returns
    n.clusters += newlines
    try Row(mut s.out, &st.show, &n, if name == "-" && st.inputs == 0 { "" } else { name })
    st.total = Add(&st.total, &n)
    st.inputs += 1
}

Add(a: &Counts, b: &Counts): Counts {
    return Counts(
        lines: a.lines + b.lines,
        words: a.words + b.words,
        chars: a.chars + b.chars,
        bytes: a.bytes + b.bytes,
        clusters: a.clusters + b.clusters,
        width: if a.width > b.width { a.width } else { b.width },
    )
}

/// Writes the counts `show` asks for, each right-aligned in 8 columns, then
/// `name` unless it is empty.
Row(w: mut Writer, show: &Columns, n: &Counts, name: str): WriteError!void {
    if show.lines {
        try Print(w, "{n.lines:>8}", (n))
    }
    if show.words {
        try Print(w, "{n.words:>8}", (n))
    }
    if show.chars {
        try Print(w, "{n.chars:>8}", (n))
    }
    if show.bytes {
        try Print(w, "{n.bytes:>8}", (n))
    }
    if show.clusters {
        try Print(w, "{n.clusters:>8}", (n))
    }
    if show.width {
        try Print(w, "{n.width:>8}", (n))
    }
    if name.Len() > 0 {
        try Print(w, " {name}", (name))
    }
    try w.Str("\n")
}
