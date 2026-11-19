// Why: Every request reads or writes notes; the SQL lives in one place.
//
// What: Open, Store with Create, Get, Update, Delete, List, Stats and Insert,
// Query, and the errors Conflict and Error.
//
// How: One row per note; tags are kept sorted and joined by spaces, so a note
// and its tags change in one statement. A unique key on the normalised title
// (normtext) turns a duplicate title into Conflict.

import std.db.sqlite
import src.notes
import src.store.cext

/// A second note whose title normalises to one already stored.
export error Conflict

/// Why a store operation failed: a conflict, or what sqlite reported.
export type Error = Conflict | sqlite.Error

// SQLite's primary result code for a violated constraint (SQLITE_CONSTRAINT); the
// code an error carries is the extended one, whose low byte is the primary.
const ConstraintCode: i32 = 19

// One row per note. `tags` holds the sorted tags joined by single spaces, so a
// note and its tags change in one statement and need no transaction on the
// shared connection. `key` is the normalised title, which is unique.
const Schema: str = """
    create table if not exists notes (
        id integer primary key,
        title text not null,
        body text not null,
        tags text not null default '',
        key text not null unique,
        rev integer not null default 1
    )
    """

/// Opens or creates the database at `path` with the notes table, after
/// registering `normtext` so the connection has it. Fails with `Misuse` when
/// the function cannot be registered. Allocates the connection in `a`.
export Open(a: &Arena, io: Io, path: str): sqlite.Error!sqlite.Db {
    _ = cext.Register() else { fail sqlite.Misuse }
    db := try sqlite.Open(a, io, path)
    onerr db.Close()
    try db.Exec(io, Schema)
    return db
}

/// A query over the notes: `text` matches the normalised title or body, `tag`
/// one tag exactly; an empty string matches everything.
export struct Query {
    export text: str
    export tag: str
    export limit: int
    export offset: int
}

/// The notes of one database. Every method takes the connection by
/// reference, so one `Store` serves every task of a server.
export view struct Store {
    db: &sqlite.Db
}

/// A store over `db`.
export Store.Of(db: &sqlite.Db): Store {
    return Store(db: db)
}

/// Stores a new note with revision 1. Fails with `Conflict` when another
/// note's title normalises to the same text. One statement; the result lives
/// in `a`.
export Store.Create(&self, a: &Arena, io: Io, n: &notes.NoteIn): Error!notes.Note from a {
    tags := try JoinTags(a, n.tags)
    var st := try self.db.Prepare(a, io, "insert into notes (title, body, tags, key) values (?1, ?2, ?3, normtext(?1)) returning id, title, body, tags, rev")
    defer st.Close()
    try st.BindText(1, n.title)
    try st.BindText(2, n.body)
    try st.BindText(3, tags)
    found := st.Step(io) else e { fail Classify(e) }
    if !found {
        fail sqlite.Misuse
    }
    return try Row(a, &st)
}

/// The note with `id`, or none. One indexed lookup; the result lives in `a`.
export Store.Get(&self, a: &Arena, io: Io, id: i64): sqlite.Error!?notes.Note from a {
    var st := try self.db.Prepare(a, io, "select id, title, body, tags, rev from notes where id = ?1")
    defer st.Close()
    try st.BindInt(1, id)
    if !try st.Step(io) {
        return none
    }
    return some(try Row(a, &st))
}

/// Replaces the note with `id` and raises its revision, or gives none when
/// there is no such note. Fails with `Conflict` as `Create` does.
export Store.Update(&self, a: &Arena, io: Io, id: i64, n: &notes.NoteIn): Error!?notes.Note from a {
    tags := try JoinTags(a, n.tags)
    var st := try self.db.Prepare(a, io, "update notes set title = ?2, body = ?3, tags = ?4, key = normtext(?2), rev = rev + 1 where id = ?1 returning id, title, body, tags, rev")
    defer st.Close()
    try st.BindInt(1, id)
    try st.BindText(2, n.title)
    try st.BindText(3, n.body)
    try st.BindText(4, tags)
    found := st.Step(io) else e { fail Classify(e) }
    if !found {
        return none
    }
    return some(try Row(a, &st))
}

/// Deletes the note with `id`; false when there was none.
export Store.Delete(&self, a: &Arena, io: Io, id: i64): sqlite.Error!bool {
    var st := try self.db.Prepare(a, io, "delete from notes where id = ?1 returning id")
    defer st.Close()
    try st.BindInt(1, id)
    return try st.Step(io)
}

