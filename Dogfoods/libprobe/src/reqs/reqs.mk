// Why: `libprobe check` verifies an install's library needs from a file.
// What: Parse, Need, Problem and the Syntax error.
// How: the file format follows.

// The requirements file `libprobe check` reads: one library a line, the
// search to find it by, then the symbols the program needs from it.
//
//     # what the installer needs
//     kernel32.dll system GetTickCount GetCurrentProcessId
//     C:/app/plugin.dll path PluginInit
//
// Words are separated by spaces or tabs, `#` starts a comment, and a line
// may end in `\r\n`.

/// What is wrong with a line.
export enum Problem {
    NoSearch
    UnknownSearch
    TooLong
    DuplicateSymbol
    TooManyLines
}

/// Line `line` of the file is wrong, for the reason given.
export error Syntax(line: int, problem: Problem)

/// One library the file requires.
export struct Need {
    /// The line it was written on, from 1.
    export line: int
    export name: str
    /// The search's word: "system", "application" or "path".
    export search: str
    export symbols: List[str]
}

// The most lines and the most symbols a line libprobe reads.
const MaxLines: int = 10_000
const MaxSymbols: int = 256

/// A sentence for a problem, for messages.
export Problem.Text(self): str {
    return match self {
        .NoSearch { "a library needs a search after its name (system, application or path)" }
        .UnknownSearch { "the search is not system, application or path" }
        .TooLong { "more than 256 symbols on one line" }
        .DuplicateSymbol { "a symbol is named twice for one library" }
        .TooManyLines { "more than 10000 lines" }
    }
}

// `line` without its comment and surrounding blanks.
Content(line: str): str {
    var s := line
    if hash := s.Find("#") {
        s = s[..hash]
    }
    return s.Trim()
}

// Whether `xs` already holds `x`.
Holds(xs: &List[str], x: str): bool {
    for y in xs.Items() {
        if y == x {
            return true
        }
    }
    return false
}

/// Parses the requirements in `text`. Fails at the first wrong line. O(n)
/// in the text; the result points into `text` and lives in `a`.
export Parse(a: &Arena, text: str into a): Syntax|OutOfMemory!List[Need] from a, text {
    var out := List[Need].New(a)
    var n := 0
    for full in text.Lines() {
        n += 1
        if n > MaxLines {
            fail Syntax(line: n, problem: Problem.TooManyLines)
        }
        line := Content(full)
        if line.Len() == 0 {
            continue
        }
        var need := Need(line: n, name: "", search: "", symbols: List[str].New(a))
        var word := 0
        for w in line.Words() {
            match word {
                0 { need.name = w }
                1 {
                    match w {
                        "system" | "application" | "path" { need.search = w }
                        _ { fail Syntax(line: n, problem: Problem.UnknownSearch) }
                    }
                }
                _ {
                    if need.symbols.Len() == MaxSymbols {
                        fail Syntax(line: n, problem: Problem.TooLong)
                    }
                    if Holds(&need.symbols, w) {
                        fail Syntax(line: n, problem: Problem.DuplicateSymbol)
                    }
                    try need.symbols.Push(w)
                }
            }
            word += 1
        }
        if word < 2 {
            fail Syntax(line: n, problem: Problem.NoSearch)
        }
        try out.Push(need)
    }
    return out
}
