// Why: The client's commands turn words on a command line into requests and
// print the replies.
//
// What: Session, Usage, the help text Commands, and Session.Run with its
// printers.
//
// How: A Session collects output, error output and the exit code on the heap,
// so a task can fill it for Main; a refused request prints the server's message
// and sets exit code 4.

import std.json
import src.notes

/// A client run: where it sends requests, what it has printed, and the exit
/// code so far. It lives on the heap, so a task can fill it for `Main`.
export struct Session {
    export base: str
    export out: HeapText
    export err: HeapText
    export code: int
    /// How many times a refused connection is tried, 20 ms apart: above 1
    /// only for a server in this process that may still be starting.
    export attempts: int
}

/// A command the client does not know, or one with missing or bad arguments.
export error Usage(word: int)

/// The commands `Run` takes, for the help text.
export const Commands: str = """
      health                     ask whether the server is up
      version                    the server's sqlite version and normalizer
      add TITLE [BODY] [TAG...]  create a note
      get ID                     show one note
      list [q=T] [tag=T] [limit=N] [offset=N]
      search TEXT                notes whose title or body contains TEXT, ignoring case and spacing
      edit ID FIELD=VALUE...     change title, body or tags (tags=a,b; tags= clears them)
      rm ID                      delete a note
      stats                      counts over every note
      raw METHOD PATH [BODY]     send any request and print the reply as it came
    """

/// Appends `text` to the session's output.
export Session.Say(mut self, al: &Alloc, text: str): OutOfMemory!void {
    try self.out.Push(al, text)
}

/// Appends `text` to the session's error output.
export Session.Complain(mut self, al: &Alloc, text: str): OutOfMemory!void {
    try self.err.Push(al, text)
}

/// Runs one client command, `words[0]` being its name, and prints its result
/// into the session. A refused request prints the server's message and sets
/// the exit code to 4; a server that cannot be reached is the error.
export Session.Run(mut self, a: &Arena into a, io: Io, al: &Alloc, words: []str into a): Usage|Error!void *words into a {
    if words.Len() == 0 {
        fail Usage(word: 0)
    }
    cmd := words[0]
    args := words[1..]
    match cmd {
        "health" {
            r := try Call(a, io, self.base, "GET", "/health", "", self.attempts)
            try self.Show(a, al, &r, "up\n")
        }
        "version" {
            r := try Call(a, io, self.base, "GET", "/version", "", self.attempts)
            try self.Version(a, al, &r)
        }
        "add" {
            if args.Len() == 0 {
                fail Usage(word: 1)
            }
            // no tags is the empty slice args[0..0]: `[]` is a const initializer's only
            body := try NoteJson(a, args[0], if args.Len() > 1 { args[1] } else { "" }, if args.Len() > 2 { args[2..] } else { args[0..0] })
            r := try Call(a, io, self.base, "POST", "/notes", body, self.attempts)
            try self.ShowNote(a, al, &r, "created")
        }
        "get" {
            id := try IdArg(args)
            path := try a.Fmt("/notes/{id}", (id))
            r := try Call(a, io, self.base, "GET", path, "", self.attempts)
            try self.ShowNote(a, al, &r, "")
        }
        "rm" {
            id := try IdArg(args)
            path := try a.Fmt("/notes/{id}", (id))
            r := try Call(a, io, self.base, "DELETE", path, "", self.attempts)
            text := try a.Fmt("deleted #{id}\n", (id))
            try self.Show(a, al, &r, text)
        }
        "edit" {
            id := try IdArg(args)
            if args.Len() < 2 {
                fail Usage(word: 2)
            }
            body := try PatchJson(a, args[1..])
            path := try a.Fmt("/notes/{id}", (id))
            r := try Call(a, io, self.base, "PATCH", path, body, self.attempts)
            try self.ShowNote(a, al, &r, "updated")
        }
        "list" {
            path := try ListPath(a, args)
            r := try Call(a, io, self.base, "GET", path, "", self.attempts)
            try self.ShowList(a, al, &r)
        }
        "search" {
            if args.Len() != 1 {
                fail Usage(word: 1)
            }
            q := try a.Fmt("q={t}", (t: args[0]))
            pair := [q]
            path := try ListPath(a, pair[..])
            r := try Call(a, io, self.base, "GET", path, "", self.attempts)
            try self.ShowList(a, al, &r)
        }
        "stats" {
            r := try Call(a, io, self.base, "GET", "/stats", "", self.attempts)
            try self.Stats(a, al, &r)
        }
        "raw" {
            if args.Len() < 2 {
                fail Usage(word: 1)
            }
            body := if args.Len() > 2 { args[2] } else { "" }
            r := try Call(a, io, self.base, args[0], args[1], body, self.attempts)
            zip := if r.gzipped { " (gzip)" } else { "" }
            text := try a.Fmt("{status}{zip} {body}\n", (status: r.status, zip, body: r.body))
            try self.Say(al, text)
        }
        _ {
            fail Usage(word: 0)
        }
    }
}

