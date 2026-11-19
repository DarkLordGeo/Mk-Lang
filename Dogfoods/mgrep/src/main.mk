// Why: the entry point and grep's exit statuses: 0 matched, 1 no match, 2
// trouble.
// What: Main, Grep, Status and the option checks (OutOfRange, BadColor).
// How: parses the command line, compiles the matcher, walks the roots and
// hands the files to the parallel search in work.Run.

import std.cli
import std.text
import src.search
import src.walk
import src.work

/// The version `--version` prints.
const Version: str = "mgrep 1.0.0"

/// The exit status when a line matched.
const Matched: int = 0

/// The exit status when no line matched.
const NoMatch: int = 1

/// The exit status on an error.
const Trouble: int = 2

export Main(sys: &System): int {
    var out := sys.Stdout()
    var err := sys.Stderr()
    return Grep(sys, mut out, mut err) else e {
        sys.PrintErr("mgrep: {e}\n", (e)) else {}
        Trouble
    }
}

// Parses the command line, then searches standard input or the paths it names.
Grep(sys: &System, out: mut Writer, err: mut Writer): WriteError|OutOfMemory!int {
    // the file list lives in an arena of Main's own: with sys.arena in its
    // place, storing into the list is refused as a store into `*sys`
    var arena := try Arena.New(sys.heap)
    defer arena.Free()
    a := &arena
    expanded := Expand(a, sys.args[1..]) else e {
        match e {
            UnknownShort(arg, at) {
                word := sys.args[1 + arg]
                try Print(err, "mgrep: unknown option -{c} in {word}\n", (c: word[at..at + 1], word))
                try err.Str("try mgrep --help\n")
                return Trouble
            }
            OutOfMemory {
                fail OutOfMemory
            }
        }
    }
    opts := Options.Parse(a, expanded.args[..]) else e {
        try Explain(err, sys.args[1..], &expanded, e)
        return Trouble
    }
    if opts.help {
        try Help(out)
        return Matched
    }
    if opts.version {
        try Print(out, "{v}\n", (v: Version))
        return Matched
    }
    var patterns := List[str].New(a)
    var paths := opts.args
    if p := opts.regexp {
        try patterns.Push(p)
    } else if f := opts.file {
        failed := try ReadPatterns(a, sys, f, mut patterns, err)
        if failed {
            return Trouble
        }
    } else if paths.Len() > 0 {
        try patterns.Push(try a.Dup(paths[0]))
        paths = paths[1..]
    } else {
        try err.Str("mgrep: no pattern given\ntry mgrep --help\n")
        return Trouble
    }
    tune := Tune(&opts) else e {
        try Problem(err, e)
        return Trouble
    }
    // the options are checked before any file is read
    _ = Configure(&opts, false) else e {
        try Problem(err, e)
        return Trouble
    }
    spec := search.Spec(patterns: patterns[..], fixed: opts.fixed_strings, fold: opts.ignore_case, word: opts.word_regexp)
    m := search.Compile(a, &spec) else e {
        try BadPattern(err, &opts, patterns[..], e)
        return Trouble
    }
    if patterns.Len() == 0 {
        return NoMatch
    }
    if paths.Len() == 1 && paths[0] == "-" {
        plain := Configure(&opts, opts.with_filename) else {
            return Trouble
        }
        return try SearchStdin(a, sys, &m, &plain, out, err)
    }
    here := ["."]
    roots := if paths.Len() == 0 { here[..] } else { paths }
    one := [opts.glob else { "" }]
    globs := if opts.glob.IsSome() { one[..] } else { one[..0] }
    filter := walk.Filter(hidden: opts.hidden, globs: globs, max_depth: opts.max_depth else { -1 }, max_size: Cast[i64](opts.max_filesize else { -1 }), separator: opts.path_separator else { "" })
    // the roots are copied into the arena, so `files` holds no loan of `*sys`
    var owned := List[str].New(a)
    for root in roots {
        try owned.Push(try a.Dup(root))
    }
    var files := List[walk.Source].New(a)
    problems := try walk.Walk(a, sys.io, owned[..], &filter, mut files, err)
    // one file named alone prints no file names, as grep does
    alone := roots.Len() == 1 && files.Len() == 1 && files[0].path == roots[0]
    cfg := Configure(&opts, !alone) else {
        return Trouble
    }
    totals := try work.Run(a, sys.heap, sys.io, files[..], &m, &cfg, &tune, out, err)
    return Status(&cfg, problems + totals.failed, totals.matched_lines)
}

// The exit status: an error wins, except that a quiet search that matched exits 0.
Status(cfg: &search.Config, problems: int, matched: int): int {
    if matched > 0 && (cfg.quiet || problems == 0) {
        return Matched
    }
    return if problems > 0 { Trouble } else { NoMatch }
}

