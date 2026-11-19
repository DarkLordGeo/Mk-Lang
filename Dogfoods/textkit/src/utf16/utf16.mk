// Why: `textkit utf16` converts between UTF-8 and UTF-16.
// What: Utf16, Utf16Options and the OddLength and Unpaired errors.
// How: either byte order, or the one a byte order mark names; unpaired
// surrogates are reported by position.

import std.cli
import std.text
import src.input
import src.scalar

/// `textkit utf16`: converts text between UTF-8 and UTF-16.
struct Utf16Options {
    /// Write or read big-endian code units (default little-endian)
    export big: bool
    /// Begin the encoded output with a byte order mark
    export bom: bool
    /// Write or read code units as hex numbers, one line of text per line
    export hex: bool
    /// The most bytes to read, all inputs together (default 8388608)
    export limit: ?int
    /// encode or decode, then the files to read; none, or -, is standard input
    export args: []str
}

gen cli.Args(Utf16Options)

/// An odd number of bytes, which no sequence of 16-bit code units has.
export error OddLength(bytes: int)

/// UTF-16 that pairs no surrogate, or hex that names no code unit; counted from 1.
export error Unpaired(unit: int)

struct Utf16State {
    encode: bool
    big: bool
    bom: bool
    hex: bool
    limit: int
    bad: int
}

/// Runs `textkit utf16` with `args`. Returns the exit status.
export Utf16(c: &input.Ctx, s: mut input.Streams, stdin: mut Reader, args: []str): int {
    synopsis := "utf16 encode|decode [--big] [--bom] [--hex] [--limit bytes] [file...]"
    if input.WantsHelp(args) {
        return input.Help(s, synopsis, Utf16Options.Usage)
    }
    opts := Utf16Options.Parse(c.arena, args) else e {
        return input.BadArgs(s, "utf16", args, &e, Utf16Options.Usage)
    }
    if opts.args.Len() == 0 || (opts.args[0] != "encode" && opts.args[0] != "decode") {
        Print(mut s.err, "textkit utf16: say encode or decode\n") else {}
        Utf16Options.Usage(mut s.err) else {}
        return input.Usage
    }
    limit := input.Size(s, "utf16", "--limit", opts.limit, input.WholeLimit, 0) else {
        return input.Usage
    }
    var st := Utf16State(
        encode: opts.args[0] == "encode",
        big: opts.big,
        bom: opts.bom,
        hex: opts.hex,
        limit: limit,
        bad: 0,
    )
    status := input.ForEach(c, s, stdin, opts.args[1..], input.BufferSize, mut st, fn(ctx: &input.Ctx, state: mut Utf16State, streams: mut input.Streams, reader: mut Reader, name_of: str): input.Error!void { try Utf16Input(ctx, state, streams, reader, name_of) })
    return if st.bad > 0 { input.Worse(status, input.Failed) } else { status }
}

/// Converts one whole input, which is read with `Reader.ReadAll`.
Utf16Input(c: &input.Ctx, st: mut Utf16State, s: mut input.Streams, r: mut Reader, name: str): input.Error!void {
    var scratch := try c.arena.Sub()
    defer scratch.Free()
    if st.encode {
        content := try input.ReadText(&scratch, r, st.limit)
        st.limit -= content.Len()
        if st.hex {
            for line in content.Lines() {
                try WriteHex(mut s.out, try text.ToUtf16(&scratch, line))
            }
            return
        }
        if st.bom {
            mark: [1]u16 = [0xFEFF]
            try WriteUnits(mut s.out, mark[..], st.big)
        }
        try WriteUnits(mut s.out, try text.ToUtf16(&scratch, content), st.big)
        return
    }
    if st.hex {
        content := try input.ReadText(&scratch, r, st.limit)
        st.limit -= content.Len()
        var n := 0
        for line in content.Lines() {
            n += 1
            try st.DecodeHexLine(&scratch, s, line, name, n)
        }
        return
    }
    bytes := try input.ReadWhole(&scratch, r, st.limit)
    st.limit -= bytes.Len()
    try st.DecodeBytes(&scratch, s, bytes, name)
}

