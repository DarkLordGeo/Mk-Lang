// Why: caseless comparison needs case folding.
// What: Fold and FoldOptions.
// How: folds each line with std.text's mapping.

import std.cli
import std.text
import src.input

/// `textkit fold`: folds the case of each line, for caseless comparison.
struct FoldOptions {
    /// Map each letter to lower case, one scalar to one, instead of full folding
    export lower: bool
    /// Map each letter to upper case, one scalar to one
    export upper: bool
    /// Read and write lines of code points in hex
    export hex: bool
    /// The read buffer, in bytes (default 16384)
    export buffer: ?int
    /// Files to read; none, or -, is standard input
    export files: []str
}

gen cli.Args(FoldOptions)

/// How `fold` maps case.
enum Mapping {
    Fold
    Lower
    Upper
}

struct FoldState {
    mapping: Mapping
    hex: bool
    size: int
    bad: int
}

/// Runs `textkit fold` with `args`. Returns the exit status.
export Fold(c: &input.Ctx, s: mut input.Streams, stdin: mut Reader, args: []str): int {
    synopsis := "fold [--lower | --upper] [--hex] [--buffer n] [file...]"
    if input.WantsHelp(args) {
        return input.Help(s, synopsis, FoldOptions.Usage)
    }
    opts := FoldOptions.Parse(c.arena, args) else e {
        return input.BadArgs(s, "fold", args, &e, FoldOptions.Usage)
    }
    if opts.lower && opts.upper {
        Print(mut s.err, "textkit fold: --lower and --upper exclude each other\n") else {}
        return input.Usage
    }
    size := input.Size(s, "fold", "--buffer", opts.buffer, input.BufferSize, 1) else {
        return input.Usage
    }
    var st := FoldState(
        mapping: if opts.lower { Mapping.Lower } else if opts.upper { Mapping.Upper } else { Mapping.Fold },
        hex: opts.hex,
        size: size,
        bad: 0,
    )
    status := input.ForEach(c, s, stdin, opts.files, st.size, mut st, fn(ctx: &input.Ctx, state: mut FoldState, streams: mut input.Streams, reader: mut Reader, name_of: str): input.Error!void { try FoldInput(ctx, state, streams, reader, name_of) })
    return if st.bad > 0 { input.Worse(status, input.Failed) } else { status }
}

FoldInput(c: &input.Ctx, st: mut FoldState, s: mut input.Streams, r: mut Reader, name: str): input.Error!void {
    var scratch := try Arena.New(c.heap)
    defer scratch.Free()
    var lines := try input.LineSource.New(c.arena, r, st.size)
    for {
        line := try lines.Next(&scratch) else { break }
        decoded := Decode(&scratch, line, st.hex) else e {
            input.ReportAt(s, name, lines.LineNumber(), &e)
            st.bad += 1
            scratch.Reset()
            continue
        }
        mapped := match st.mapping {
            .Fold { try text.FoldCase(&scratch, decoded) }
            .Lower { try decoded.ToLower(&scratch) }
            .Upper { try decoded.ToUpper(&scratch) }
        }
        try Encode(mut s.out, mapped, st.hex)
        try s.out.Str("\n")
        scratch.Reset()
    }
}
