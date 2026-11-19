// Why: The offline commands work on the database file directly, without the
// server.
//
// What: Import, Export, Backup, Version, StoreFailed, and SplitWords for batch
// lines.
//
// How: Import stores a whole JSON array in one transaction or nothing. Backup
// reads the source holding its write lock, so no writer changes it meanwhile.

import std.db.sqlite
import std.fs
import std.json
import std.log
import src.notes
import src.store
import src.store.cext

/// A batch line whose quote is not closed.
error OpenQuote(column: int)

// import FILE: every entry of a JSON array, in one transaction, or none.
Import(sys: &System, opts: &Options, level: log.Level): WriteError!int {
    path := try NeedDb(sys, opts) else { return BadUsage }
    if opts.args.Len() != 2 {
        try sys.PrintErr("notes: import takes one FILE\n")
        return BadUsage
    }
    file := opts.args[1]
    var errw := sys.Stderr()
    var l := log.Logger.New(mut errw, level)
    // the file, its value and the batch live in an arena of their own: the
    // value holds views of the text, and the batch views of the value
    var arena := Arena.New(sys.heap) else {
        try l.Error("out of memory")
        return Failed
    }
    defer arena.Free()
    a := &arena
    text := fs.ReadText(a, sys.io, file) else e {
        try l.Error("{file}: {e}", (file, e))
        return Failed
    }
    v := json.Parse(a, text) else e {
        try l.Error("{file}: not JSON: {e}", (file, e))
        return Failed
    }
    var batch := List[notes.NoteIn].New(a)
    match &v {
        .Array(items) {
            for i, item in &items {
                n := notes.FromEntry(a, item) else e {
                    try l.Error("{file}: entry {i}: {e}", (file, i, e))
                    return Failed
                }
                notes.Validate(&n) else e {
                    try l.Error("{file}: entry {i}: {m}", (file, i, m: notes.Explain(e)))
                    return Failed
                }
                if n.body.Len() == 0 {
                    try l.Warn("{file}: entry {i} has no body", (file, i))
                }
                try l.Debug("{file}: entry {i}: {t}", (file, i, t: n.title))
                batch.Push(n) else {
                    try l.Error("out of memory")
                    return Failed
                }
            }
        }
        _ {
            try l.Error("{file}: not a JSON array", (file))
            return Failed
        }
    }
    all := batch.Done()
    return try Store(sys, sys.io, mut l, path, file, all[..])
}

// Stores an import's notes in one transaction through io.
Store(sys: &System, io: Io, l: mut log.Logger, path: str, file: str, all: []notes.NoteIn): WriteError!int {
    db := store.Open(sys.arena, io, path) else e {
        return try DbFailed(sys, path, e)
    }
    defer db.Close()
    s := store.Store.Of(&db)
    count := s.Insert(sys.arena, io, all) else e {
        return try StoreFailed(sys, path, e)
    }
    try l.Info("{file}: imported {count} notes", (file, count))
    return 0
}

// export: every note, as a JSON array with one note a line.
Export(sys: &System, opts: &Options): WriteError!int {
    path := try NeedDb(sys, opts) else { return BadUsage }
    db := store.Open(sys.arena, sys.io, path) else e {
        return try DbFailed(sys, path, e)
    }
    defer db.Close()
    s := store.Store.Of(&db)
    q := store.Query(text: "", tag: "", limit: -1, offset: 0)
    page := s.List(sys.arena, sys.io, &q) else e {
        return try StoreFailed(sys, path, e)
    }
    var out := sys.Stdout()
    try out.Str("[")
    for i, n in &page.notes {
        try out.Str(if i == 0 { "\n" } else { ",\n" })
        try n.FormatJson(mut out)
    }
    try out.Str("\n]\n")
    return 0
}

