# std.db.sqlite

`std.db.sqlite` is an embedded SQL database: SQLite's own C source, compiled into your program, behind a small safe API of linear handles and one error set. Reach for it when a program needs tables, queries and transactions in a single file, without a database server. For a plain key-value file, `std.fs` may be enough.

Read the [library guide](README.md) first.

```mk
// verdict: parse
import std.db.sqlite
```

## Contents

- [The main pieces](#the-main-pieces)
- [The package manifest](#the-package-manifest)
- [Connections and statements](#connections-and-statements)
- [Errors](#errors)
- [Files, locks and processes](#files-locks-and-processes)
- [Tasks and cancellation](#tasks-and-cancellation)
- [Memory and cost](#memory-and-cost)
- [Examples](#examples): [create a table and query it](#create-a-table-and-query-it), [handle a constraint failure](#handle-a-constraint-failure), [keep rows past the statement](#keep-rows-past-the-statement), [two connections to one file](#two-connections-to-one-file)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Open or create a database | `Open(a, io, path)`; `":memory:"` or `""` for one in memory |
| Run SQL that returns no rows | `Db.Exec(io, sql)`, one or more statements |
| Prepare a statement | `Db.Prepare(a, io, sql)`, for the first statement of `sql`; it returns a `Stmt` |
| Bind parameters `?1`, `?2`, ... | `Stmt.BindInt(i, v)`, `Stmt.BindText(i, s)` |
| Step through rows | `Stmt.Step(io)`: `true` with a row, `false` when done |
| Read a column | `Stmt.Int(col)`, `Stmt.Str(col)` |
| Run a statement again | `Stmt.Reset()` |
| End a statement or a connection | `Stmt.Close()`, `Db.Close()` |

## The package manifest

`std.db.sqlite` compiles SQLite's amalgamation, `sqlite3.c`, as part of your package, and links it against `std.libc`, a C library written in Mk. So the package that imports the module declares the C source and the `sqlite3` C import in its `pkg.mk`; without them the import is E0202 (`reason: needs-cimport`). The module needs these flags:

```mk
// verdict: parse
const Package = pkg.Package(
    name: "shop",
    mk: "0.1",
    bins: [pkg.Bin(name: "shop", root: "src", subsystem: "console")],
    c_sources: [
        pkg.CSource(path: "lib/sqlite3.c", flags: ["-DSQLITE_OS_OTHER=1", "-DSQLITE_THREADSAFE=0", "-DSQLITE_OMIT_LOCALTIME", "-DSQLITE_DEFAULT_MEMSTATUS=0", "-DSQLITE_TEMP_STORE=3", "-DSQLITE_WITHOUT_MSIZE", "-DSQLITE_DISABLE_INTRINSIC", "-DSQLITE_OMIT_SEH"]),
    ],
    c_imports: [
        pkg.CImport(name: "sqlite3", headers: ["lib/sqlite3.h"], defines: [], macros: [], vendor: false),
    ],
)
```

- Put `sqlite3.c` and `sqlite3.h` from SQLite's amalgamation download in `lib/`. Mk does not ship them.
- `SQLITE_OS_OTHER` leaves the file layer to the embedder: the module supplies its own, which works through std's system layer instead of a C runtime. `SQLITE_THREADSAFE=0` builds SQLite without mutexes; the module locks for it.
- The last three flags matter on Windows and do no harm elsewhere. Add `-DSQLITE_MALLOCSIZE=malloc_usable_size` to save sqlite's 8-byte size prefix on every allocation.
- [Packages](../packages.md#2-the-manifest) describes the manifest, and the reference the [C sources](../reference.md#c-sources-and-assembly-files) and [C imports](../reference.md#c-imports).

## Connections and statements

A `Db` is an open connection, and a `Stmt` one prepared statement of it. Both are **linear**: the compiler rejects a path through your code that drops one without `Close`, so a `try` cannot leak a handle. Neither `Close` can fail, so `defer x.Close()` right after the open is the usual pattern.

- A `Stmt` borrows its `Db` and the arena given to `Prepare`, so the compiler keeps the `Db` open while a statement lives. Close statements before their connection; deferred closes run in that order.
- `Stmt.Step` runs the statement to its next row. For a statement with no result rows, such as an `insert`, the first `Step` runs it and returns `false`.
- `Stmt.Reset` rewinds a statement to run again, with its bindings kept.
- Parameters count from 1 and columns from 0. `BindText` copies the text, so the string need not outlive the statement.
- `Stmt.Str` returns sqlite's own bytes, which stay valid until the next `Step`, `Reset` or `Close`. The compiler enforces this: the result borrows the `Stmt`; `Step` and `Reset` take it `mut` and `Close` ends it. Copy the text into an arena (`a.Dup(st.Str(i))`) to keep it.
- A NULL, a column past the end, or a call with no current row reads as `0` from `Int` and `""` from `Str`. So does text that is not UTF-8 in `Str`; read such a column with SQL's `length()` or `cast(x as blob)`.
- Only integers and text bind and read. A REAL column reads through `Int` as sqlite converts it (2.7 reads as 2). Select `x is null` as a column of its own to tell NULL from 0 or `""`, and read a float through `Str`, which gives its text.
- `Db.Exec` runs every statement of its text in order and discards their rows. It stops at the first failure; the statements before it stay run.

## Errors

Every call that can fail fails with a member of `sqlite.Error`:

| Member | Meaning |
|---|---|
| `Sqlite(code)` | Any sqlite failure without a member of its own, with sqlite's extended result code |
| `Busy` | Another connection of this process holds a lock the call needs (`SQLITE_BUSY`); [retry in your own loop](#files-locks-and-processes) |
| `Misuse` | The call broke sqlite's rules for its interface (`SQLITE_MISUSE`), or `Prepare` got no statement |
| `OutOfMemory` | sqlite could not allocate (`SQLITE_NOMEM`) |
| `Canceled` | The call's scope was cancelled (`SQLITE_INTERRUPT` maps here too) |

`Sqlite(code)` holds the extended code. A second insert of a key in a `unique` column is `Sqlite(code: 2067)`, `SQLITE_CONSTRAINT_UNIQUE`, and a duplicate primary key is `Sqlite(code: 1555)`. The low byte, `code & 0xFF`, is the primary code: 19, `SQLITE_CONSTRAINT`, for both. A syntax error is `Sqlite(code: 1)`. `SQLITE_LOCKED` (6) is a `Sqlite` too, not `Busy`. It is a conflict inside one connection, such as a table dropped while one of its statements runs, and waiting does not clear it.

`Open` fails with `Sqlite(code: 14)`, `SQLITE_CANTOPEN`, for a file it cannot open or a path that holds a NUL byte. Text over 2^31 - 2 bytes is `Sqlite(code: 18)`, `SQLITE_TOOBIG`, and a parameter index out of range `Sqlite(code: 25)`, `SQLITE_RANGE`. `sqlite.Error` is open, so a `match` on it outside std keeps a `_` arm.

## Files, locks and processes

- `Open` opens an existing file for reading and writing, and creates a missing one.
- sqlite reads and writes the file through the OS, on the calling thread, never through the `Io` a call takes. That `Io` serves cancellation and the wait for the module's lock. Under a `Sim`'s `Io` the database file is still a real file: a C callback cannot suspend, so a simulated `Io` never reaches it.
- Connections of one process on one file exclude each other with sqlite's usual locks, and see `Busy` while another holds a conflicting one. Paths that differ only in `.` elements or repeated separators name one file. So do, on Windows, paths that differ in separator style or ASCII letter case. A relative and an absolute spelling, a path through `..` and a link name different files.
- Another process is not locked out. Use a database file from one process at a time.
- A database that shrinks keeps its old size on disk.
- There is no clock: `date('now')`, `current_timestamp` and the `'localtime'` modifier give NULL. Pass times in as parameters, from `std.time`.
- There is no sleep: `PRAGMA busy_timeout` does not wait, and `Busy` comes back at once. Retry in your own loop, after the other connection ends its transaction.

## Tasks and cancellation

The SQLite build has no mutexes, so the whole library, not one connection, is single-threaded. The module runs every C call, on every connection, under one process-wide lock. So a `&Db` may be shared between tasks (`Db` is `Send` and `Share`), and calls run one at a time.

- A task that waits for the lock parks and holds no thread. The task that holds it keeps its thread for the length of one C call.
- `Db.Exec` takes the lock once per statement, so another task may run between two of them.
- `Open`, `Prepare`, `Exec` and `Step` take an `Io`. In a cancelled scope they fail with `Canceled` before they start, and a task parked on the lock leaves with `Canceled`. A statement that has started runs to its next row; a cancel does not interrupt it.
- The binds, the column reads, `Reset` and both `Close`s are never cancelled. They wait for the lock through the `Io` their `Db` was opened with, or their `Stmt` prepared with.

## Memory and cost

`Open` copies the path, with a NUL, into its arena `a`. `Prepare`'s arena is kept for the statement and borrowed while it lives. sqlite allocates connections, statements, bound text and its page cache through `std.libc`, outside your arenas, and frees them on `Close`. Every bind and column read is O(1) except `BindText` and `Str`, which are O(length). Temporary tables, large sorts and `VACUUM` work in memory (`SQLITE_TEMP_STORE=3`), never in a temporary file. The cost of `Step` and `Exec` is the cost of the SQL.

## Examples

Each program runs as written with `mk run` in a package whose `pkg.mk` is the one [above](#the-package-manifest), with `src/main.mk` holding the program.

### Create a table and query it

```mk
// verdict: parse
import std.db.sqlite

export Main(sys: &System): sqlite.Error|WriteError!void {
    db := try sqlite.Open(sys.arena, sys.io, ":memory:") // a database in memory
    defer db.Close()
    try db.Exec(sys.io, "create table fruit(name text, price integer)")

    var ins := try db.Prepare(sys.arena, sys.io, "insert into fruit values (?1, ?2)")
    defer ins.Close()
    names := ["apple", "pear", "plum"]
    prices: [3]i64 = [30, 45, 20]
    for i in 0..3 {
        try ins.BindText(1, names[i]) // sqlite keeps a copy
        try ins.BindInt(2, prices[i])
        _ = try ins.Step(sys.io) // false: an insert returns no row
        ins.Reset()              // run it again; the bindings stay
    }

    var q := try db.Prepare(sys.arena, sys.io, "select name, price from fruit where price < ?1 order by price")
    defer q.Close()
    try q.BindInt(1, 40)
    for try q.Step(sys.io) { // true while there is a row
        try sys.Print("{name}: {price}\n", (name: q.Str(0), price: q.Int(1)))
    }
}
```

```text
$ mk run
plum: 20
apple: 30
```

- The deferred closes run in reverse: `q`, then `ins`, then `db`.
- Prepare a statement once and `Reset` it for each row, rather than building SQL text from values.

### Handle a constraint failure

```mk
// verdict: parse
import std.db.sqlite

// AddUser inserts a user and reports a taken name as false.
AddUser(db: &sqlite.Db, a: &Arena, io: Io, name: str): sqlite.Error!bool {
    var st := try db.Prepare(a, io, "insert into users(name) values (?1)")
    defer st.Close()
    try st.BindText(1, name)
    _ = st.Step(io) else e {
        match e {
            sqlite.Sqlite(code) {
                if code == 2067 { // SQLITE_CONSTRAINT_UNIQUE
                    return false
                }
            }
            _ {}
        }
        fail e
    }
    return true
}

export Main(sys: &System): sqlite.Error|WriteError!void {
    db := try sqlite.Open(sys.arena, sys.io, ":memory:")
    defer db.Close()
    try db.Exec(sys.io, "create table users(id integer primary key, name text unique)")
    for name in ["ada", "grace", "ada"] {
        added := try AddUser(&db, sys.arena, sys.io, name)
        try sys.Print("{name}: {r}\n", (name, r: if added { "added" } else { "taken" }))
    }
    db.Exec(sys.io, "selct 1") else e {
        match e {
            sqlite.Sqlite(code) { try sys.Print("syntax error: Sqlite({code})\n", (code)) }
            _ { fail e }
        }
    }
}
```

```text
$ mk run
ada: added
grace: added
ada: taken
syntax error: Sqlite(1)
```

- Match the extended code for one constraint, or `code & 0xFF == 19` for any.
- `fail e` passes every other failure on unchanged.

### Keep rows past the statement

```mk
// verdict: parse
import std.db.sqlite

struct Note {
    id: i64
    title: str
}

// Titles copies every note into a, so the result outlives the statement.
Titles(db: &sqlite.Db, a: &Arena, io: Io): sqlite.Error!List[Note] from a {
    var out := List[Note].New(a)
    var st := try db.Prepare(a, io, "select id, title from notes order by id")
    defer st.Close()
    for try st.Step(io) {
        title := try a.Dup(st.Str(1)) // st.Str(1) lasts only until the next Step
        try out.Push(Note(id: st.Int(0), title: title))
    }
    return out
}

export Main(sys: &System): sqlite.Error|WriteError!void {
    db := try sqlite.Open(sys.arena, sys.io, ":memory:")
    defer db.Close()
    try db.Exec(sys.io, "create table notes(id integer primary key, title text); insert into notes(title) values ('milk'), ('bread'), (null)")
    notes := try Titles(&db, sys.arena, sys.io)
    for n in notes {
        try sys.Print("{id} [{title}]\n", (id: n.id, title: n.title))
    }
}
```

```text
$ mk run
1 [milk]
2 [bread]
3 []
```

- `from a` says the list borrows only the arena, so it outlives `st`.
- A NULL title reads as `""`.
- `Exec` runs both statements of its text.

### Two connections to one file

```mk
// verdict: parse
import std.db.sqlite
import std.fs

Count(db: &sqlite.Db, a: &Arena, io: Io): sqlite.Error!i64 {
    var st := try db.Prepare(a, io, "select count(*) from log")
    defer st.Close()
    _ = try st.Step(io)
    return st.Int(0)
}

Create(sys: &System): sqlite.Error!void {
    db := try sqlite.Open(sys.arena, sys.io, "app.db") // the file is created
    defer db.Close()
    try db.Exec(sys.io, "create table log(line text); insert into log values ('started')")
}

TwoConnections(sys: &System): sqlite.Error|WriteError!void {
    w := try sqlite.Open(sys.arena, sys.io, "app.db") // the file exists: opened read-write
    defer w.Close()
    r := try sqlite.Open(sys.arena, sys.io, "app.db")
    defer r.Close()
    try w.Exec(sys.io, "begin exclusive; insert into log values ('working')")
    n := Count(&r, sys.arena, sys.io) else e {
        match e {
            sqlite.Busy { try sys.Print("r is busy while w holds its lock\n", ()) }
            _ { fail e }
        }
        return
    }
    try sys.Print("{n} rows\n", (n))
}

// Rows counts the rows a new connection sees.
Rows(sys: &System): sqlite.Error!i64 {
    db := try sqlite.Open(sys.arena, sys.io, "app.db")
    defer db.Close()
    return try Count(&db, sys.arena, sys.io)
}

export Main(sys: &System): sqlite.Error|fs.Error|WriteError!void {
    try Create(sys)
    try TwoConnections(sys)
    try sys.Print("{n} row after the rollback\n", (n: try Rows(sys)))
    try fs.Remove(sys.io, "app.db")
}
```

```text
$ mk run
r is busy while w holds its lock
1 row after the rollback
```

- `w` is closed with its transaction still open, so the insert is rolled back.
- `Busy` is the answer between connections of one process. A second process opening `app.db` at the same time is not stopped, so do not share the file between processes.

## API

Stability: Stable.

### `Busy`

```
export error Busy
```

SQLITE_BUSY: another connection of this process holds a lock the call
needs. The call may be retried once that connection ends its
transaction.

### `Db`

```
export linear struct Db
```

An open connection to one database. It is linear: end it with Close.
A &Db may be shared between tasks. Every call of this module, on any
connection, takes one process-wide lock, so calls run one at a time.

### `Db.Close`

```
export Db.Close(self)
```

Closes the connection, and rolls back a transaction it left open. It
cannot fail: no Stmt of the Db can still be open, since each borrows
it. It waits for the process-wide lock and is not cancelled.

### `Db.Exec`

```
export Db.Exec(&self, io: Io, sql: str): Error!void
```

- Errors: Error (Canceled, OutOfMemory, Busy, Misuse, Sqlite(code: i32))

Runs every statement of sql, discarding rows. Fails as Prepare and Step
do, at the first statement that fails; the statements before it have
run. The process-wide lock is taken once per statement, so another task
may run between two. It allocates nothing in Mk.

### `Db.Prepare`

```
export Db.Prepare(&self, a: &Arena, io: Io, sql: str): Error!Stmt from self, a
```

- Flows: The result borrows from self and a.
- Errors: Error (Canceled, OutOfMemory, Busy, Misuse, Sqlite(code: i32))

Compiles the first statement of sql. Fails with Misuse when sql holds
no statement, Sqlite(18) (SQLITE_TOOBIG) past 2^31 - 2 bytes, Canceled
in a cancelled scope, and with sqlite's code for a syntax or schema
error. O(len(sql)) to parse. a is reserved for the statement's own
copies; sqlite allocates the statement through std.libc.

### `Error`

```
export open type Error = Sqlite | Busy | Misuse | OutOfMemory | Canceled
```

Every failure of this module. SQLITE_NOMEM is OutOfMemory and
SQLITE_INTERRUPT is Canceled. The set is open, so a match on it outside
std keeps a `_` arm.

### `Misuse`

```
export error Misuse
```

SQLITE_MISUSE: the call broke sqlite's rules for its interface, or
Prepare was given no statement.

### `Open`

```
export Open(a: &Arena, io: Io, path: str): Error!Db
```

- Errors: Error (Canceled, OutOfMemory, Busy, Misuse, Sqlite(code: i32))

Opens the database file at path for reading and writing, and creates it
when it is missing; "" or ":memory:" opens a database in memory. Fails
with Sqlite(14) (SQLITE_CANTOPEN) for a path that holds a NUL byte or
a file that cannot be opened, Canceled in a cancelled scope, and with
sqlite's code otherwise.

sqlite reads and writes the file through the OS, on the calling
thread, never through io: io serves cancellation and the wait for the
process-wide lock, so under a simulated Io the database file is still
a real file. Only connections of this process exclude each other;
another process is not locked out, so use a database file from one
process at a time. The path is copied with a NUL into a; sqlite
allocates the connection through std.libc.

### `Sqlite`

```
export error Sqlite(code: i32)
```

A sqlite failure, with sqlite's extended result code (sqlite3.h): a
second insert of a key in a unique column is Sqlite(code: 2067),
SQLITE_CONSTRAINT_UNIQUE. The low byte, code & 0xFF, is the primary
code (19, SQLITE_CONSTRAINT). Every code without a member of its own is
Sqlite, SQLITE_LOCKED (6) included: that is a conflict inside one
connection, such as a table dropped while one of its statements runs,
which waiting does not clear.

### `Stmt`

```
export linear view struct Stmt
```

One prepared statement of a Db. It is linear: end it with Close. It
borrows the Db and Prepare's arena, so the Db cannot be closed while
the statement lives.

### `Stmt.BindInt`

```
export Stmt.BindInt(mut self, i: int, v: i64): Error!void
```

- Errors: Error (Canceled, OutOfMemory, Busy, Misuse, Sqlite(code: i32))

Binds parameter i (from 1, as ?1 in the SQL) to v. Fails with
Sqlite(25) (SQLITE_RANGE) for an i out of range. O(1). It waits for
the process-wide lock and is not cancelled.

### `Stmt.BindText`

```
export Stmt.BindText(mut self, i: int, s: str): Error!void
```

- Errors: Error (Canceled, OutOfMemory, Busy, Misuse, Sqlite(code: i32))

Binds parameter i (from 1) to a copy of s, so s need not outlive the
statement. Fails with Sqlite(25) for an i out of range and Sqlite(18)
past 2^31 - 2 bytes. O(len(s)); sqlite copies s through std.libc. It
waits for the process-wide lock and is not cancelled.

### `Stmt.Close`

```
export Stmt.Close(self)
```

Finalizes the statement and ends its loan on the Db. It cannot fail.

### `Stmt.Int`

```
export Stmt.Int(&self, col: int): i64
```

Column col (from 0) of the current row as an integer, as sqlite
converts it; a NULL, a column past the end or no row gives 0. O(1).

### `Stmt.Reset`

```
export Stmt.Reset(mut self)
```

Rewinds the statement to run again; the bindings stay. O(1). It cannot
fail.

### `Stmt.Step`

```
export Stmt.Step(mut self, io: Io): Error!bool
```

- Errors: Error (Canceled, OutOfMemory, Busy, Misuse, Sqlite(code: i32))

Runs the statement to its next row: true with a row, false when done.
Fails with Busy when another connection holds a lock it needs,
Canceled in a cancelled scope or while it waits for the process-wide
lock, and with sqlite's code otherwise. A statement that has started
runs to its next row; a cancel does not interrupt it.

### `Stmt.Str`

```
export Stmt.Str(&self, col: int): str from self
```

- Flows: The result borrows from self.

Column col (from 0) of the current row as text. The bytes are sqlite's
and stay valid until the next Step, Reset or Close; the compiler
enforces this, since the result borrows the Stmt; Step and Reset take
it mut and Close ends it. Copy the text into an arena to keep it
longer. A NULL, a column past the end, no row, or text that is not
UTF-8 gives "": Str has no error to report bad UTF-8 with, so read
such a column with SQL's length() or cast(x as blob). O(len).