// Prints ok on a 2xx reply, else the server's message with exit code 4.
Session.Show(mut self, a: &Arena, al: &Alloc, r: &Answer, ok: str): OutOfMemory!void *r into a {
    if !try self.Refused(a, al, r) {
        try self.Say(al, ok)
    }
}

// Whether r is an error reply, which it then prints.
Session.Refused(mut self, a: &Arena, al: &Alloc, r: &Answer): OutOfMemory!bool *r into a {
    if r.status < 400 {
        return false
    }
    text := try a.Fmt("notes: error {status}: {m}\n", (status: r.status, m: ProblemText(a, r.body)))
    try self.Complain(al, text)
    self.code = 4
    return true
}

// Prints the note in r's body after verb, or the refusal.
Session.ShowNote(mut self, a: &Arena, al: &Alloc, r: &Answer, verb: str): OutOfMemory!void *r into a {
    if try self.Refused(a, al, r) {
        return
    }
    v := json.Parse(a, r.body) else e {
        try self.Malformed(a, al, e)
        return
    }
    n := notes.Note.FromJson(a, &v) else e {
        try self.Malformed(a, al, e)
        return
    }
    if verb.Len() > 0 {
        text := try a.Fmt("{verb} ", (verb))
        try self.Say(al, text)
    }
    try self.Say(al, try Line(a, &n))
    if n.body.Len() > 0 {
        text := try a.Fmt("  {body}\n", (body: n.body))
        try self.Say(al, text)
    }
}

// Prints one line per note of a list reply, then the count.
Session.ShowList(mut self, a: &Arena, al: &Alloc, r: &Answer): OutOfMemory!void *r into a {
    if try self.Refused(a, al, r) {
        return
    }
    v := json.Parse(a, r.body) else e {
        try self.Malformed(a, al, e)
        return
    }
    page := notes.NoteList.FromJson(a, &v) else e {
        try self.Malformed(a, al, e)
        return
    }
    for n in &page.notes {
        try self.Say(al, try Line(a, n))
    }
    shown := page.notes.Len()
    all := page.total
    zip := if r.gzipped { ", gzip" } else { "" }
    text := try a.Fmt("{shown} of {all} notes{zip}\n", (shown, all, zip))
    try self.Say(al, text)
}

// Prints the stats reply, read member by member.
Session.Stats(mut self, a: &Arena, al: &Alloc, r: &Answer): OutOfMemory!void *r into a {
    if try self.Refused(a, al, r) {
        return
    }
    v := json.Parse(a, r.body) else e {
        try self.Malformed(a, al, e)
        return
    }
    count := v.Get("notes") else { return }
    tags := v.Get("tags") else { return }
    words := v.Get("words") else { return }
    avg := v.Get("avg_words") else { return }
    n := count.Int() else { -1 }
    t := tags.Int() else { -1 }
    w := words.Int() else { -1 }
    mean := avg.Float() else { 0.0 }
    text := try a.Fmt("{n} notes, {t} tags, {w} words, {mean:.1} words a note\n", (n, t, w, mean))
    try self.Say(al, text)
}

// Prints the version reply.
Session.Version(mut self, a: &Arena, al: &Alloc, r: &Answer): OutOfMemory!void *r into a {
    if try self.Refused(a, al, r) {
        return
    }
    v := json.Parse(a, r.body) else e {
        try self.Malformed(a, al, e)
        return
    }
    s := v.Get("sqlite") else { return }
    n := v.Get("normalizer") else { return }
    text := try a.Fmt("sqlite {s}, normalizer {n}\n", (s: s.Str() else { "?" }, n: n.Str() else { "?" }))
    try self.Say(al, text)
}

// Reports a reply the client could not read, with exit code 1.
Session.Malformed(mut self, a: &Arena, al: &Alloc, e: json.Error): OutOfMemory!void {
    text := try a.Fmt("notes: the server's reply is not what the client expects: {e}\n", (e))
    try self.Complain(al, text)
    self.code = 1
}

// One line for a note: #id title [tags] (rev N).
Line(a: &Arena, n: &notes.Note): OutOfMemory!str from a {
    var t := Text.New(a)
    try t.Push(try a.Fmt("#{id} {title}", (id: n.id, title: n.title)))
    if n.tags.Len() > 0 {
        try t.Push(" [")
        for i, tag in n.tags {
            if i > 0 {
                try t.Push(" ")
            }
            try t.Push(tag)
        }
        try t.Push("]")
    }
    try t.Push(try a.Fmt(" (rev {rev})\n", (rev: n.rev)))
    return t.Done()
}

// The note id argument.
IdArg(args: []str): Usage!i64 {
    if args.Len() == 0 {
        fail Usage(word: 1)
    }
    return args[0].ParseInt() else { fail Usage(word: 1) }
}

impl Free for Session {
    Free(self, a: &Alloc) {
        Session(out, err, ..) := self
        out.Free(a)
        err.Free(a)
    }
}
