// Why: The server modes and the client modes share setup: the database, the app
// and its log.
//
// What: Serve, Replay, Client, Batch, LoadTest, RunClient and NewApp.
//
// How: Serve runs until Ctrl-C cancels its scope. With `--embed`, RunClient
// serves the database in the same process and runs the client against it, then
// cancels the server.

import std.db.sqlite
import std.fs
import std.http
import std.log
import std.proc
import src.client
import src.store
import src.web

const DefaultAddr: str = "127.0.0.1:8080"
const DefaultLimit: int = 1048576

// serve: the API on --addr until Ctrl-C.
Serve(sys: &System, opts: &Options, level: log.Level): WriteError!int {
    path := try NeedDb(sys, opts) else { return BadUsage }
    db := store.Open(sys.arena, sys.io, path) else e {
        return try DbFailed(sys, path, e)
    }
    defer db.Close()
    app := try NewApp(sys, &db, opts, level) else { return Failed }
    addr := opts.addr else { DefaultAddr }
    var failed: ?http.Error = none
    scope s := sys.io.Scope() {
        s.go Watch(&s, sys.io)
        web.Serve(sys, addr, opts.limit else { DefaultLimit }, &app) else e {
            failed = some(e)
        }
        s.Cancel()
    }
    Finish(sys, app, opts)
    if e := failed {
        try sys.PrintErr("notes: serve {addr}: {e}\n", (addr, e))
        return Failed
    }
    return 0
}

// Cancels the server's scope on Ctrl-C.
Watch(s: &Scope, io: Io) {
    proc.WaitSignal(io, proc.Signal.Interrupt) else {
        return
    }
    s.Cancel()
}

// replay: raw requests on standard input, responses on standard output.
Replay(sys: &System, opts: &Options, level: log.Level): WriteError!int {
    path := try NeedDb(sys, opts) else { return BadUsage }
    db := store.Open(sys.arena, sys.io, path) else e {
        return try DbFailed(sys, path, e)
    }
    defer db.Close()
    app := try NewApp(sys, &db, opts, level) else { return Failed }
    var input := sys.Stdin()
    var output := sys.Stdout()
    var code := 0
    _ = web.Replay(sys.arena, sys.io, mut input, mut output, opts.limit else { DefaultLimit }, &app) else e {
        sys.PrintErr("notes: replay: {e}\n", (e)) else {}
        code = Failed
    }
    output.Flush() else {
        code = Failed
    }
    Finish(sys, app, opts)
    return code
}

// A client command from the command line.
Client(sys: &System, opts: &Options, level: log.Level): WriteError!int {
    script := [opts.args]
    return try RunClient(sys, opts, level, script[..], none)
}

// batch: client commands from standard input, one a line, `#` starting a comment.
Batch(sys: &System, opts: &Options, level: log.Level): WriteError!int {
    if opts.args.Len() != 1 {
        try sys.PrintErr("notes: batch takes its commands on standard input\n")
        return BadUsage
    }
    var input := sys.Stdin()
    var lines := List[[]str].New(sys.arena)
    var n := 0
    for {
        line := input.Line(sys.arena) else e {
            try sys.PrintErr("notes: batch: line {n}: {e}\n", (n: n + 1, e))
            return Failed
        }
        text := line else { break }
        n += 1
        words := SplitWords(sys.arena, text) else e {
            try sys.PrintErr("notes: batch: line {n}: {e}\n", (n, e))
            return BadUsage
        }
        if words.Len() > 0 {
            lines.Push(words) else {
                try sys.PrintErr("notes: batch: out of memory\n")
                return Failed
            }
        }
    }
    script := lines.Done()
    return try RunClient(sys, opts, level, script[..], none)
}

// load: concurrent clients posting notes.
LoadTest(sys: &System, opts: &Options, level: log.Level): WriteError!int {
    plan := client.Plan(clients: opts.clients else { 4 }, requests: opts.requests else { 25 })
    if plan.clients < 1 || plan.clients > 256 || plan.requests < 0 {
        try sys.PrintErr("notes: load: --clients is 1 to 256 and --requests at least 0\n")
        return BadUsage
    }
    // the empty script: `[]` is a const initializer's only
    empty := List[[]str].New(sys.arena)
    script := empty.Done()
    return try RunClient(sys, opts, level, script[..], some(plan))
}

