// Why: text arrives in legacy encodings.
// What: Convert, ConvertOptions and the labels listing.
// How: decodes through std.encoding's WHATWG decoders and writes UTF-8 or
// another encoding, or a hex or base64 dump.

import std.cli
import std.encoding
import src.input

/// `textkit convert`: converts text between the encodings of the WHATWG
/// Encoding Standard, in the manner of iconv.
struct ConvertOptions {
    /// The input's encoding, by any label of the Encoding Standard (default utf-8)
    export from: ?str
    /// The output's encoding, by any label of the Encoding Standard (default utf-8)
    export to: ?str
    /// Stop at the first malformed byte instead of writing U+FFFD; reads each input whole
    export strict: bool
    /// Write the output bytes as raw, hex or base64 (default raw)
    export dump: ?str
    /// Print the encoding each line of the input names, instead of converting
    export labels: bool
    /// The read buffer, in bytes (default 16384)
    export buffer: ?int
    /// With --strict, the most bytes to read, all inputs together (default 8388608)
    export limit: ?int
    /// Files to read; none, or -, is standard input
    export files: []str
}

gen cli.Args(ConvertOptions)

struct ConvertState {
    from: encoding.Encoding
    to: encoding.Encoding
    strict: bool
    size: int
    limit: int
    dump: Dumper
    bad: int
}

/// Runs `textkit convert` with `args`. Returns the exit status.
export Convert(c: &input.Ctx, s: mut input.Streams, stdin: mut Reader, args: []str): int {
    synopsis := "convert [--from label] [--to label] [--strict] [--dump raw|hex|base64] [--labels] [--buffer n] [--limit bytes] [file...]"
    if input.WantsHelp(args) {
        return input.Help(s, synopsis, ConvertOptions.Usage)
    }
    opts := ConvertOptions.Parse(c.arena, args) else e {
        return input.BadArgs(s, "convert", args, &e, ConvertOptions.Usage)
    }
    size := input.Size(s, "convert", "--buffer", opts.buffer, input.BufferSize, 1) else {
        return input.Usage
    }
    if opts.labels {
        var labels := LabelsState(bad: 0)
        status := input.ForEach(c, s, stdin, opts.files, size, mut labels, fn(ctx: &input.Ctx, state: mut LabelsState, streams: mut input.Streams, reader: mut Reader, name_of: str): input.Error!void { try LabelsInput(ctx, state, streams, reader, name_of) })
        return if labels.bad > 0 { input.Worse(status, input.Failed) } else { status }
    }
    limit := input.Size(s, "convert", "--limit", opts.limit, input.WholeLimit, 0) else {
        return input.Usage
    }
    from := input.LookupLabel(s, "convert", "--from", opts.from else { "utf-8" }) else {
        return input.Usage
    }
    to := input.LookupLabel(s, "convert", "--to", opts.to else { "utf-8" }) else {
        return input.Usage
    }
    // the Encoding Standard gives the replacement encoding a decoder only
    if to.Name() == "replacement" {
        Print(mut s.err, "textkit convert: --to {label}: the replacement encoding only decodes\n", (label: opts.to else { "" })) else {}
        return input.Usage
    }
    kind := ParseDump(opts.dump else { "raw" }) else {
        Print(mut s.err, "textkit convert: --dump must be raw, hex or base64, not {d}\n", (d: opts.dump else { "" })) else {}
        return input.Usage
    }
    var st := ConvertState(
        from: from,
        to: to,
        strict: opts.strict,
        size: size,
        limit: limit,
        dump: Dumper.New(kind),
        bad: 0,
    )
    var status := input.ForEach(c, s, stdin, opts.files, size, mut st, fn(ctx: &input.Ctx, state: mut ConvertState, streams: mut input.Streams, reader: mut Reader, name_of: str): input.Error!void { try ConvertInput(ctx, state, streams, reader, name_of) })
    st.dump.Finish(c.arena, mut s.out) else e {
        input.Report(s, "output", &e)
        return match e {
            OutOfMemory { input.NoMemory }
            WriteError { input.Failed }
        }
    }
    if st.bad > 0 {
        status = input.Worse(status, input.Failed)
    }
    return status
}