/// Decodes UTF-16 bytes in the order `st.big` names, or the order a byte
/// order mark at the start names, which is then dropped.
Utf16State.DecodeBytes(mut self, a: &Arena, s: mut input.Streams, bytes: []u8, name: str): input.Error!void {
    if bytes.Len() % 2 != 0 {
        input.Report(s, name, &OddLength(bytes: bytes.Len()))
        self.bad += 1
        return
    }
    var big := self.big
    var start := 0
    if bytes.Len() >= 2 && bytes[0] == 0xFE && bytes[1] == 0xFF {
        big = true
        start = 2
    } else if bytes.Len() >= 2 && bytes[0] == 0xFF && bytes[1] == 0xFE {
        big = false
        start = 2
    }
    units := try a.Slice[u16]((bytes.Len() - start) / 2)
    high := if big { 0 } else { 1 }
    for i in 0..units.Len() {
        hi := u16(bytes[start + 2 * i + high])
        lo := u16(bytes[start + 2 * i + 1 - high])
        units[i] = hi << 8 | lo
    }
    decoded := text.FromUtf16(a, units[..]) else e {
        return match e {
            OutOfMemory { fail OutOfMemory }
            Utf8Error {
                input.Report(s, name, &Unpaired(unit: FirstUnpaired(units[..]) + 1))
                self.bad += 1
                return
            }
        }
    }
    try s.out.Str(decoded)
}

/// Decodes one line of hex code units, such as `D83D DE00`, and writes the
/// text and a newline. A bad line is reported, and the rest go on.
Utf16State.DecodeHexLine(mut self, a: &Arena, s: mut input.Streams, line: str, name: str, n: int): input.Error!void {
    var units := List[u16].New(a)
    var i := 0
    for word in line.Words() {
        i += 1
        v := scalar.ParseHex(word) else {
            input.ReportAt(s, name, n, &scalar.BadScalar(token: i))
            self.bad += 1
            return
        }
        if v > 0xFFFF {
            input.ReportAt(s, name, n, &scalar.BadScalar(token: i))
            self.bad += 1
            return
        }
        try units.Push(Trunc[u16](v))
    }
    all := units.Done()
    decoded := text.FromUtf16(a, all[..]) else e {
        return match e {
            OutOfMemory { fail OutOfMemory }
            Utf8Error {
                input.ReportAt(s, name, n, &Unpaired(unit: FirstUnpaired(all[..]) + 1))
                self.bad += 1
                return
            }
        }
    }
    try s.out.Str(decoded)
    try s.out.Str("\n")
}

/// The index of the first surrogate in `units` that has no partner, or
/// `units.Len()` when every one has one.
export FirstUnpaired(units: []u16): int {
    var i := 0
    for i < units.Len() {
        u := units[i]
        if u >= 0xD800 && u <= 0xDBFF {
            if i + 1 < units.Len() && units[i + 1] >= 0xDC00 && units[i + 1] <= 0xDFFF {
                i += 2
                continue
            }
            return i
        }
        if u >= 0xDC00 && u <= 0xDFFF {
            return i
        }
        i += 1
    }
    return i
}

/// Writes `units` as bytes, big-endian when `big`.
WriteUnits(w: mut Writer, units: []u16, big: bool): WriteError!void {
    var pair: [2]u8 = [0; 2]
    for u in units {
        hi := Trunc[u8](u >> 8)
        lo := Trunc[u8](u)
        pair[0] = if big { hi } else { lo }
        pair[1] = if big { lo } else { hi }
        try w.Bytes(pair[..])
    }
}

/// Writes `units` as upper-case hex on one line.
WriteHex(w: mut Writer, units: []u16): WriteError!void {
    for i, u in units {
        if i > 0 {
            try w.Str(" ")
        }
        try Print(w, "{u:04X}", (u))
    }
    try w.Str("\n")
}