// Runs a script or a load against --addr, serving --db in this process too
// when --embed is given, and prints what the client printed.
RunClient(sys: &System, opts: &Options, level: log.Level, script: [][]str, plan: ?client.Plan): WriteError!int {
    addr := opts.addr else { DefaultAddr }
    base := sys.arena.Fmt("http://{addr}", (addr)) else { return Failed }
    al := sys.heap.Alloc()
    var session := client.Session(base: base, out: HeapText.New(al), err: HeapText.New(al), code: 0, attempts: if opts.embed { 50 } else { 1 })
    if !opts.embed {
        if p := plan {
            client.Load(sys.heap, sys.io, mut session, p)
        } else {
            client.Script(sys.heap, sys.io, mut session, script)
        }
        return Done(sys, session)
    }
    given := NeedDb(sys, opts) else {
        session.Free(al)
        return Failed
    }
    path := given else {
        session.Free(al)
        return BadUsage
    }
    db := store.Open(sys.arena, sys.io, path) else e {
        session.Free(al)
        return try DbFailed(sys, path, e)
    }
    defer db.Close()
    made := NewApp(sys, &db, opts, level) else {
        session.Free(al)
        return Failed
    }
    app := made else {
        session.Free(al)
        return Failed
    }
    var failed: ?http.Error = none
    scope s := sys.io.Scope() {
        s.go client.Drive(&s, sys.heap, sys.io, mut session, script, plan)
        web.Serve(sys, addr, opts.limit else { DefaultLimit }, &app) else e {
            failed = some(e)
        }
        s.Cancel()
    }
    Finish(sys, app, opts)
    if e := failed {
        session.Free(al)
        try sys.PrintErr("notes: serve {addr}: {e}\n", (addr, e))
        return Failed
    }
    return Done(sys, session)
}

// Prints the session's output and errors and frees them, giving its exit
// code, or 1 when standard output fails.
Done(sys: &System, session: client.Session): int {
    client.Session(out, err, code, ..) := session
    var w := sys.Stdout()
    var failed := false
    w.Str(out.Str()) else {
        failed = true
    }
    var e := sys.Stderr()
    e.Str(err.Str()) else {}
    out.Free(sys.heap.Alloc())
    err.Free(sys.heap.Alloc())
    return if failed { Failed } else { code }
}

// The --db path, or none after a usage message.
NeedDb(sys: &System, opts: &Options): WriteError!?str from opts {
    path := opts.db else {
        try sys.PrintErr("notes: {cmd} needs --db FILE\n", (cmd: opts.args[0]))
        return none
    }
    return some(path)
}

// The app over db, with its request log in --log's file or in memory.
NewApp(sys: &System, db: &sqlite.Db, opts: &Options, level: log.Level): WriteError!?web.App from db, sys {
    al := sys.heap.Alloc()
    path := opts.log else {
        return some(web.App(store: store.Store.Of(db), heap: sys.heap, log: web.AccessLog.InMemory(al, level)))
    }
    f := OpenLog(sys.io, path) else e {
        try sys.PrintErr("notes: --log {path}: {e}\n", (path, e))
        return none
    }
    return some(web.App(store: store.Store.Of(db), heap: sys.heap, log: web.AccessLog.ToFile(f, level)))
}

// Creates the log file, replacing an old one: std.fs opens no file for appending.
OpenLog(io: Io, path: str): fs.Error!fs.File {
    return try fs.Create(io, path)
}

// Releases the app and prints its in-memory log to standard error. Nothing
// here fails the command: a log line that cannot be printed is dropped.
Finish(sys: &System, app: web.App, opts: &Options) {
    r := app.Close(sys.arena, sys.io)
    if !r.closed {
        sys.PrintErr("notes: closing the request log failed\n") else {}
    }
    if !opts.quiet {
        var w := sys.Stderr()
        w.Str(r.lines) else {}
    }
}

// Reports a database that cannot be opened: 3 when it is locked, else 1.
DbFailed(sys: &System, path: str, e: sqlite.Error): WriteError!int {
    match e {
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