/// The options whose values must not be negative, as they are spelled.
const Names: []str = ["--context", "--before-context", "--after-context", "--max-count", "--group", "--threads", "--max-open"]

/// An option whose value is out of range; `option` indexes Names.
error OutOfRange(option: int)

/// --color given something other than always or never.
error BadColor

type OptionError = OutOfRange | BadColor

// Writes an option error in the words of the command line.
Problem(err: mut Writer, e: OptionError): WriteError!void {
    match e {
        OutOfRange(option) {
            name := Names[option]
            try Print(err, "mgrep: {name} is out of range\n", (name))
        }
        BadColor {
            try err.Str("mgrep: --color takes always or never\n")
        }
    }
}

// The search configuration the options describe. `names` says whether file
// names are printed when no option decides it.
Configure(opts: &Options, names: bool): OptionError!search.Config {
    context := try NonNegative(opts.context, 0)
    before := try NonNegative(opts.before_context, 1)
    after := try NonNegative(opts.after_context, 2)
    max_count := try NonNegative(opts.max_count, 3)
    _ = try NonNegative(opts.group, 4)
    color := match opts.color {
        some(c) {
            if c == "always" { true } else if c == "never" { false } else { fail BadColor }
        }
        none { false }
    }
    return search.Config(
        invert: opts.invert_match,
        count: opts.count,
        count_matches: opts.count_matches,
        files_only: opts.files_with_matches,
        only_matching: opts.only_matching,
        line_number: opts.line_number || !opts.no_line_number,
        column: opts.column,
        heading: opts.heading,
        with_filename: !opts.no_filename && (opts.with_filename || opts.heading || names),
        color: color,
        before: if before > 0 { before } else { context },
        after: if after > 0 { after } else { context },
        max_count: max_count,
        replace: opts.replace,
        group: opts.group,
        quiet: opts.quiet,
    )
}

NonNegative(v: ?int, option: int): OutOfRange!int {
    n := v else { 0 }
    if n < 0 {
        fail OutOfRange(option: option)
    }
    return n
}

// How the tasks run: 4 search tasks and 8 open files unless the options say.
Tune(opts: &Options): OutOfRange!work.Tuning {
    threads := opts.threads else { 4 }
    if threads < 1 || threads > 256 {
        fail OutOfRange(option: 5)
    }
    max_open := opts.max_open else { 8 }
    if max_open < 1 {
        fail OutOfRange(option: 6)
    }
    return work.Tuning(threads: threads, max_open: max_open, binary: opts.binary, stats: opts.stats)
}

// Says which argument a parse error is about, in the words the user typed.
Explain(err: mut Writer, args: []str, ex: &Expanded, e: cli.Error): WriteError!void {
    match e {
        cli.Unknown(arg) {
            word := args[ex.origin[arg]]
            try Print(err, "mgrep: unknown option {word}\n", (word))
        }
        cli.Invalid(arg) {
            word := args[ex.origin[arg]]
            if arg > 0 {
                option := ex.args[arg - 1]
                try Print(err, "mgrep: {option} needs a number, not {word}\n", (option, word))
            } else {
                try Print(err, "mgrep: {word} needs a number\n", (word))
            }
        }
        cli.Missing(field) {
            try Print(err, "mgrep: a required option is missing (field {field})\n", (field))
        }
        OutOfMemory {
            try err.Str("mgrep: out of memory reading the options\n")
        }
        _ {
            Panic("cli.Error gained a member this match does not handle")
        }
    }
    try err.Str("try mgrep --help\n")
}

// Reports a pattern that does not compile, with a caret under the offending
// byte, placed in terminal columns so wide characters before it line up.
BadPattern(err: mut Writer, opts: &Options, patterns: []str, e: search.Error): WriteError!void {
    match e {
        search.BadPattern(index, offset) {
            p := patterns[index]
            if opts.file.IsSome() {
                n := index + 1
                try Print(err, "mgrep: pattern {n} does not parse at byte {offset}:\n", (n, offset))
            } else {
                try Print(err, "mgrep: the pattern does not parse at byte {offset}:\n", (offset))
            }
            try Print(err, "    {p}\n    ", (p))
            for i in 0..text.Width(p[..offset]) {
                _ = i
                try err.Str(" ")
            }
            try err.Str("^\n")
        }
        search.PatternTooLarge {
            try err.Str("mgrep: the pattern is too large to compile\n")
        }
        OutOfMemory {
            try err.Str("mgrep: out of memory compiling the pattern\n")
        }
    }
}
