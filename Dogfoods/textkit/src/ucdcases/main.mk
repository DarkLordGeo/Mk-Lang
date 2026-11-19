// Why: textkit's cases come from the Unicode and WHATWG data files.
// What: Main, Options and Normalization.
// How: writes each test file as cases whose expected outputs are the
// sources' own columns, never textkit's output.

import std.cli
import std.fs

/// `ucdcases`: turns the UCD's NormalizationTest.txt and its grapheme, word
/// and sentence break tests, and the WHATWG Encoding Standard's label table
/// and single-byte indexes, into textkit's cases, so the cases need no
/// source file when they run. The expected outputs are the sources' own
/// columns, break marks and mappings, never textkit's output.
struct Options {
    /// Keep every n-th line of NormalizationTest's part 1, the per-character part (default 16)
    export every: ?int
    /// A directory holding the Encoding Standard's encodings.json and index-*.txt files
    export whatwg: ?str
    /// The UCD directory (holding NormalizationTest.txt and auxiliary/), then the cases directory
    export dirs: []str
}

gen cli.Args(Options)

/// The normalization forms in the order of NormalizationTest's columns 2 to 5.
const Forms: [4]str = ["nfc", "nfd", "nfkc", "nfkd"]

/// Reads the UCD test files, and the Encoding Standard's with `--whatwg`,
/// and writes the cases. Returns 2 on a bad command line and 1 when a file
/// cannot be read or written.
export Main(sys: &System): fs.Error|OutOfMemory|WriteError!int {
    args := sys.args[1..]
    for arg in args {
        if arg == "--help" {
            try sys.Print("usage: ucdcases [--every n] [--whatwg dir] <ucd-dir> <cases-dir>\n")
            var out := sys.Stdout()
            try Options.Usage(mut out)
            return 0
        }
    }
    opts := Options.Parse(sys.arena, args) else e {
        try sys.PrintErr("ucdcases: {e}\n", (e))
        var err := sys.Stderr()
        try Options.Usage(mut err)
        return 2
    }
    every := opts.every else { 16 }
    if opts.dirs.Len() != 2 || every < 1 {
        try sys.PrintErr("usage: ucdcases [--every n] [--whatwg dir] <ucd-dir> <cases-dir>\n")
        return 2
    }
    ucd := opts.dirs[0]
    cases := opts.dirs[1]
    fs.MakeDir(sys.io, try sys.arena.Fmt("{cases}/data", (cases))) else e {
        match e {
            fs.Exists {}
            _ { fail e }
        }
    }
    norm_path := try sys.arena.Fmt("{ucd}/NormalizationTest.txt", (ucd))
    norms := Normalization(sys.arena, sys.io, norm_path, cases, every) else e {
        try sys.PrintErr("ucdcases: {norm_path}: {e}\n", (norm_path, e))
        return 1
    }
    var breaks: [3]int = [0; 3]
    for i, break_file in BreakTests {
        path := try sys.arena.Fmt("{ucd}/auxiliary/{f}", (ucd, f: break_file.file))
        breaks[i] = BreakTest(sys.arena, sys.io, path, cases, &break_file) else e {
            try sys.PrintErr("ucdcases: {path}: {e}\n", (path, e))
            return 1
        }
    }
    try sys.Print("wrote {norms} normalization, {g} grapheme, {w} word and {s} sentence break tests under {cases}\n", (norms, g: breaks[0], w: breaks[1], s: breaks[2], cases))
    if dir := opts.whatwg {
        written := Whatwg(sys.arena, sys.io, dir, cases) else e {
            try sys.PrintErr("ucdcases: {dir}: {e}\n", (dir, e))
            return 1
        }
        try sys.Print("wrote {n} labels and {m} single-byte encodings under {cases}\n", (n: written.labels, m: written.encodings, cases))
    }
    return 0
}

/// Writes `data/norm-source.txt`, column 1 of the kept lines, and for each
/// form the expected output `ucd-<form>.out`, columns 2 to 5.
Normalization(a: &Arena, io: Io, path: str, cases: str, every: int): fs.Error|OutOfMemory!int {
    content := try fs.ReadText(a, io, path)
    var source := Text.New(a)
    var expected: [4]Text = [Text.New(a), Text.New(a), Text.New(a), Text.New(a)]
    var part := ""
    var n := 0
    var kept := 0
    for line in content.Lines() {
        if line.StartsWith("@Part") {
            part = line
            n = 0
            continue
        }
        if line.Len() == 0 || line.StartsWith("#") {
            continue
        }
        n += 1
        if part.StartsWith("@Part1") && n % every != 1 {
            continue
        }
        kept += 1
        var column := 0
        for field in line.Split(";") {
            if column == 0 {
                try source.Push(field.Trim())
                try source.Push("\n")
            } else if column <= 4 {
                try expected[column - 1].Push(field.Trim())
                try expected[column - 1].Push("\n")
            }
            column += 1
        }
    }
    try fs.WriteAll(io, try a.Fmt("{cases}/data/norm-source.txt", (cases)), source.Str().AsBytes())
    for i, form in Forms {
        try fs.WriteAll(io, try a.Fmt("{cases}/ucd-{form}.out", (cases, form)), expected[i].Str().AsBytes())
    }
    return kept
}
