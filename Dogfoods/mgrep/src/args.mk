// Why: mgrep takes grep's short options (`-inw`) as well as long ones.
// What: Options, Expand (short options to long), Shorts, TakesValue and
// Help.
// How: each bundle of short letters is expanded to its long options before
// the long-option parser sees the command line.

import std.cli

/// mgrep's options. `gen cli.Args` writes `Parse` and `Usage` from the fields:
/// a field `max_count` is the option `--max-count`.
struct Options {
    /// Match case-insensitively (-i)
    export ignore_case: bool
    /// Take the patterns as fixed strings, not regular expressions (-F)
    export fixed_strings: bool
    /// Match whole words only (-w)
    export word_regexp: bool
    /// Print the lines that do not match (-v)
    export invert_match: bool
    /// Print the number of matching lines in each file (-c)
    export count: bool
    /// Print the number of matches in each file
    export count_matches: bool
    /// Print only the names of the files that match (-l)
    export files_with_matches: bool
    /// Print each match on a line of its own (-o)
    export only_matching: bool
    /// Print line numbers, which is the default (-n)
    export line_number: bool
    /// Leave out line numbers (-N)
    export no_line_number: bool
    /// Print the terminal column where each line's first match starts
    export column: bool
    /// Print each file's name once, above its lines
    export heading: bool
    /// Print the file name on every line, even for one file (-H)
    export with_filename: bool
    /// Never print file names
    export no_filename: bool
    /// Print this many lines after each match (-A)
    export after_context: ?int
    /// Print this many lines before each match (-B)
    export before_context: ?int
    /// Print this many lines before and after each match (-C)
    export context: ?int
    /// Stop reading a file after this many matching lines (-m)
    export max_count: ?int
    /// Print nothing, and stop at the first match (-q)
    export quiet: bool
    /// Search with this many tasks; 4 when not given (-j)
    export threads: ?int
    /// Keep at most this many files open at once; 8 when not given
    export max_open: ?int
    /// The pattern, when it starts with a dash (-e)
    export regexp: ?str
    /// Read the patterns from this file, one a line; - is standard input (-f)
    export file: ?str
    /// Print each matching line with its matches replaced; $1 and $name expand groups (-r)
    export replace: ?str
    /// With --only-matching, print this capture group of each match
    export group: ?int
    /// Colour output: always or never; never when not given
    export color: ?str
    /// Search names that start with a dot (-.)
    export hidden: bool
    /// Search only files whose names match one of these comma-separated globs (-g)
    export glob: ?str
    /// Descend at most this many directory levels below each path (-d)
    export max_depth: ?int
    /// Skip files larger than this many bytes
    export max_filesize: ?int
    /// Search files that hold NUL bytes as well (-a)
    export binary: bool
    /// Print paths with this separator instead of the system's
    export path_separator: ?str
    /// Print a table of the matching files and the totals at the end
    export stats: bool
    /// Print this help (-h)
    export help: bool
    /// Print the version (-V)
    export version: bool
    /// PATTERN, then the files and directories to search; - is standard input
    export args: []str
}

gen cli.Args(Options)

// A short option and the long one it stands for
struct Short {
    letter: u8
    long: str
    takes_value: bool
}

// cli.Args has no short options, so mgrep maps its own
const Shorts: []Short = [
    Short(letter: b'i', long: "--ignore-case", takes_value: false),
    Short(letter: b'F', long: "--fixed-strings", takes_value: false),
    Short(letter: b'w', long: "--word-regexp", takes_value: false),
    Short(letter: b'v', long: "--invert-match", takes_value: false),
    Short(letter: b'c', long: "--count", takes_value: false),
    Short(letter: b'l', long: "--files-with-matches", takes_value: false),
    Short(letter: b'o', long: "--only-matching", takes_value: false),
    Short(letter: b'n', long: "--line-number", takes_value: false),
    Short(letter: b'N', long: "--no-line-number", takes_value: false),
    Short(letter: b'H', long: "--with-filename", takes_value: false),
    Short(letter: b'A', long: "--after-context", takes_value: true),
    Short(letter: b'B', long: "--before-context", takes_value: true),
    Short(letter: b'C', long: "--context", takes_value: true),
    Short(letter: b'm', long: "--max-count", takes_value: true),
    Short(letter: b'q', long: "--quiet", takes_value: false),
    Short(letter: b'j', long: "--threads", takes_value: true),
    Short(letter: b'e', long: "--regexp", takes_value: true),
    Short(letter: b'f', long: "--file", takes_value: true),
    Short(letter: b'r', long: "--replace", takes_value: true),
    Short(letter: b'.', long: "--hidden", takes_value: false),
    Short(letter: b'g', long: "--glob", takes_value: true),
    Short(letter: b'd', long: "--max-depth", takes_value: true),
    Short(letter: b'a', long: "--binary", takes_value: false),
    Short(letter: b'h', long: "--help", takes_value: false),
    Short(letter: b'V', long: "--version", takes_value: false),
]

