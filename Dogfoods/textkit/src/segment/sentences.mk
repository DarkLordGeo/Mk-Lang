// Why: `textkit sentences` splits paragraphs into sentences.
// What: Sentences and SentencesOptions.
// How: UAX 29's sentence boundaries over each paragraph.

import std.cli
import std.encoding
import src.input

/// `textkit sentences`: splits paragraphs into sentences at UAX 29's
/// sentence boundaries.
struct SentencesOptions {
    /// Print each line's boundaries in the UCD's notation instead, one line at a time
    export marks: bool
    /// With --marks, read lines of code points in hex
    export hex: bool
    /// The encoding of the input, by a label of the Encoding Standard (default utf-8)
    export from: ?str
    /// The most bytes to read, all inputs together (default 8388608)
    export limit: ?int
    /// With --marks, the read buffer, in bytes (default 16384)
    export buffer: ?int
    /// Files to read; none, or -, is standard input
    export files: []str
}

gen cli.Args(SentencesOptions)

struct SentencesState {
    // none for UTF-8, which is checked line by line instead of replaced
    from: ?encoding.Encoding
    limit: int
    // whether a paragraph has been written, so the next is set apart by a blank line
    wrote: bool
}

/// Runs `textkit sentences` with `args`. Returns the exit status.
export Sentences(c: &input.Ctx, s: mut input.Streams, stdin: mut Reader, args: []str): int {
    synopsis := "sentences [--marks [--hex] [--buffer n]] [--from label] [--limit bytes] [file...]"
    if input.WantsHelp(args) {
        return input.Help(s, synopsis, SentencesOptions.Usage)
    }
    opts := SentencesOptions.Parse(c.arena, args) else e {
        return input.BadArgs(s, "sentences", args, &e, SentencesOptions.Usage)
    }
    size := input.Size(s, "sentences", "--buffer", opts.buffer, input.BufferSize, 1) else {
        return input.Usage
    }
    if opts.marks {
        if opts.from.IsSome() {
            Print(mut s.err, "textkit sentences: --marks reads UTF-8; --from does not go with it\n") else {}
            return input.Usage
        }
        var marks := MarksState(unit: Unit.Sentence, hex: opts.hex, size: size, bad: 0)
        status := input.ForEach(c, s, stdin, opts.files, size, mut marks, fn(ctx: &input.Ctx, state: mut MarksState, streams: mut input.Streams, reader: mut Reader, name_of: str): input.Error!void { try MarksInput(ctx, state, streams, reader, name_of) })
        return if marks.bad > 0 { input.Worse(status, input.Failed) } else { status }
    }
    limit := input.Size(s, "sentences", "--limit", opts.limit, input.WholeLimit, 0) else {
        return input.Usage
    }
    var from: ?encoding.Encoding = none
    if label := opts.from {
        from = some(input.LookupLabel(s, "sentences", "--from", label) else {
            return input.Usage
        })
    }
    var st := SentencesState(from: from, limit: limit, wrote: false)
    return input.ForEach(c, s, stdin, opts.files, input.BufferSize, mut st, fn(ctx: &input.Ctx, state: mut SentencesState, streams: mut input.Streams, reader: mut Reader, name_of: str): input.Error!void { try SentencesInput(ctx, state, streams, reader, name_of) })
}

/// Writes the sentences of one whole input, one a line. A paragraph is a
/// run of lines that are not blank, joined by single spaces, since UAX 29
/// breaks a sentence after every line end (SB4); paragraphs are set apart
/// by a blank line.
SentencesInput(c: &input.Ctx, st: mut SentencesState, s: mut input.Streams, r: mut Reader, name: str): input.Error!void {
    var scratch := try c.arena.Sub()
    defer scratch.Free()
    content := if from := st.from {
        bytes := try input.ReadWhole(&scratch, r, st.limit)
        st.limit -= bytes.Len()
        try from.Decode(&scratch, bytes)
    } else {
        text := try input.ReadText(&scratch, r, st.limit)
        st.limit -= text.Len()
        text
    }
    var paragraph := Text.New(&scratch)
    for line in content.Lines() {
        trimmed := line.Trim()
        if trimmed.Len() == 0 {
            try st.WriteParagraph(s, paragraph.Str())
            paragraph.Clear()
            continue
        }
        if paragraph.Str().Len() > 0 {
            try paragraph.Push(" ")
        }
        try paragraph.Push(trimmed)
    }
    try st.WriteParagraph(s, paragraph.Str())
}

/// Writes each sentence of `p` on a line of its own, with the white space
/// UAX 29 leaves at its end trimmed; an empty `p` writes nothing.
SentencesState.WriteParagraph(mut self, s: mut input.Streams, p: str): WriteError!void {
    if p.Len() == 0 {
        return
    }
    if self.wrote {
        try s.out.Str("\n")
    }
    self.wrote = true
    var start := 0
    for end in BoundsOf(p, Unit.Sentence) {
        sentence := p[start..end].Trim()
        start = end
        if sentence.Len() > 0 {
            try s.out.Str(sentence)
            try s.out.Str("\n")
        }
    }
}
