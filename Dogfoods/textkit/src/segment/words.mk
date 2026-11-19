// Why: `textkit words` splits lines into words.
// What: Words and WordsOptions.
// How: UAX 29's word boundaries.

import std.cli
import src.input
import src.normal

/// `textkit words`: splits lines into words at UAX 29's word boundaries.
struct WordsOptions {
    /// Print each line's boundaries in the UCD's notation: ÷ at a boundary, × between scalars of one segment
    export marks: bool
    /// Print only the number of words, all inputs together
    export count: bool
    /// Read lines of code points in hex, and write words that way
    export hex: bool
    /// The read buffer, in bytes (default 16384)
    export buffer: ?int
    /// Files to read; none, or -, is standard input
    export files: []str
}

gen cli.Args(WordsOptions)

struct WordsState {
    count: bool
    hex: bool
    size: int
    words: int
    bad: int
}

/// Runs `textkit words` with `args`. Returns the exit status.
export Words(c: &input.Ctx, s: mut input.Streams, stdin: mut Reader, args: []str): int {
    synopsis := "words [--marks | --count] [--hex] [--buffer n] [file...]"
    if input.WantsHelp(args) {
        return input.Help(s, synopsis, WordsOptions.Usage)
    }
    opts := WordsOptions.Parse(c.arena, args) else e {
        return input.BadArgs(s, "words", args, &e, WordsOptions.Usage)
    }
    if opts.marks && opts.count {
        Print(mut s.err, "textkit words: --marks and --count do not go together\n") else {}
        return input.Usage
    }
    size := input.Size(s, "words", "--buffer", opts.buffer, input.BufferSize, 1) else {
        return input.Usage
    }
    if opts.marks {
        var marks := MarksState(unit: Unit.Word, hex: opts.hex, size: size, bad: 0)
        status := input.ForEach(c, s, stdin, opts.files, size, mut marks, fn(ctx: &input.Ctx, state: mut MarksState, streams: mut input.Streams, reader: mut Reader, name_of: str): input.Error!void { try MarksInput(ctx, state, streams, reader, name_of) })
        return if marks.bad > 0 { input.Worse(status, input.Failed) } else { status }
    }
    var st := WordsState(count: opts.count, hex: opts.hex, size: size, words: 0, bad: 0)
    var status := input.ForEach(c, s, stdin, opts.files, size, mut st, fn(ctx: &input.Ctx, state: mut WordsState, streams: mut input.Streams, reader: mut Reader, name_of: str): input.Error!void { try WordsInput(ctx, state, streams, reader, name_of) })
    if st.count {
        Print(mut s.out, "{n}\n", (n: st.words)) else {
            return input.Failed
        }
    }
    if st.bad > 0 {
        status = input.Worse(status, input.Failed)
    }
    return status
}

/// Writes each word of each line of `r` on a line of its own, or counts
/// them. A word is a segment between two word boundaries that is not white
/// space alone, so punctuation marks are words too: std.text gives no
/// letter or digit property to tell them apart.
WordsInput(c: &input.Ctx, st: mut WordsState, s: mut input.Streams, r: mut Reader, name: str): input.Error!void {
    var scratch := try Arena.New(c.heap)
    defer scratch.Free()
    var lines := try input.LineSource.New(c.arena, r, st.size)
    for {
        line := try lines.Next(&scratch) else { break }
        decoded := normal.Decode(&scratch, line, st.hex) else e {
            input.ReportAt(s, name, lines.LineNumber(), &e)
            st.bad += 1
            scratch.Reset()
            continue
        }
        var start := 0
        for end in BoundsOf(decoded, Unit.Word) {
            segment := decoded[start..end]
            start = end
            if segment.Trim().Len() == 0 {
                continue
            }
            st.words += 1
            if !st.count {
                try WriteSegment(mut s.out, segment, st.hex)
            }
        }
        scratch.Reset()
    }
}
