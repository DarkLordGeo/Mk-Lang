// Why: patterns can come from a file (`-f`) and text from standard input.
// What: ReadPatterns, PatternLines, SearchStdin and ReadAll.
// How: input is read whole up to a size limit (InputTooLarge past it) and
// searched as one buffer; pattern files are one pattern a line.

import std.fs
import src.search

/// The longest line mgrep reads from standard input or a pattern file.
const LineLimit: int = 1 << 20

// Reads the patterns of `path`, one a line, blank lines skipped, into
// `patterns`; `-` is standard input. Reports a failure on `err` and returns true.
ReadPatterns(a: &Arena into patterns, sys: &System, path: str, patterns: mut List[str], err: mut Writer): WriteError|OutOfMemory!bool {
    if path == "-" {
        var input := sys.Stdin()
        return try PatternLines(a, mut input, path, patterns, err)
    }
    var f := fs.Open(sys.io, path) else e {
        try Print(err, "mgrep: {path}: {e}\n", (path, e))
        return true
    }
    defer f.Abandon()
    buf := try a.Slice[u8](65536)
    var input := f.Reader(sys.io, buf)
    return try PatternLines(a, mut input, path, patterns, err)
}

PatternLines(a: &Arena into patterns, input: mut Reader, path: str, patterns: mut List[str], err: mut Writer): WriteError|OutOfMemory!bool {
    for {
        next := input.Line(a) else e {
            try Print(err, "mgrep: {path}: {e}\n", (path, e))
            return true
        }
        line := next else {
            return false
        }
        if line.Len() > 0 {
            try patterns.Push(line)
        }
    }
}

// Searches standard input, read line by line with Reader.Until into one
// buffer, under the name `<stdin>`.
SearchStdin(a: &Arena into a, sys: &System, m: &search.Matcher, cfg: &search.Config, out: mut Writer, err: mut Writer): WriteError|OutOfMemory!int {
    var input := sys.Stdin()
    data := ReadAll(a, mut input) else e {
        match e {
            OutOfMemory {
                fail OutOfMemory
            }
            _ {
                try Print(err, "mgrep: <stdin>: {e}\n", (e))
                return 2
            }
        }
    }
    var text := Text.New(a)
    var w := text.Writer()
    sum := try search.Search(a, m, cfg, "<stdin>", data, mut w)
    try out.Str(text.Str())
    if sum.invalid_lines > 0 && !cfg.quiet {
        try Print(err, "mgrep: <stdin>: lines skipped as not UTF-8: {n}\n", (n: sum.invalid_lines))
    }
    return if sum.matched_lines > 0 { 0 } else { 1 }
}

/// Standard input past LineLimit * 64 bytes.
error InputTooLarge

// Reads all of `input` into `a`, a line at a time, doubling the buffer as it fills.
ReadAll(a: &Arena, input: mut Reader): ReadError|InputTooLarge|OutOfMemory![]u8 from a {
    var buf := try a.Slice[u8](4096)
    var used := 0
    for {
        if used == buf.Len() {
            if buf.Len() >= LineLimit * 64 {
                fail InputTooLarge
            }
            var bigger := try a.Slice[u8](buf.Len() * 2)
            var front := mut bigger[..used]
            front.CopyFrom(buf[..used])
            buf = bigger
        }
        n := try input.Until(b'\n', mut buf[used..])
        if n == 0 {
            return buf[..used]
        }
        used += n
    }
}
