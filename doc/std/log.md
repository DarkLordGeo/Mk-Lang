# std.log

`std.log` writes levelled log lines: one line per call, with the level first. A `Logger` writes through a `Writer` you give it, for one task. A `Sink` writes to standard error, standard output or a file and is shared by any number of tasks, whose records never interleave. Neither allocates. Use `sys.Print` for a program's output, and `std.log` for reports about what it is doing.

Read the [standard library guide](README.md) first, and [formatting and printing](core.md#formatting-and-printing) for the format strings that every call takes.

```mk
// verdict: parse
import std.log
```

## Contents

- [The main pieces](#the-main-pieces)
- [Levels and lines](#levels-and-lines)
- [Logger: one task, one Writer](#logger-one-task-one-writer)
- [Sink: shared by tasks](#sink-shared-by-tasks)
- [Errors](#errors)
- [Memory and cost](#memory-and-cost)
- [Examples](#examples): [log at levels](#log-at-levels), [log from many tasks](#log-from-many-tasks), [a record that is too long](#a-record-that-is-too-long)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Log through a `Writer` you own | `Logger.New(w, level)` |
| Log with the time since start | `Logger.NewTimed(io, w, level)` |
| Log from many tasks | `log.Stderr(level)`, `log.Stdout(level)`, `log.ToFile(&mutex, level)`, which return a `Sink` |
| Write a line | `Debug`, `Info`, `Warn`, `Error`, or `Log` with a `Level` |

## Levels and lines

`Level` has four members in increasing order of severity: `Debug`, `Info`, `Warn` and `Error`. A logger or sink is made with the least level it writes, and a call below it returns at once and writes nothing. No logger drops `Error`.

Every call takes a format string and a record, as `Print` does, and the compiler checks one against the other at the call (`E0706`). A message with no holes needs no record: `lg.Info("ready")`.

A line is the level's word (`DEBUG`, `INFO`, `WARN` or `ERROR`), a space, the message and a newline:

```text
INFO listening on port 8080
```

A `Logger` made with `NewTimed` puts the time since it was made in front, in seconds and microseconds: `0.000412 INFO ready`. The time comes from the monotonic clock through `io`, so it is relative, never the time of day.

## Logger: one task, one Writer

A `Logger` borrows the `Writer` you give it (`from w`) and buffers nothing itself, so whatever the `Writer` buffers, the `Logger` does too. Standard output and standard error are buffered by the process (see [standard streams](core.md#system-and-the-standard-streams)); call `Flush` to put lines out sooner. A `Logger` is a view: it cannot outlive the `Writer`, and only one task uses it.

## Sink: shared by tasks

A `Sink` names a process stream or a file and holds no `Writer`. It is plain, `Send` and `Share`, so copy it into each task or lend it with `&`. Each call takes the `Io` to write with.

- A call formats its record into a 4 KiB buffer on the caller's stack, then writes it with one write. It writes to the process stream under that stream's lock, or to the file under its mutex. So records of different tasks never interleave, and `log.Stderr`'s records stay in order with `sys.PrintErr`'s bytes.
- A record is at most 4,096 bytes with its newline. A longer one is cut on a UTF-8 boundary and ends with ` [cut N bytes]`, where N counts the bytes not written.
- A call is not a cancellation point. The file's mutex is taken through `io.NoCancel()`, so a record made while its task's scope is being cancelled is still written.
- `log.ToFile` borrows a `Mutex[fs.File]`. The program owns the file, and takes it back and closes it once every task that logs to it has joined.
- `Sink.file` is the mutex of a `ToFile` sink and `none` for a process stream; the sink only locks it.

## Errors

Every call fails with `WriteError` and nothing else: when the write fails, and when a field's `Format` fails. A `Sink` formats the whole record before it writes, so a failed `Format` writes nothing. A `Sink` call also fails on an `Io` that reaches no process stream or file.

## Memory and cost

Nothing in `std.log` allocates. A call costs O(length of the line). A `Sink` call uses one 4 KiB stack buffer and one write.

## Examples

### Log at levels

```mk
// verdict: ok
import std.log

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var lg := log.Logger.New(mut out, log.Level.Info) // drops Debug
    try lg.Debug("not written")
    try lg.Info("listening on port {port}", (port: 8080))
    try lg.Warn("{n} slow requests", (n: 3))
    try lg.Error("disk full: {path}", (path: "/var/data"))
    try out.Flush() // the logger writes into out's buffer
}
```

```text
$ mk run levels.mk
INFO listening on port 8080
WARN 3 slow requests
ERROR disk full: /var/data
```

### Log from many tasks

Four tasks log three records each through one file sink. The program then reads the file back and counts the lines that are whole records.

```mk
// verdict: ok
import std.fs
import std.log

Work(io: Io, sink: &log.Sink, id: int) {
    for step in 0..3 {
        sink.Info(io, "task {id} step {step}", (id, step)) else {
            return
        }
    }
}

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    f := try fs.Create(sys.io, "tasks.log")
    m := Mutex[fs.File].New(f)
    sink := log.ToFile(&m, log.Level.Info)
    scope s := sys.io.Scope() {
        for id in 0..4 {
            s.go Work(sys.io, &sink, id) // every task logs through one sink
        }
    }
    var g := m.Into() // the tasks have joined: take the file back and close it
    onerr g.Abandon()
    try g.Close(sys.io)
    text := try fs.ReadText(sys.arena, sys.io, "tasks.log")
    var n := 0
    for line in text.Lines() {
        if line.StartsWith("INFO task ") { // each line is one whole record
            n += 1
        }
    }
    try sys.Print("{n} whole records\n", (n))
    try fs.Remove(sys.io, "tasks.log")
}
```

```text
$ mk run tasks.mk
12 whole records
```

- The order of the records depends on how the tasks run; each record is whole.
- For standard error, `log.Stderr(log.Level.Info)` needs no file and no mutex. See the [Report from many tasks](../cookbook.md#report-from-many-tasks) recipe.

### A record that is too long

```mk
// verdict: ok
import std.fs
import std.log

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    var t := Text.New(sys.arena)
    for _ in 0..5000 {
        try t.Push("x")
    }
    f := try fs.Create(sys.io, "big.log")
    m := Mutex[fs.File].New(f)
    sink := log.ToFile(&m, log.Level.Info)
    sink.Info(sys.io, "{body}", (body: t.Str())) else {} // 5,005 bytes with "INFO "
    var g := m.Into()
    onerr g.Abandon()
    try g.Close(sys.io)
    text := try fs.ReadText(sys.arena, sys.io, "big.log")
    tail := text[text.Len() - 20..]
    try sys.Print("{n} bytes, ending {tail}", (n: text.Len(), tail))
    try fs.Remove(sys.io, "big.log")
}
```

```text
$ mk run cut.mk
4095 bytes, ending xxx [cut 927 bytes]
```

The record stays within 4,096 bytes, the mark included, and N counts every byte of the record that is not in the file.

## API

<!-- api:begin -->
Stability: Stable.

### `Level`

```
export enum Level
```

The four levels, in increasing order of severity.

Variants:

- `Debug`
- `Info`
- `Warn`
- `Error`

### `Logger`

```
export view struct Logger
```

A writer of log lines over a Writer it borrows, with the least level it
writes. It buffers nothing itself; the Writer does. No exported field:
make one with New or NewTimed.

### `Logger.Debug`

```
export Logger.Debug[A: Fields[Format] + Plain](mut self, const f: str, args: A): WriteError!void
```

- Errors: WriteError

Log at Debug: dropped unless the logger's level is Debug. Fails with
the Writer's error. O(length of the line); allocates nothing.

### `Logger.Error`

```
export Logger.Error[A: Fields[Format] + Plain](mut self, const f: str, args: A): WriteError!void
```

- Errors: WriteError

Log at Error, which no logger drops. Fails with the Writer's error.
O(length of the line); allocates nothing.

### `Logger.Info`

```
export Logger.Info[A: Fields[Format] + Plain](mut self, const f: str, args: A): WriteError!void
```

- Errors: WriteError

Log at Info: dropped when the logger's level is Warn or Error. Fails
with the Writer's error. O(length of the line); allocates nothing.

### `Logger.Log`

```
export Logger.Log[A: Fields[Format] + Plain](mut self, level: Level, const f: str, args: A): WriteError!void
```

- Errors: WriteError

Writes one line at level, unless level is below the logger's: the
stamp of a NewTimed logger, the level's word (DEBUG, INFO, WARN or
ERROR), a space, the message and a newline. f and args are Print's
format and record, checked at the call (E0706); a message with no holes
needs no record. Fails with the Writer's error, or when a field's
Format fails. O(length of the line); allocates nothing.

### `Logger.New`

```
export Logger.New(w: mut Writer, level: Level): Logger from w
```

- Flows: The result borrows from w.

A logger that writes `LEVEL message` lines to w, such as
`INFO listening on 8080`, and drops calls below level. O(1); allocates
nothing.

### `Logger.NewTimed`

```
export Logger.NewTimed(io: Io, w: mut Writer, level: Level): Logger from w
```

- Flows: The result borrows from w.

A logger whose lines start with the time since it was made, in seconds
and microseconds: `S.UUUUUU LEVEL message`, such as
`0.000412 INFO ready`. The time is read through io on the monotonic
clock, so it is relative, never the time of day. O(1); allocates
nothing.

### `Logger.Warn`

```
export Logger.Warn[A: Fields[Format] + Plain](mut self, const f: str, args: A): WriteError!void
```

- Errors: WriteError

Log at Warn: dropped when the logger's level is Error. Fails with the
Writer's error. O(length of the line); allocates nothing.

### `Sink`

```
export struct Sink
```

A log destination that many tasks share: a process stream, or a file
under a mutex, and the least level it writes. Each call writes its
record with one write, so the records of different tasks never
interleave. Plain, Send and Share, and it borrows no Writer: copy it
into each task, or lend it with &. Make one with Stderr, Stdout or
ToFile.

Fields:

- `file: ?&Mutex[fs.File]`: The mutex a ToFile sink writes under, none for a process stream.
  Exported because a plain struct's reference field must be (E0410);
  the sink only locks it.

### `Sink.Debug`

```
export Sink.Debug[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A): WriteError!void
```

- Errors: WriteError

Log at Debug: dropped unless the sink's level is Debug. Fails as Log.
O(record bytes); no allocation.

### `Sink.Error`

```
export Sink.Error[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A): WriteError!void
```

- Errors: WriteError

Log at Error, which no sink drops. Fails as Log. O(record bytes); no
allocation.

### `Sink.Info`

```
export Sink.Info[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A): WriteError!void
```

- Errors: WriteError

Log at Info: dropped when the sink's level is Warn or Error. Fails as
Log. O(record bytes); no allocation.

### `Sink.Log`

```
export Sink.Log[A: Fields[Format] + Plain](&self, io: Io, level: Level, const f: str, args: A): WriteError!void
```

- Errors: WriteError

Writes one record, `LEVEL message` and a newline, unless level is below
the sink's. The record is formatted on the stack, at most 4,096 bytes
with its newline: a longer one is cut on a UTF-8 boundary and ends with
` [cut N bytes]`, N counting the bytes not written. Then it is written
whole with one write: to the process stream under its lock, or to the
file under its mutex, taken through io.NoCancel(), so it is not a
cancellation point and a record made while its scope is cancelled is
still written. f and args are Print's format and record, checked at the
call (E0706).

Fails with WriteError when the write fails, on an Io that reaches no
process or file, and when a field's Format fails, in which case nothing
is written. O(record bytes): one 4 KiB stack buffer; no allocation.

### `Sink.Warn`

```
export Sink.Warn[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A): WriteError!void
```

- Errors: WriteError

Log at Warn: dropped when the sink's level is Error. Fails as Log.
O(record bytes); no allocation.

### `Stderr`

```
export Stderr(level: Level): Sink
```

A sink that writes records at level or above to standard error, in
order with System.PrintErr's bytes. O(1); allocates nothing.

### `Stdout`

```
export Stdout(level: Level): Sink
```

A sink that writes records at level or above to standard output, after
the bytes System.Stdout's writers have buffered. O(1); allocates nothing.

### `ToFile`

```
export ToFile(f: &Mutex[fs.File], level: Level): Sink from f
```

- Flows: The result borrows from f.

A sink that writes records at level or above to the file f guards. The
program owns the file and closes it after the tasks that log to it have
joined. O(1); allocates nothing.

<!-- api:end -->
