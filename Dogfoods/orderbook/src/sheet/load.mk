// Why: a session can start from the book a previous session closed with.
// What: Opening and LoadBook.
// How: reads a book file as WriteBook writes one and turns each row into the
// command that rebuilds its order, keeping each order's tag.

import std.csv
import std.fs
import src.book

/// What a session starts from: the commands that put the opening book's
/// orders back, in the file's order, and each tagged order's tag.
export struct Opening {
    export commands: List[book.Command]
    export tags: Map[book.ClientId, str]
}

/// A session with no opening book. O(1); allocates nothing.
export Opening.Empty(a: &Arena): Opening from a {
    return Opening(commands: List[book.Command].New(a), tags: Map[book.ClientId, str].New(a))
}

/// The tag of the order `id`, or "" when it has none. O(1) expected.
export Opening.Tag(&self, id: &book.ClientId): str from self {
    t := self.tags.Get(id) else {
        return ""
    }
    return *t
}

/// Reads the book file `path`: the header, then one order a row. Fails
/// with fs.Error when the file is missing, a directory, or cannot be
/// opened or closed, csv.Error when it is not CSV within BookOptions'
/// limits or cannot be read, BookError at the first row that is not a
/// resting order, and OutOfMemory. O(file bytes); allocates the commands,
/// the tags and a 128 KiB scratch.
export LoadBook(a: &Arena, io: Io, path: str): LoadError!Opening from a {
    info := try fs.Stat(io, path)
    if info.kind == fs.Kind.Dir { // a directory opens; say why it cannot be read
        fail fs.IsDirectory
    }
    var opening := Opening.Empty(a)
    var scratch := try a.Sub()
    defer scratch.Free()
    rbuf := try scratch.Slice[u8](64 * 1024)
    cbuf := try scratch.Slice[u8](64 * 1024)
    o := BookOptions()
    var f := try fs.Open(io, path)
    onerr f.Abandon()
    var r := f.Reader(io, rbuf)
    var records := csv.Records.New(mut r, cbuf, &o)
    first := try records.Next() else {
        fail BadHeader(line: 1)
    }
    try CheckHeader(&first)
    for {
        next := try records.Next()
        rec := next else {
            break
        }
        line := rec.Line()
        if rec.Len() != Columns {
            fail RowLength(line: line, got: rec.Len())
        }
        var rowa := try scratch.Sub()
        defer rowa.Free()
        row := OrderRow.FromCsv(&rowa, &rec) else e {
            match e {
                OutOfMemory { fail OutOfMemory }
                Utf8Error { fail BadText(line: line) }
                ParseError { fail BadNumber(line: line) }
            }
        }
        id := try IdOf(&row, line)
        cmd := try CommandOf(&row, id, line)
        if row.tag.Len() > 0 && !opening.tags.Has(&id) {
            try opening.tags.Set(id, try a.Dup(row.tag))
        }
        try opening.commands.Push(cmd)
    }
    try f.Close(io)
    return opening
}