// backup: every note of --db copied into --to in one write transaction. The
// source is read holding its write lock, so no writer changes it meanwhile;
// a destination that is the source itself is then locked (exit 3).
Backup(sys: &System, opts: &Options): WriteError!int {
    path := try NeedDb(sys, opts) else { return BadUsage }
    to := opts.to else {
        try sys.PrintErr("notes: backup needs --to FILE\n")
        return BadUsage
    }
    src := store.Open(sys.arena, sys.io, path) else e {
        return try DbFailed(sys, path, e)
    }
    defer src.Close()
    dst := store.Open(sys.arena, sys.io, to) else e {
        return try DbFailed(sys, to, e)
    }
    defer dst.Close()
    src.Exec(sys.io, "begin immediate") else e {
        return try DbFailed(sys, path, e)
    }
    from := store.Store.Of(&src)
    q := store.Query(text: "", tag: "", limit: -1, offset: 0)
    page := from.List(sys.arena, sys.io, &q) else e {
        src.Exec(sys.io, "rollback") else {}
        return try StoreFailed(sys, path, e)
    }
    var batch := List[notes.NoteIn].New(sys.arena)
    for n in &page.notes {
        batch.Push(notes.NoteIn(title: n.title, body: n.body, tags: some(n.tags))) else {
            try sys.PrintErr("notes: backup: out of memory\n")
            return Failed
        }
    }
    all := batch.Done()
    into := store.Store.Of(&dst)
    count := into.Insert(sys.arena, sys.io, all[..]) else e {
        src.Exec(sys.io, "rollback") else {}
        return try StoreFailed(sys, to, e)
    }
    src.Exec(sys.io, "commit") else {}
    try sys.Print("copied {count} notes into {to}\n", (count, to))
    return 0
}

// version: this program's and sqlite's versions, read through C.
Version(sys: &System): WriteError!int {
    name := cext.Register() else {
        try sys.PrintErr("notes: sqlite refused the normtext function\n")
        return Failed
    }
    try sys.Print("notes 1.0\nsqlite {v}\nnormalizer {name}\n", (v: cext.SqliteVersion(), name))
    return 0
}

// Reports a store failure: 3 for a locked database, else 1.
StoreFailed(sys: &System, path: str, e: store.Error): WriteError!int {
    match e {
        store.Conflict {
            try sys.PrintErr("notes: {path}: two notes have the same title\n", (path))
            return Failed
        }
        sqlite.Busy {
            try sys.PrintErr("notes: {path}: the database is locked\n", (path))
            return Locked
        }
        _ {
            try sys.PrintErr("notes: {path}: {e}\n", (path, e))
            return Failed
        }
    }
}

/// Splits a batch line into words at spaces and tabs, as a shell does: a
/// double-quoted part, with `\"` and `\\` escapes inside it, may hold spaces
/// and may stand anywhere in a word. A line that starts with `#` is a comment
/// and has no words. Fails with `OpenQuote` at an unclosed quote. The words
/// live in `a`.
SplitWords(a: &Arena, line: str): OpenQuote|OutOfMemory![]str from a {
    var words := List[str].New(a)
    if line.Trim().StartsWith("#") {
        done := words.Done()
        return done[..]
    }
    var word := Text.New(a)
    var inside := false
    var quoted := false
    var open := 0
    var i := 0
    for i < line.Len() {
        b := line.Byte(i)
        if !quoted && (b == b' ' || b == b'\t') {
            if inside {
                try words.Push(try a.Dup(word.Str()))
                word.Clear()
                inside = false
            }
            i += 1
            continue
        }
        inside = true
        if b == b'"' {
            quoted = !quoted
            open = i
            i += 1
            continue
        }
        if quoted && b == b'\\' && i + 1 < line.Len() {
            i += 1
        }
        c := line.Byte(i)
        width := if c < 0x80 { 1 } else if c < 0xE0 { 2 } else if c < 0xF0 { 3 } else { 4 }
        try word.Push(line[i..i + width])
        i += width
    }
    if quoted {
        fail OpenQuote(column: open + 1)
    }
    if inside {
        try words.Push(try a.Dup(word.Str()))
    }
    done := words.Done()
    return done[..]
}

test "SplitWords keeps a quoted part with spaces as one word" (sys: &System) {
    words := try SplitWords(sys.arena, "edit 2 body=\"two words\" x")
    Assert(words.Len() == 4)
    Assert(words[2] == "body=two words")
}

test "SplitWords skips a comment line" (sys: &System) {
    words := try SplitWords(sys.arena, "  # nothing here")
    Assert(words.Len() == 0)
}

test "SplitWords reports an unclosed quote" (sys: &System) {
    _ = SplitWords(sys.arena, "add \"open") else {
        return
    }
    Assert(false)
}