/// The notes `q` matches, in id order, one page of them, with the count of
/// all matches. Two statements; O(n) in the matches; the page lives in `a`.
export Store.List(&self, a: &Arena, io: Io, q: &Query): sqlite.Error!notes.NoteList from a {
    var count := try self.db.Prepare(a, io, "select count(*) from notes where (?1 = '' or instr(key, normtext(?1)) > 0 or instr(normtext(body), normtext(?1)) > 0) and (?2 = '' or instr(' ' || tags || ' ', ' ' || ?2 || ' ') > 0)")
    defer count.Close()
    try count.BindText(1, q.text)
    try count.BindText(2, q.tag)
    var total := 0
    if try count.Step(io) {
        total = Cast[int](count.Int(0))
    }
    var st := try self.db.Prepare(a, io, "select id, title, body, tags, rev from notes where (?1 = '' or instr(key, normtext(?1)) > 0 or instr(normtext(body), normtext(?1)) > 0) and (?2 = '' or instr(' ' || tags || ' ', ' ' || ?2 || ' ') > 0) order by id limit ?3 offset ?4")
    defer st.Close()
    try st.BindText(1, q.text)
    try st.BindText(2, q.tag)
    try st.BindInt(3, i64(q.limit))
    try st.BindInt(4, i64(q.offset))
    var out := List[notes.Note].New(a)
    for try st.Step(io) {
        try out.Push(try Row(a, &st))
    }
    page := out.Done()
    return notes.NoteList(notes: page[..], total: total)
}

/// Counts notes, distinct tags and the words of every body. One pass over the
/// table; scratch lives in a sub-arena of `a`.
export Store.Stats(&self, a: &Arena, io: Io): sqlite.Error!notes.Stats {
    var scratch := try a.Sub()
    defer scratch.Free()
    var st := try self.db.Prepare(&scratch, io, "select body, tags from notes")
    defer st.Close()
    var tags := Set[str].New(&scratch)
    var count := 0
    var words := 0
    // Set has no Len, so the new members are counted as they are added.
    var distinct := 0
    for try st.Step(io) {
        count += 1
        for _ in st.Str(0).Words() {
            words += 1
        }
        for tag in st.Str(1).Words() {
            if try tags.Add(try scratch.Dup(tag)) {
                distinct += 1
            }
        }
    }
    avg := if count == 0 { 0.0 } else { Cast[f64](words) / Cast[f64](count) }
    return notes.Stats(notes: count, tags: distinct, words: words, avg_words: avg)
}

/// Inserts every note of `batch` in one transaction, reusing one statement,
/// and returns how many were stored; on any failure nothing is stored. Use it
/// only on a connection no other task shares, since the transaction spans
/// statements.
export Store.Insert(&self, a: &Arena, io: Io, batch: []notes.NoteIn): Error!int {
    try self.db.Exec(io, "begin immediate")
    onerr self.db.Exec(io, "rollback") else {}
    var st := try self.db.Prepare(a, io, "insert into notes (title, body, tags, key) values (?1, ?2, ?3, normtext(?1))")
    defer st.Close()
    for n in &batch {
        try st.BindText(1, n.title)
        try st.BindText(2, n.body)
        try st.BindText(3, try JoinTags(a, n.tags))
        _ = st.Step(io) else e { fail Classify(e) }
        st.Reset()
    }
    try self.db.Exec(io, "commit")
    return batch.Len()
}

// The conflict a constraint violation means here, else the error itself.
Classify(e: sqlite.Error): Error {
    return match e {
        sqlite.Sqlite(code) {
            // the code is sqlite's extended one; its low byte is the primary
            if code & 0xFF == ConstraintCode { Conflict } else { e }
        }
        sqlite.Busy | sqlite.Misuse | OutOfMemory | Canceled { e }
        _ {
            Panic("sqlite.Error gained a member this match does not handle")
        }
    }
}

// Reads the row st is on, as id, title, body, tags, rev, copying its text into a, since
// Stmt.Str is valid only until the next Step.
Row(a: &Arena, st: &sqlite.Stmt): OutOfMemory!notes.Note from a {
    var tags := List[str].New(a)
    for tag in st.Str(3).Words() {
        try tags.Push(try a.Dup(tag))
    }
    done := tags.Done()
    return notes.Note(id: st.Int(0), title: try a.Dup(st.Str(1)), body: try a.Dup(st.Str(2)), tags: done[..], rev: st.Int(4))
}

// The tags sorted, without repeats, joined by single spaces.
JoinTags(a: &Arena, tags: ?[]str): OutOfMemory!str from a {
    given := tags else { return "" }
    var sorted := List[str].New(a)
    for tag in given {
        // a copy: a list in `a` that held the caller's views would store them into `a`
        try sorted.Push(try a.Dup(tag))
    }
    sorted.Sort(fn(x: &str, y: &str): bool { return *x < *y })
    var out := Text.New(a)
    for i, tag in sorted {
        if i > 0 && tag == sorted[i - 1] {
            continue
        }
        if out.Str().Len() > 0 {
            try out.Push(" ")
        }
        try out.Push(tag)
    }
    return out.Done()
}
