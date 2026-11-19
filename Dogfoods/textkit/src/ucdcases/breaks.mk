// Why: the UCD's break tests become textkit cases.
// What: BreakFile and BreakTests.
// How: one entry per UAX 29 test file and the case it becomes.

import std.fs

/// One of the UCD's break test files (UAX 29) and the case it becomes.
struct BreakFile {
    // the file's name under the UCD's auxiliary/ directory
    file: str
    // the source file written under data/, each test's code points
    source: str
    // the expected output, each test's break marks
    expected: str
}

/// The grapheme cluster, word and sentence break tests, in that order.
const BreakTests: [3]BreakFile = [
    BreakFile(file: "GraphemeBreakTest.txt", source: "grapheme-source.txt", expected: "ucd-graphemes.out"),
    BreakFile(file: "WordBreakTest.txt", source: "word-source.txt", expected: "ucd-words.out"),
    BreakFile(file: "SentenceBreakTest.txt", source: "sentence-source.txt", expected: "ucd-sentences.out"),
]

/// Writes `data/<file.source>`, each test's code points, and
/// `<file.expected>`, each test's breaks as the file marks them (`÷ 0041 ×
/// 0308 ÷`). Returns the number of tests.
BreakTest(a: &Arena, io: Io, path: str, cases: str, file: &BreakFile): fs.Error|OutOfMemory!int {
    content := try fs.ReadText(a, io, path)
    var source := Text.New(a)
    var expected := Text.New(a)
    var kept := 0
    for line in content.Lines() {
        if !line.StartsWith("÷") {
            continue
        }
        kept += 1
        marks := line[..line.Find("#") else { line.Len() }].Trim()
        try expected.Push(marks)
        try expected.Push("\n")
        var first := true
        for word in marks.Words() {
            if word == "÷" || word == "×" {
                continue
            }
            if !first {
                try source.Push(" ")
            }
            first = false
            try source.Push(word)
        }
        try source.Push("\n")
    }
    try fs.WriteAll(io, try a.Fmt("{cases}/data/{f}", (cases, f: file.source)), source.Str().AsBytes())
    try fs.WriteAll(io, try a.Fmt("{cases}/{f}", (cases, f: file.expected)), expected.Str().AsBytes())
    return kept
}