/// Converts one input: decoded a piece at a time by the input encoding's
/// streaming `Decoder`, or whole by `DecodeStrict` with `--strict`, then
/// encoded and written. An input stops at a malformed byte (`--strict`) or
/// at a scalar the output encoding has no bytes for; that is reported, and
/// the next input is still read.
ConvertInput(c: &input.Ctx, st: mut ConvertState, s: mut input.Streams, r: mut Reader, name: str): input.Error!void {
    var scratch := try Arena.New(c.heap)
    defer scratch.Free()
    if st.strict {
        bytes := try input.ReadWhole(&scratch, r, st.limit)
        st.limit -= bytes.Len()
        decoded := st.from.DecodeStrict(&scratch, bytes) else e {
            return match e {
                OutOfMemory { fail OutOfMemory }
                encoding.BadInput(offset) {
                    s.out.Flush() else {}
                    Print(mut s.err, "textkit: {name}: malformed {f} at byte {offset}\n", (name, f: st.from.Name(), offset)) else {}
                    st.bad += 1
                    return
                }
                _ {
                    input.Report(s, name, &e)
                    st.bad += 1
                    return
                }
            }
        }
        _ = try st.Emit(&scratch, s, decoded, name, 0)
        return
    }
    buf := try scratch.Slice[u8](st.size)
    var step := try scratch.Sub()
    defer step.Free()
    var decoder := st.from.Decoder()
    // scalars written so far, to place a scalar the output cannot encode
    var seen := 0
    for {
        n := try r.Read(buf)
        last := n == 0
        decoded := try decoder.Push(&step, buf[..n], last)
        next := try st.Emit(&step, s, decoded, name, seen)
        seen = next else {
            return
        }
        step.Reset()
        if last {
            return
        }
    }
}

/// Encodes `decoded`, the scalars after the first `seen` of the input, and
/// writes the bytes. Returns the number of scalars seen after it, or `none`
/// after reporting a scalar the output encoding has no bytes for, once the
/// bytes of the scalars before it are written, as iconv does.
ConvertState.Emit(mut self, a: &Arena, s: mut input.Streams, decoded: str, name: str, seen: int): OutOfMemory|WriteError!?int {
    bytes := self.to.Encode(a, decoded) else e {
        return match e {
            OutOfMemory { fail OutOfMemory }
            encoding.BadInput(offset) {
                // every scalar before offset has bytes, so this cannot fail the same way
                before := try self.Emit(a, s, decoded[..offset], name, seen)
                at := (before else { seen }) + 1
                var code: u32 = 0
                for ch in decoded[offset..].Scalars() {
                    code = u32(ch)
                    break
                }
                try s.out.Flush()
                Print(mut s.err, "textkit: {name}: U+{code:04X}, scalar {at}, has no bytes in {t}\n", (name, code, at, t: self.to.Name())) else {}
                self.bad += 1
                none
            }
            _ {
                input.Report(s, name, &e)
                self.bad += 1
                none
            }
        }
    }
    try self.dump.Write(a, mut s.out, bytes)
    return some(seen + Scalars(decoded))
}

/// The number of scalars in `s`.
Scalars(s: str): int {
    var n := 0
    for _ in s.Scalars() {
        n += 1
    }
    return n
}

struct LabelsState {
    // lines naming no encoding textkit converts
    bad: int
}

/// Prints `label<tab>name` for each line of `r` that holds a label of the
/// Encoding Standard textkit converts; a line holding any other label is
/// reported with why, and counted. Blank lines are skipped.
LabelsInput(c: &input.Ctx, st: mut LabelsState, s: mut input.Streams, r: mut Reader, name: str): input.Error!void {
    var scratch := try Arena.New(c.heap)
    defer scratch.Free()
    var lines := try input.LineSource.New(c.arena, r, input.BufferSize)
    for {
        line := try lines.Next(&scratch) else { break }
        label := line.Trim()
        if label.Len() == 0 {
            scratch.Reset()
            continue
        }
        found := encoding.Lookup(label) else e {
            try s.out.Flush()
            Print(mut s.err, "textkit: {name}:{n}: {label}: {p}\n", (name, n: lines.LineNumber(), label, p: input.Problem(&e))) else {}
            st.bad += 1
            scratch.Reset()
            continue
        }
        try Print(mut s.out, "{label}\t{n}\n", (label, n: found.Name()))
        scratch.Reset()
    }
}
