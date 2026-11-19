// Why: `textkit norm` rewrites lines in a Unicode normalization form.
// What: Norm, NormOptions and ParseForm.
// How: NFC, NFD, NFKC or NFKD on each line, as text or as hex scalars, in
// a scratch arena reset after each line.

import std.cli
import std.text
import src.input
import src.scalar

/// `textkit norm`: rewrites each line in a Unicode normalization form.
struct NormOptions {
    /// The form: nfc, nfd, nfkc or nfkd (default nfc)
    export form: ?str
    /// Read and write lines of code points in hex, as the UCD's test files do
    export hex: bool
    /// Print where lines are not already in the form; exit 1 if any are
    export check: bool
    /// The read buffer, in bytes (default 16384)
    export buffer: ?int
    /// Files to read; none, or -, is standard input
    export files: []str
}

gen cli.Args(NormOptions)

struct NormState {
    form: text.Form
    hex: bool
    check: bool
    size: int
    // lines not in the form (with check), or not decodable (with hex)
    unnormal: int
    bad: int
}

/// The normalization form `name` names, in any case, or `none`.
export ParseForm(a: &Arena, name: str): OutOfMemory!?text.Form {
    lower := try name.ToLower(a)
    return match lower {
        "nfc" { some(text.Form.Nfc) }
        "nfd" { some(text.Form.Nfd) }
        "nfkc" { some(text.Form.Nfkc) }
        "nfkd" { some(text.Form.Nfkd) }
        _ { none }
    }
}

/// The name of `form`, upper case, as Unicode writes it.
export FormName(form: text.Form): str {
    return match form {
        .Nfc { "NFC" }
        .Nfd { "NFD" }
        .Nfkc { "NFKC" }
        .Nfkd { "NFKD" }
    }
}

/// Runs `textkit norm` with `args`, the arguments after the subcommand.
/// Returns the exit status.
export Norm(c: &input.Ctx, s: mut input.Streams, stdin: mut Reader, args: []str): int {
    synopsis := "norm [--form nfc|nfd|nfkc|nfkd] [--hex] [--check] [--buffer n] [file...]"
    if input.WantsHelp(args) {
        return input.Help(s, synopsis, NormOptions.Usage)
    }
    opts := NormOptions.Parse(c.arena, args) else e {
        return input.BadArgs(s, "norm", args, &e, NormOptions.Usage)
    }
    name := opts.form else { "nfc" }
    parsed := ParseForm(c.arena, name) else {
        return input.NoMemory
    }
    form := parsed else {
        Print(mut s.err, "textkit norm: unknown form {name}: use nfc, nfd, nfkc or nfkd\n", (name)) else {}
        return input.Usage
    }
    size := input.Size(s, "norm", "--buffer", opts.buffer, input.BufferSize, 1) else {
        return input.Usage
    }
    var st := NormState(
        form: form,
        hex: opts.hex,
        check: opts.check,
        size: size,
        unnormal: 0,
        bad: 0,
    )
    var status := input.ForEach(c, s, stdin, opts.files, st.size, mut st, fn(ctx: &input.Ctx, state: mut NormState, streams: mut input.Streams, reader: mut Reader, name_of: str): input.Error!void { try NormInput(ctx, state, streams, reader, name_of) })
    if st.bad > 0 {
        status = input.Worse(status, input.Failed)
    }
    if st.unnormal > 0 {
        status = input.Worse(status, input.NoMatch)
    }
    return status
}

/// Normalizes every line of `r`. A line is copied into a scratch arena that
/// is reset after it, so memory stays at the longest line.
NormInput(c: &input.Ctx, st: mut NormState, s: mut input.Streams, r: mut Reader, name: str): input.Error!void {
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
        normal := try text.Normalize(&scratch, decoded, st.form)
        if st.check {
            if normal != decoded {
                st.unnormal += 1
                try Print(mut s.out, "{name}:{n}: not {f}\n", (name, n: lines.LineNumber(), f: FormName(st.form)))
            }
        } else {
            try Encode(mut s.out, normal, st.hex)
            try s.out.Str("\n")
        }
        scratch.Reset()
    }
}

/// `line` as text: itself, or the scalars its hex notation names.
export Decode(a: &Arena, line: str, hex: bool): scalar.Error!str from a, line {
    if hex {
        return try scalar.Decode(a, line)
    }
    return line
}

/// Writes `s`, or its scalars in hex.
export Encode(w: mut Writer, s: str, hex: bool): WriteError!void {
    if hex {
        try scalar.Write(w, s, " ")
        return
    }
    try w.Str(s)
}
