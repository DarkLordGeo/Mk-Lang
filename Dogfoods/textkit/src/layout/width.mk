// Why: `textkit width` measures lines in terminal columns.
// What: Width and WidthOptions.
// How: sums each grapheme cluster's terminal width.

import std.cli
import std.text
import src.input
import src.normal
import src.scalar

/// `textkit width`: measures lines in terminal columns.
struct WidthOptions {
    /// Print each line's grapheme clusters in the UCD's notation: ÷ between clusters, × inside one
    export clusters: bool
    /// Print only the widest line's width
    export max: bool
    /// Read lines of code points in hex
    export hex: bool
    /// The read buffer, in bytes (default 16384)
    export buffer: ?int
    /// Files to read; none, or -, is standard input
    export files: []str
}

gen cli.Args(WidthOptions)

struct WidthState {
    clusters: bool
    max: bool
    hex: bool
    size: int
    widest: int
    bad: int
}

/// Runs `textkit width` with `args`. Returns the exit status.
export Width(c: &input.Ctx, s: mut input.Streams, stdin: mut Reader, args: []str): int {
    synopsis := "width [--clusters | --max] [--hex] [--buffer n] [file...]"
    if input.WantsHelp(args) {
        return input.Help(s, synopsis, WidthOptions.Usage)
    }
    opts := WidthOptions.Parse(c.arena, args) else e {
        return input.BadArgs(s, "width", args, &e, WidthOptions.Usage)
    }
    size := input.Size(s, "width", "--buffer", opts.buffer, input.BufferSize, 1) else {
        return input.Usage
    }
    var st := WidthState(
        clusters: opts.clusters,
        max: opts.max,
        hex: opts.hex,
        size: size,
        widest: 0,
        bad: 0,
    )
    var status := input.ForEach(c, s, stdin, opts.files, st.size, mut st, fn(ctx: &input.Ctx, state: mut WidthState, streams: mut input.Streams, reader: mut Reader, name_of: str): input.Error!void { try WidthInput(ctx, state, streams, reader, name_of) })
    if st.max {
        Print(mut s.out, "{w}\n", (w: st.widest)) else {
            return input.Failed
        }
    }
    if st.bad > 0 {
        status = input.Worse(status, input.Failed)
    }
    return status
}

WidthInput(c: &input.Ctx, st: mut WidthState, s: mut input.Streams, r: mut Reader, name: str): input.Error!void {
    var scratch := try Arena.New(c.heap)
    defer scratch.Free()
    var notation := Text.New(c.arena)
    var lines := try input.LineSource.New(c.arena, r, st.size)
    for {
        line := try lines.Next(&scratch) else { break }
        decoded := normal.Decode(&scratch, line, st.hex) else e {
            input.ReportAt(s, name, lines.LineNumber(), &e)
            st.bad += 1
            scratch.Reset()
            continue
        }
        w := text.Width(decoded)
        if w > st.widest {
            st.widest = w
        }
        if st.clusters {
            notation.Clear()
            var nw := notation.Writer()
            try Breaks(mut nw, decoded)
            try s.out.Str(notation.Str())
            try s.out.Str("\n")
        } else if !st.max {
            try Print(mut s.out, "{w:>4} {line}\n", (w, line))
        }
        scratch.Reset()
    }
}

/// Writes the grapheme cluster breaks of `s` as UAX 29's test files do:
/// `÷ 0020 × 0308 ÷`, with `÷` at every break and `×` between the scalars
/// of one cluster. An empty `s` writes nothing.
export Breaks(w: mut Writer, s: str): WriteError!void {
    for cluster in text.Graphemes(s) {
        try w.Str("÷ ")
        try scalar.Write(w, cluster, " × ")
        try w.Str(" ")
    }
    if s.Len() > 0 {
        try w.Str("÷")
    }
}
