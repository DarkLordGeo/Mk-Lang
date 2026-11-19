// Why: the segmenting commands share their output.
// What: Unit, BoundsOf and WriteMarks.
// How: marks UAX 29 boundaries in each line.

import std.text
import src.input
import src.normal
import src.scalar

/// What a text is segmented into, by UAX 29.
export enum Unit {
    Word
    Sentence
}

/// The boundaries of `s` for `unit`: `text.WordBounds` or `text.SentenceBounds`.
export BoundsOf(s: str, unit: Unit): text.Bounds {
    return match unit {
        .Word { text.WordBounds(s) }
        .Sentence { text.SentenceBounds(s) }
    }
}

/// How many bytes `c` takes in UTF-8 (RFC 3629 section 3).
export Utf8Len(c: char): int {
    n := u32(c)
    return if n < 0x80 { 1 } else if n < 0x800 { 2 } else if n < 0x10000 { 3 } else { 4 }
}

/// Writes the `unit` boundaries of `s` as UAX 29's test files do:
/// `÷ 0041 × 0042 ÷ 0020 ÷`, with `÷` at every boundary and `×` between two
/// scalars no boundary separates. An empty `s` writes nothing. O(n);
/// allocates nothing.
export WriteMarks(w: mut Writer, s: str, unit: Unit): WriteError!void {
    if s.Len() == 0 {
        return
    }
    var bounds := BoundsOf(s, unit)
    // the boundary at 0 comes first; -1 stands for none left
    var next := bounds.Next() else { -1 }
    var at := 0
    for c in s.Scalars() {
        if at == next {
            try w.Str("÷ ")
            next = bounds.Next() else { -1 }
        } else {
            try w.Str("× ")
        }
        try Print(w, "{n:04X} ", (n: u32(c)))
        at += Utf8Len(c)
    }
    try w.Str("÷")
}

/// The options `--marks` reads lines with.
export struct MarksState {
    export unit: Unit
    export hex: bool
    export size: int
    // lines whose hex names no scalar
    export bad: int
}

/// Writes the boundaries of each line of `r` in UAX 29's notation, one
/// output line per input line. A line of hex that names no scalar is
/// reported, and the next line is still read.
export MarksInput(c: &input.Ctx, st: mut MarksState, s: mut input.Streams, r: mut Reader, name: str): input.Error!void {
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
        try WriteMarks(mut s.out, decoded, st.unit)
        try s.out.Str("\n")
        scratch.Reset()
    }
}

/// Writes `segment` and a newline, or its scalars in hex when `hex`.
export WriteSegment(w: mut Writer, segment: str, hex: bool): WriteError!void {
    if hex {
        try scalar.Write(w, segment, " ")
    } else {
        try w.Str(segment)
    }
    try w.Str("\n")
}