/// A short option mgrep does not have, with the index of the argument and the letter's offset in it.
error UnknownShort(arg: int, at: int)

/// The arguments with every short option written long, and for each the index
/// of the argument it came from, so a parse error names what the user typed.
struct Expanded {
    args: List[str]
    origin: List[int]
}

// Writes each short option, alone (`-i`), grouped (`-iw`) or with its value
// attached (`-C2`), as its long form, and splits `--name=value` in two. An
// argument after `--`, or one that is only `-`, is left as it is.
Expand(a: &Arena, args: []str): UnknownShort|OutOfMemory!Expanded from a, args *args into a {
    var out := List[str].New(a)
    var origin := List[int].New(a)
    var rest := false
    var value_next := false
    for i, arg in args {
        if value_next || rest || arg.Len() < 2 || arg.Byte(0) != b'-' {
            try out.Push(arg)
            try origin.Push(i)
            value_next = false
            continue
        }
        if arg == "--" {
            rest = true
            try out.Push(arg)
            try origin.Push(i)
            continue
        }
        if arg.StartsWith("--") {
            eq := arg.Find("=") else {
                try out.Push(arg)
                try origin.Push(i)
                value_next = TakesValue(arg)
                continue
            }
            try out.Push(arg[..eq])
            try out.Push(arg[eq + 1..])
            try origin.Push(i)
            try origin.Push(i)
            continue
        }
        var k := 1
        for k < arg.Len() {
            s := Lookup(arg.Byte(k)) else {
                fail UnknownShort(arg: i, at: k)
            }
            try out.Push(s.long)
            try origin.Push(i)
            k += 1
            if s.takes_value {
                if k < arg.Len() {
                    try out.Push(arg[k..])
                    try origin.Push(i)
                } else {
                    value_next = true
                }
                break
            }
        }
    }
    return Expanded(args: out, origin: origin)
}

// The long options that take a value, so that a value such as -1 is not read as an option.
const Valued: []str = ["--after-context", "--before-context", "--context", "--max-count", "--threads", "--max-open", "--regexp", "--file", "--replace", "--group", "--color", "--glob", "--max-depth", "--max-filesize", "--path-separator"]

TakesValue(long: str): bool {
    return Valued[..].Has(&long)
}

Lookup(letter: u8): ?Short {
    for s in Shorts {
        if s.letter == letter {
            return some(s)
        }
    }
    return none
}

// Writes the help: what mgrep does, its options, the short forms and the exit codes.
Help(w: mut Writer): WriteError!void {
    try w.Str("""
        mgrep: search files for lines that match a pattern

        usage: mgrep [OPTIONS] PATTERN [PATH...]
               mgrep [OPTIONS] -e PATTERN [PATH...]
               mgrep [OPTIONS] -f FILE [PATH...]

        Directories are searched recursively, each one's entries in byte order, and
        files that hold a NUL byte are skipped. With no PATH, mgrep searches the
        current directory. The output is the same whatever the number of carriers.

        """)
    try Options.Usage(w)
    try w.Str("""

        Short forms: -i -F -w -v -c -l -o -n -N -H -q -a -. -h -V, and -A N -B N -C N
        -m N -j N -e PAT -f FILE -r TEXT -g GLOBS -d N, which group as in -iw or -C2.
        --name=value is --name value.

        Exit status: 0 when a line matched, 1 when none did, 2 on an error.
        """)
}
