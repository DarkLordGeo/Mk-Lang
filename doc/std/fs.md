# std.fs

`std.fs` reads and writes files and directories: whole files in one call, open files with a position, directory listings, tree walks, metadata, links, locks and memory maps. It works the same on Linux and Windows, and under a simulated `Io` from `std.sim`.

Read the [library guide](README.md) first.

```mk
// verdict: parse
import std.fs
```

## Contents

- [The main pieces](#the-main-pieces)
- [Paths](#paths)
- [Files and other handles](#files-and-other-handles)
- [Errors](#errors)
- [Memory and cost](#memory-and-cost)
- [Cancellation](#cancellation)
- [Examples](#examples): [read a whole file](#read-a-whole-file), [write a file](#write-a-file), [read line by line](#read-line-by-line), [append to a log](#append-to-a-log), [walk a directory](#walk-a-directory)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Read a whole file | `ReadText` (UTF-8 text) or `ReadBytes` |
| Write a whole file | `WriteAll` |
| Open a file | `Open` (read), `Create` (create or truncate, read and write), `OpenWith` with `Options` |
| Read or write an open file | `File.Read`, `File.Write`, `File.WriteAll`, `File.ReadAt`, `File.WriteAt`, `File.Seek` |
| Read lines or buffered data | `File.Reader`, `File.Writer` |
| Facts about a file | `Stat`, `StatLink`, which return an `Info` |
| List one directory | `Dir.Open`, then `Dir.Next` until `none` |
| Walk a tree | `Walk`, then `Walker.Next` until `none` |
| Make and remove | `MakeDir`, `MakeDirAll`, `Remove`, `RemoveAll`, `Rename`, `Copy` |
| Temporary files | `CreateTemp`, `TempDir` |
| Links | `Symlink`, `ReadLink` |
| Share a file between processes | `File.Lock`, `File.TryLock`, `File.Unlock` |
| Map a file into memory | `Map` (unsafe), then `Mapping.Bytes` |
| Where am I | `Cwd`, `Absolute` |

Every function that touches the file system takes an `io: Io` ([`Io`](README.md#4-io-and-the-system-handle)).

## Paths

A path is a `str`. A relative path is resolved against the working directory. On Windows both `/` and `\` separate elements; on Linux only `/` does.

Names that come from the OS, in `Dir.Next`'s `Entry.name` and `Walker.Next`'s `WalkEntry.path`, are a `Path`: the OS's bytes, which may not be UTF-8. `Path.Str()` returns the text when it is UTF-8, and a `Path` prints with `{}` either way. To open or stat such a name, pass the `Path` itself to `OpenPath` or `StatPath`, so a name that is not UTF-8 still works.

`Absolute` makes a path absolute and cleans it from its text alone, without resolving links. `Stat`, `Open` and the other calls follow links as the OS does; `StatLink`, `Walk` and `RemoveAll` do not follow them.

## Files and other handles

A `File` is an open file: an OS handle and the file's position. It is **linear**: the compiler rejects a path through your code that drops a `File` without ending it. End it with `Close`, which can fail and reports the OS's error, or with `Abandon` on an error path, which ignores errors. A failed `Close` leaves the `File` still owned, so the usual pattern is:

```mk
// verdict: ok
import std.fs

Touch(io: Io, path: str): fs.Error!void {
    var f := try fs.Create(io, path)
    onerr f.Abandon() // runs only when a later line fails, a failed Close included
    try f.WriteAll(io, "ready\n".AsBytes())
    try f.Close(io)
}
```

- `File.Read` and `File.Write` move the position past what they read or write. `File.ReadAt` and `File.WriteAt` take an offset and leave the position alone.
- `File.Read` and `File.Write` make one call and may move fewer bytes than asked. `File.WriteAll` loops until every byte is written.
- `File.Reader` and `File.Writer` borrow the file and add a buffer you pass in. While one lives, nothing else can use the file. Flush a `Writer` before you drop it.
- `Dir`, `Walker` and `Mapping` are linear too: end them with `Dir.Close`, `Walker.Close` and `Mapping.Unmap`. None of those can fail.

## Errors

Every function fails with members of one error set, `fs.Error`. A function's signature names the set; the doc text of each function says which members it returns and when.

| Member | Meaning |
|---|---|
| `NotFound` | Nothing at the path |
| `Exists` | Something is already at the path |
| `Denied` | The OS refused access |
| `IsDirectory` | A directory where a file is needed |
| `NotDirectory` | A file where a directory is needed |
| `NoSpace` | The device is full |
| `InvalidPath` | The path cannot name a file on this OS |
| `Unsupported` | This target or this `Io` cannot do it |
| `Os(code)` | Any other OS failure, with the OS's error code; 0 for an argument out of range |
| `Utf8Error` | Text that should be UTF-8 is not (from the prelude) |
| `Canceled` | The call's scope was cancelled (from the prelude) |

An OS error with a member of its own becomes that member: a missing file is `NotFound` on both targets. Functions that allocate add `OutOfMemory`. `fs.Error` is open, so a `match` on it outside std keeps a `_` arm.

## Memory and cost

`std.fs` allocates only where a signature takes an allocator. Functions that return text or bytes (`ReadText`, `ReadBytes`, `Cwd`, `Absolute`, `ReadLink`, `TempDir`, `Dir.Next`, `Walker.Next`) take an `a: &Arena` and put the result there; the result borrows the arena, which the signature shows as `from a`. `Walk` takes a `&Heap` for the walker's buffers, which `Walker.Close` frees. Everything else allocates nothing.

Each function's doc states its cost. "One runtime call" means one request to the OS through std's runtime: usually one system call on Linux, sometimes a few on Windows. Operations on paths, and copies, run on the runtime's blocking pool, off the threads that run tasks, so a slow disk does not hold up other tasks.

`Walk` and `RemoveAll` read no more than each entry's kind from a listing. On Linux the kind is the listing's `d_type`, with a `statx` only where the file system leaves it unknown. On Windows a directory they open keeps its path, of at most 2,048 UTF-16 units, so no call inside it asks the OS for its path again; a longer path is asked for at each call.

## Cancellation

A call that can be cancelled has `Canceled` in its error set. When its task's scope is cancelled, it fails with `Canceled`, and the function's doc says what was already done: a cancelled `Copy` keeps the bytes it copied, and a cancelled `MakeDirAll` keeps the directories it made. `File.Lock` and `Walker.Next` can be called again after `Canceled`. `File.Abandon`, `Dir.Close`, `Walker.Close` and `Mapping.Unmap` cannot be cancelled.

## Examples

Each program below runs as written with `mk run`, creates the files it needs and removes them at the end.

### Read a whole file

```mk
// verdict: ok
import std.fs

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    try fs.WriteAll(sys.io, "greeting.txt", "hello\nworld\n".AsBytes())
    text := try fs.ReadText(sys.arena, sys.io, "greeting.txt") // the text lives in sys.arena
    try sys.Print("{n} bytes\n{text}", (n: text.Len(), text))
    try fs.Remove(sys.io, "greeting.txt")
}
```

```text
$ mk run read.mk
12 bytes
hello
world
```

- `ReadText` fails with `Utf8Error` when the file is not UTF-8. `ReadBytes` returns the bytes as they are.
- `WriteAll` creates the file, or truncates it, writes every byte and closes it.

### Write a file

```mk
// verdict: ok
import std.fs

// Save writes each line to path. Every failure reaches the caller,
// a failed close included.
Save(io: Io, path: str, lines: []str): fs.Error!void {
    var f := try fs.Create(io, path)
    onerr f.Abandon() // runs only if a later line fails
    for line in lines {
        try f.WriteAll(io, line.AsBytes())
        try f.WriteAll(io, "\n".AsBytes())
    }
    try f.Sync(io)  // put the data on the device
    try f.Close(io) // a close can fail too
}

export Main(sys: &System): fs.Error|WriteError!void {
    lines := ["apples", "pears", "plums"]
    try Save(sys.io, "fruit.txt", lines[..])
    info := try fs.Stat(sys.io, "fruit.txt")
    try sys.Print("fruit.txt: {size} bytes\n", (size: info.size))
    try fs.Remove(sys.io, "fruit.txt")
}
```

```text
$ mk run write.mk
fruit.txt: 19 bytes
```

- A close can fail, so it is a `try` at the end, not a `defer`.
- Without `Sync`, the data may still be in the OS's cache when the program ends; it reaches the device later.

### Read line by line

See [Read a file line by line](../cookbook.md#read-a-file-line-by-line).

- The `Reader` borrows `f` until its last use, so `f.Close` comes after the loop.
- A read failure inside the `Reader` is `ReadError`, without the OS's cause. Call `File.Read` directly when you need the `fs.Error`.

### Append to a log

```mk
// verdict: ok
import std.fs

export Main(sys: &System): fs.Error|WriteError!void {
    var o := fs.Append // write only, every write at the end
    o.permissions = fs.Permissions(mode: 0o600)
    for _ in 0..3 { // three runs of a program
        var f := try fs.OpenWith(sys.io, "app.log", o)
        onerr f.Abandon()
        try f.WriteAll(sys.io, "started\n".AsBytes())
        try f.Close(sys.io)
    }
    info := try fs.Stat(sys.io, "app.log")
    try sys.Print("app.log: {size} bytes\n", (size: info.size))
    try fs.Remove(sys.io, "app.log")
}
```

```text
$ mk run log.mk
app.log: 24 bytes
```

- Start from a preset (`DefaultOptions`, `ReadOnly`, `ReadWrite`, `CreateNew`, `Append`) and change fields. A literal of `Options` does not compile outside `std.fs` (E0204), so a field added later breaks no program.
- `OpenWith` refuses a set the targets would open differently, such as `truncate` with `append`, with `Unsupported` before it calls the OS.

### Walk a directory

```mk
// verdict: ok
import std.fs

KindName(k: fs.Kind): str {
    return match k {
        .File { "file" }
        .Dir { "dir" }
        .Link { "link" }
        _ { "other" } // Kind is open: it may gain variants
    }
}

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    try fs.MakeDirAll(sys.io, "demo/src")
    try fs.WriteAll(sys.io, "demo/src/main.mk", "// empty\n".AsBytes())
    var w := try fs.Walk(sys.heap, sys.io, "demo")
    onerr w.Close()
    for entry := try w.Next(sys.arena, sys.io) {
        name := entry.path.Base() // the last element; Path prints its bytes
        try sys.Print("{depth} {kind} {name}\n", (depth: entry.depth, kind: KindName(entry.kind), name: &name))
    }
    w.Close()
    try fs.RemoveAll(sys.io, "demo")
}
```

```text
$ mk run walk.mk
0 dir demo
1 dir src
2 file main.mk
```

- The walk returns the root first, then each directory's entries before moving on, depth first. Within a directory the order is the OS's, so sort the entries when order matters.
- `entry.path` is the full path, joined with the target's separator: `demo/src/main.mk` on Linux, `demo\src\main.mk` on Windows.
- Links are reported as `Kind.Link` and never followed. Call `Walker.SkipDir` after a directory entry to skip what is inside it.
- `Walker.Next` copies each path into the arena you pass; the walker's own buffers live on the heap until `Walker.Close`.

## API

Generated from the module's doc comments by `mk doc --std std.fs` ([reading the API](README.md#reading-an-api-section)).

<!-- api:begin -->
Stability: Stable.

### `Absolute`

```
export Absolute(a: &Arena, io: Io, p: str): Error|OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (Canceled, OutOfMemory, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

p as an absolute, clean path, in a. On Linux it is p itself when p
starts with `/`, else the working directory joined with p. On Windows it
is the system's full path of p (for `C:x`, joined to the working
directory of drive C:). An empty p is the working directory.

The path is then cleaned from its text alone: `.` and `..` elements,
repeated separators and a trailing separator are removed, and `..`
never climbs above the root. Links are not resolved, so `..` after a
link names the link's parent.

Errors as Cwd. O(len) plus at most one runtime call; allocates the
result in a (on Linux, for a relative p, also the working directory
and the joined path).

Under a simulated Io (a Sim's or a Tape's) it answers as on Linux on
every target, so a seed gives one run: a p that starts with `/` is
cleaned with `/`, and any other p asks for the working directory, which
such an Io does not have (Unsupported). A Recorder over the native Io on
Windows counts as simulated here, though its file calls reach the real
file system: `/x` answers `/x`, not the drive's `C:\x`, and `\` is no
separator, so `/a\..\b` stays as written while the real file system
resolves it to `\b`. That answer is unfit for a containment check.

### `Append`

```
export const Append: Options
```

Write a log: write only, every write at the end of the file, and the
file created when it is missing.

### `Copy`

```
export Copy(io: Io, src: str, dst: str): Error!i64
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Copies the file src to dst, creating or replacing dst, and returns the
number of bytes copied. The OS copies the data itself where it can:
copy_file_range on Linux, with a read and write loop where the file
system refuses it, and CopyFileExW on Windows, which also copies the
attributes and the alternate streams.

NotFound when src or dst's parent is missing. IsDirectory when src is a
directory, and Unsupported when it is not a regular file (a FIFO, a
device), both before dst is touched. Denied, before anything is
truncated, when src and dst name one file by any path or link (under an
Io with no copy call only the same path is caught). Canceled before the
copy starts or while it runs; what it copied stays copied. Any other
failure is the OS's, as its fs.Error member.

O(size), in the kernel: a stat and one runtime call; allocates nothing
(16 KiB of stack under an Io with no copy call).

A failure before dst is opened (src's own failure, the kind check,
Denied for one file, Canceled before the start, or dst's open) leaves
dst as it was. After that, dst's earlier bytes are gone. On Linux, and
under an Io with no copy call, dst holds a prefix of src, possibly
empty: dst is truncated first, and nothing is removed.

On Windows, CopyFileExW may already have made dst src's size (as seen
for a lock violation), and on the failure it deletes dst, even a dst
that existed before. With no other handle open on dst, dst is then
absent. While a handle that shares delete is open (std's own Open
does), dst is pending deletion: every call on it, Remove too, is
Denied, and it is absent once that handle closes. If a handle that does
not share delete was open (Go's os.Open, for one), the delete fails, and
dst stays at src's size, a prefix of src then zeros, even after that
handle closes.

A caller that wants nothing left calls Remove and takes NotFound as
done; Denied means another handle still holds dst, so Remove is called
again once that handle has closed. A caller that must keep an existing
dst copies to a new name and renames it over dst.

Rarer cases: on Linux an fstat or ftruncate failure right after dst is
opened leaves an existing dst whole, and on Windows a failed attribute
read after the copy returns its error with dst fully written.

### `Create`

```
export Create(io: Io, p: str): Error!File
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Creates the file at p, or truncates the file already there, and opens
it for reading and writing at position 0. NotFound when the parent
directory is missing; any other failure is the OS's, as its fs.Error
member. One runtime call; allocates nothing.

### `CreateNew`

```
export const CreateNew: Options
```

Create a new file for reading and writing; Exists when a file is
already there.

### `CreateTemp`

```
export CreateTemp(a: &Arena, io: Io, dir: str, prefix: str): Error|OutOfMemory!(file: File, path: str) from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (Canceled, OutOfMemory, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Creates a new file in dir and returns it, open for reading and writing,
with its path in a. The name is prefix followed by 16 random
hexadecimal digits, and the mode is 0o600. An empty dir means TempDir.

InvalidPath when prefix holds a separator (`/`, and `\` on Windows or
under a simulated Io). Exists when 10,000 names in a row are taken.
NotFound when dir is missing. Canceled when Io.Random is. Any other
failure is the open's, as its fs.Error member. OutOfMemory when a
cannot hold the path. The path is allocated before any file is made, so
a failure leaves no file behind.

Usually one open, at most 10,000, each O(1) runtime calls; allocates the
path in a, and TempDir's path when dir is empty.

Under a simulated Io (a Sim's or a Tape's) the name is joined to dir
with `/` on every target, so a seed gives one run, and a prefix with
`\`, which std.sim reads as `/`, is InvalidPath on every target. A
Recorder over the native Io counts as simulated here: on Windows it
joins with `/`, so a `\\?\` dir, which takes no `/`, is InvalidPath;
on Linux it refuses a `\` that the native Io takes as a name byte.

### `Cwd`

```
export Cwd(a: &Arena, io: Io): Error|OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (Canceled, OutOfMemory, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

The working directory, in a. Utf8Error when its bytes are not UTF-8;
OutOfMemory when a cannot hold it; any other failure is the OS's. One
runtime call when the path fits in 1 KiB, else one per buffer size,
doubling from 4 KiB; allocates the path in a, and for a long path the
larger buffers tried too.

### `DefaultOptions`

```
export const DefaultOptions: Options
```

The defaults: read an existing file, create nothing. permissions is
0o666, the mode a created file gets once create is set.

### `Denied`

```
export error Denied
```

The OS refused access.

### `Dir`

```
export linear struct Dir
```

An open directory, listed one entry at a time by Next. Dir is linear:
end it with Close. Like File, it is Send but not Share.

### `Dir.Close`

```
export Dir.Close(self)
```

Closes the directory. It ignores every error and cannot be cancelled,
so it takes no Io and cannot fail. O(1); allocates nothing.

### `Dir.Next`

```
export Dir.Next(mut self, a: &Arena, io: Io): Error|OutOfMemory!?Entry from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (Canceled, OutOfMemory, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

The next entry of the directory, its name copied into a; none after the
last. `.` and `..` never appear, and the order is the file system's.
OutOfMemory when a cannot hold the name; any other failure is the OS's,
as its fs.Error member. After a failure the directory stays open: Close
it.

O(1) runtime calls per entry, two for a name over 512 bytes, and
O(name) copying; allocates one block of exactly the name's length in a.
A long name is read straight into that block.

### `Dir.Open`

```
export Dir.Open(io: Io, p: str): Error!Dir
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Opens the directory at p for listing. NotDirectory when p is a file,
NotFound when nothing is at p; any other failure is the OS's, as its
fs.Error member. O(1); allocates nothing.

### `Entry`

```
export struct Entry
```

One entry of a directory listing. name is the entry's own name, without
the directory; its bytes come from the OS and may not be UTF-8 (Path.Str
says). is_dir and size describe the entry itself, not a link's target:
a link to a directory is not a directory, and a directory's or a link's
size is 0, on both targets.

Fields:

- `name: Path`: The entry's own name, without the directory, in the OS's bytes.
- `is_dir: bool`: Whether the entry is a directory; a link to a directory is not.
- `size: i64`: The size in bytes; 0 for a directory or a link.

### `Error`

```
export open type Error = NotFound | Exists | Denied | IsDirectory | NotDirectory | NoSpace | InvalidPath | Utf8Error | Os | Canceled | Unsupported
```

Every way an fs operation can fail. The set is open: it may gain
members, so a match on it outside std needs a `_` arm.

### `Exists`

```
export error Exists
```

Something is already at the path.

### `File`

```
export linear struct File
```

An open file: an OS handle and the file's position. File is linear:
end it with Close, or with Abandon on an error path. It is Send but not
Share.

### `File.Abandon`

```
export File.Abandon(self)
```

Closes the file and ignores every error; it cannot be cancelled. Use it
on error paths and for a file whose Close already failed. O(1);
allocates nothing.

### `File.Close`

```
export File.Close(self, io: Io): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Closes the file. On success the File is consumed. On failure it is
still owned, so the caller's `onerr f.Abandon()` runs. The error is the
OS's, as its fs.Error member. O(1); allocates nothing.

### `File.Lock`

```
export File.Lock(&self, io: Io, mode: LockMode): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Takes the lock on the whole file in mode, waiting while another
holder's lock conflicts. Canceled when the scope is cancelled or its
deadline passes while it waits; nothing is taken, and the call can be
made again. Os for a file that cannot be locked. O(1) system calls per
try (one more first on Windows, the unlock); allocates nothing.

Locking a file that already holds the lock converts it, as flock(2)
does on Linux. On Windows, where LockFileEx would stack a second shared
lock or wait on the handle's own exclusive one, the file is unlocked
before it locks. A conversion that is cancelled, or that another holder
wins, can leave nothing held (always so on Windows; flock(2) does not
promise an atomic conversion), so lock again before relying on it.

A wait holds no thread. On Windows, for a file on the poller, the
kernel completes one overlapped wait at the holder's release. Elsewhere
(Linux, a file off the poller, a simulated file) the call tries again
with Io.Sleep between tries, from 1 ms doubling to 64 ms, so the lock
can be taken up to 64 ms after its release.

### `File.Read`

```
export File.Read(mut self, io: Io, buf: mut []u8): Error!int
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Reads into buf at the file's position, moves the position past the
bytes read, and returns their count, 0 at the end of the file. One
call, which may read fewer bytes than buf holds. Any failure is the
OS's, as its fs.Error member. O(n); allocates nothing.

A count the runtime reports outside 0..buf.Len() is Os(0), and the
position does not move. A file with no position (a pipe, a FIFO or a
terminal opened by path) reads at its stream position: when the OS
refuses the first positioned read, the read is made again there, and
the file reads that way from then on.

### `File.ReadAt`

```
export File.ReadAt(&self, io: Io, pos: i64, buf: mut []u8): Error!int
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Reads into buf at offset pos and returns the count; the file's
position does not move. One call, which may read fewer bytes than buf
holds. Any failure is the OS's, as its fs.Error member. O(n); allocates
nothing.

A negative pos is Os(0), with no call made. A handle with no position
(a pipe) fails as the OS says: ESPIPE, Os(29), on Linux.

### `File.Reader`

```
export File.Reader(mut self, io: Io, buf: mut []u8): Reader from self, buf
```

- Flows: The result borrows from self and buf.

A buffered Reader for this file, with buf as its buffer. It reads at
the file's position and moves it past what it takes, as File.Read does.
O(1); allocates nothing.

It reads ahead into buf, so while it lives the file's position may be
past bytes it has not returned yet. It borrows the file, so nothing else
reads or moves the position meanwhile. A read failure is reported as
ReadError, the Reader's error, without its cause.

### `File.Seek`

```
export File.Seek(mut self, io: Io, pos: i64, from: Whence): Error!i64
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Moves the file's position to pos counted from from, and returns the new
position. The position may be past the end of the file; a write there
fills the gap with zeros.

Os(0) when the result would be before 0 or past the largest i64; the
position is unchanged. Os(29) (ESPIPE, on Linux) for a pipe, or a FIFO
that the OS seeks (one served by the blocking pool). Any other failure
is the OS's.

O(1). The first Seek of a file asks the runtime once whether the handle
seeks. After that, Start and Current make no call and End makes one size
query; a file under append, or a pipe, makes one OS seek each time.
It allocates nothing.

### `File.Size`

```
export File.Size(&self, io: Io): Error!i64
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

The file's size in bytes, read from the open handle (fstat on Linux,
GetFileSizeEx on Windows). Any failure is the OS's, as its fs.Error
member. One runtime call; allocates nothing.

### `File.Sync`

```
export File.Sync(mut self, io: Io): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Asks the OS to write the file's data through to its device (fsync).
NoSpace when the device cannot take it, Canceled in a cancelled scope,
Os for any other failure. O(file size) in the worst case, in the OS;
allocates nothing.

### `File.Truncate`

```
export File.Truncate(mut self, io: Io, size: i64): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Sets the file's size: bytes past size are dropped, and a larger size
extends the file with zeros. The position does not move. Os(0) for a
negative size; any other failure is the OS's. One runtime call
(ftruncate on Linux, SetFileInformationByHandle on Windows); allocates
nothing.

On Windows a file opened under append is Denied, and a file with a live
Mapping cannot shrink (Os(1224)).

### `File.TryLock`

```
export File.TryLock(&self, io: Io, mode: LockMode): Error!bool
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Takes the lock on the whole file in mode if no other holder's lock
conflicts, and returns whether it did; it never waits. A file that
holds the lock gives it up first, as Lock does, so the answer is about
other holders only, and false can leave nothing held, as a lost
conversion of Lock can. Os for a file that cannot be locked. O(1): one
system call (two on Windows); allocates nothing.

### `File.Unlock`

```
export File.Unlock(&self, io: Io): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Releases the file's lock; a file that holds none succeeds. Close,
Abandon and the end of the process release it too. Any failure is the
OS's. O(1): one system call; allocates nothing.

### `File.Write`

```
export File.Write(mut self, io: Io, data: []u8): Error!int
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Writes data at the file's position, moves the position past the bytes
written, and returns their count. One call, which may write fewer bytes
than data holds. NoSpace when the device is full, Canceled in a
cancelled scope, Os for any other failure (Os(0) when the runtime
reports a count out of range). O(n); allocates nothing.

### `File.WriteAll`

```
export File.WriteAll(mut self, io: Io, data: []u8): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Writes all of data at the file's position, calling Write until every
byte is written. Fails with the first failed write's error. O(n);
allocates nothing.

The position moves past what was written, also when a later write
fails. A write that makes no progress is Os(0) rather than an endless
loop. An empty data writes nothing, but still fails with Canceled in a
cancelled scope.

### `File.WriteAt`

```
export File.WriteAt(&self, io: Io, pos: i64, data: []u8): Error!int
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Writes data at offset pos and returns the count; the file's position
does not move. One call, which may write fewer bytes than data holds.
A negative pos is Os(0), as for ReadAt. Unsupported, with no call made,
for a file opened under append, where the OS would put the bytes at the
end instead. Any other failure is the OS's. O(n); allocates nothing.

### `File.Writer`

```
export File.Writer(mut self, io: Io, buf: mut []u8): Writer from self, buf
```

- Flows: The result borrows from self and buf.

A buffered Writer for this file, with buf as its buffer. It writes at
the file's position and moves it, as File.Write does. O(1); allocates
nothing.

While the Writer lives it borrows the file, so nothing else moves the
position. Bytes still in buf when it is dropped are lost: call Flush
first.

### `Info`

```
export struct Info
```

The facts Stat, StatLink and StatPath report about a file: its size in
bytes, the time of its last modification, its kind and its permissions.
Stat follows links; StatLink does not.

Fields:

- `size: i64`: The size in bytes.
- `modified: time.Time`: The time of the last modification.
- `kind: Kind`: What the file is.
- `permissions: Permissions`: The file's permissions.

### `InvalidPath`

```
export error InvalidPath
```

The path cannot name a file on this OS.

### `IsDirectory`

```
export error IsDirectory
```

The path is a directory where a file is needed.

### `Kind`

```
export open enum Kind
```

What a file system entry is. The enum is open: it may gain variants, so
a match on it outside std needs a `_` arm.

Variants:

- `File`: A regular file.
- `Dir`: A directory.
- `Link`: A symbolic link; only StatLink and Walk report one.
- `Other`: A device, a pipe, a socket or anything else.

### `LockMode`

```
export enum LockMode
```

The kind of lock File.Lock and File.TryLock take.

Variants:

- `Shared`: Many holders at once, none of them Exclusive.
- `Exclusive`: One holder.

### `MakeDir`

```
export MakeDir(io: Io, p: str): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Creates the directory p. Its parent must exist: NotFound otherwise.
Exists when anything is at p, a directory included. Denied, InvalidPath
or Os as the OS answers. One runtime call; allocates nothing.

### `MakeDirAll`

```
export MakeDirAll(io: Io, p: str): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Creates the directory p and every missing parent. It succeeds when p is
already a directory, or a link to one, and does nothing when p names
only a root. NotFound for an empty p. NotDirectory when p or a parent is
something else, a dangling link included. Denied, InvalidPath or Os as
the OS answers. Canceled when cancelled; the directories made so far
stay. O(len(p)), with at most two runtime calls per element of p, one
when p's parent exists; allocates nothing.

### `Map`

```
export Map(io: Io, f: &File, offset: i64, len: int): Error!Mapping
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Maps len bytes of f, from offset on, into memory, read-only and
private. Map can be called only in an unsafe block (E1101 elsewhere):
the caller answers that no process changes the mapped bytes while the
Mapping lives. len 0 gives an empty mapping and makes no system call.

Os(0) for a negative offset or len, or a range past the end of the
file, so no mapped page lies past the end on either target. Denied when
f was not opened for reading. Any other failure is the OS's, as its
fs.Error member.

O(1): a size query and a mapping call (on Windows a section, made and
closed, and a view); allocates nothing but the mapping's address space,
whose pages the OS reads from the file on first touch.

### `Mapping`

```
export linear struct Mapping
```

A read-only view of a range of a file in memory, made by Map. Mapping
is linear: end it with Unmap. It owns the mapping, not the file, which
may be closed while the Mapping lives.

### `Mapping.Bytes`

```
export Mapping.Bytes(&self): []u8 from self
```

- Flows: The result borrows from self.

The mapped bytes, readable while the Mapping lives. Reading them may
fault pages in from the file. Reading a page that another process cut
from the file panics; it is not undefined behaviour. O(1); allocates
nothing.

### `Mapping.Unmap`

```
export Mapping.Unmap(self)
```

Releases the mapping; the file is not touched. It cannot be cancelled
and cannot fail: unmapping a range the OS mapped fails only on a broken
handle, and that error is ignored, as Abandon ignores a close's error.
O(1): one system call, none for an empty mapping; allocates nothing.

### `NoSpace`

```
export error NoSpace
```

The device is full.

### `NotDirectory`

```
export error NotDirectory
```

A component of the path, or the path itself, is not a directory.

### `NotFound`

```
export error NotFound
```

No file or directory at the path.

### `Open`

```
export Open(io: Io, p: str): Error!File
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Opens the file at p for reading, at position 0. NotFound when nothing is
at p; any other failure is the OS's, as its fs.Error member (Denied,
...). One runtime call; allocates nothing.

A directory opens too, on both targets, and File.Read on it fails with
IsDirectory. A File.Reader over it reports that as ReadError, so a
caller that wants the cause reads once with File.Read or calls Stat
first.

### `OpenPath`

```
export OpenPath(io: Io, p: &Path, o: Options): Error!File
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

OpenWith for a Path that Walk or Dir.Next listed. It opens the path
from its bytes, so every listed name opens, even one that is not UTF-8:
on Linux any bytes, on Windows a name's WTF-8, an unpaired surrogate
included. O(n) in the path's length for the checks, then one runtime
call; allocates nothing.

InvalidPath for a NUL byte, or on Windows for bytes that are not WTF-8.
Unsupported for bytes that are not UTF-8 under an Io other than the
native one (std.sim, a Recorder). Any other failure as OpenWith.

### `OpenWith`

```
export OpenWith(io: Io, p: str, o: Options): Error!File
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Opens the file at p as o says. The position starts at 0, except under
append, where every write lands at the end of the file and Read, Write
and Seek use the OS's own position. One runtime call; allocates nothing.

Unsupported, before any call, for a set that the targets do not open
alike: none of read, write and append; create, truncate or exclusive
without write or append; truncate with append. Exists under exclusive
when p exists. NotFound when p or its parent is missing and create is
not set. Any other failure is the OS's, as its fs.Error member.

On Windows an append handle has FILE_APPEND_DATA without
FILE_WRITE_DATA, so File.Truncate on it is Denied.

### `Options`

```
export struct Options
```

How OpenWith opens a file: the access wanted, whether to create or
truncate it, and the mode bits of a created file. Start from
DefaultOptions or a preset and assign the fields to change. Options has
a private field, so a literal of it outside std.fs is E0204; a field
added later breaks no program.

Fields:

- `read: bool`: Open for reading.
- `write: bool`: Open for writing at the file's position.
- `append: bool`: Open for writing, every write at the end of the file; implies write.
- `create: bool`: Create the file when it is missing.
- `truncate: bool`: Cut the file to 0 bytes when it opens.
- `exclusive: bool`: Create the file, failing with Exists when it is already there;
  implies create.
- `permissions: Permissions`: The mode bits a created file gets, less the umask on Linux. On
  Windows only the owner's write bit counts: clear, the file is
  created read-only. Ignored when the file exists.

### `Os`

```
export error Os(code: i64)
```

Any other OS failure, with the OS's error code. The code is 0 when an
argument is out of range (a negative position or length) or the runtime
broke its contract (a count out of range, a write with no progress).

### `Permissions`

```
export struct Permissions
```

A file's permission bits. mode is the Unix mode bits, at most 0o7777.
On Windows it is 0o444 or 0o666, by the read-only attribute, plus 0o111
on a directory.

Fields:

- `mode: u32`: The mode bits.

### `ReadBytes`

```
export ReadBytes(a: &Arena, io: Io, p: str): Error|OutOfMemory![]u8 from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (Canceled, OutOfMemory, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Reads the whole file at p into one block of a and returns it. Fails
with the fs.Error of the open, size query or read that failed, or
OutOfMemory. O(n); allocates the n bytes read plus at most one first
buffer in a.

The first buffer is the file's size plus one byte (4 KiB when the size
is 0), so a file whose size is right costs two reads and one buffer,
and the spare byte goes back to a. A file that grows while it is read,
or that reports no size (a pipe, a FIFO, a character device, a procfs
file), continues into a spill buffer that is copied once into a block
of exactly the bytes read. Nothing is zero-filled, and the peak is at
most 3n plus the first buffer. Reads start at the beginning of the
stream, so pipes and devices read too. The file is closed on every
path; an error from that close is not reported, because every byte is
already read.

### `ReadLink`

```
export ReadLink(a: &Arena, io: Io, p: str): Error|OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (Canceled, OutOfMemory, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

The target of the symbolic link at p, as stored, in a. Os(code) when p
is not a link: EINVAL on Linux, 4390 (ERROR_NOT_A_REPARSE_POINT) on
Windows. Utf8Error when the target is not UTF-8; NotFound when nothing
is at p; OutOfMemory when a cannot hold the target.

One runtime call when the target fits in 1 KiB, else one per buffer
size, doubling from 4 KiB; allocates the target in a, and for a long
target the larger buffers tried too.

### `ReadOnly`

```
export const ReadOnly: Options = DefaultOptions
```

Read an existing file; the same as DefaultOptions.

### `ReadText`

```
export ReadText(a: &Arena, io: Io, p: str): Error|OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (Canceled, OutOfMemory, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Reads the whole file at p into a as text: ReadBytes, then a check that
the bytes are UTF-8. Text that is not UTF-8 fails with the fs.Error
member Utf8Error. O(n); allocates as ReadBytes.

The UTF-8 check is one pass over the bytes, using SIMD where the CPU
has it.

### `ReadWrite`

```
export const ReadWrite: Options
```

Read and write an existing file, without truncating it.

### `Remove`

```
export Remove(io: Io, p: str): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Removes the file or empty directory at p. NotFound when nothing is at
p; any other failure is the OS's, as its fs.Error member. One runtime
call; allocates nothing.

### `RemoveAll`

```
export RemoveAll(io: Io, p: str): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Removes p and everything under it. It succeeds when nothing is at p,
and does nothing for an empty p. A file or a link at p is removed
itself, never a link's target, even with a trailing separator after it.
Every entry is removed relative to its open parent directory and no
link below p is followed, so another process that renames a directory
or swaps in a link cannot carry the removal out of the tree.

InvalidPath when p's last element is `.` or `..`, when p names only a
root, or when the tree is deeper than 1,024 levels or its names below p
pass 32 KiB at once. Denied, Os and the rest as the OS answers for the
first entry it cannot remove. Os with the OS's not-empty code when
directories are still not empty after 256 more listings in all, because
another process keeps filling them. Canceled when cancelled, with what
it removed gone.

Each directory is listed once, and again only when its removal finds it
not empty. O(entries) runtime calls: one per entry, about three per
directory (open, listing, removal), and up to three more for p (its
removal tried first, then its parent opened); allocates nothing, and
uses about 50 KiB of stack.

On Windows, when other calls or tasks remove the same tree at once
(another RemoveAll, or Remove of its entries), any of them can fail with
Denied, and when every one fails part of the tree can be left; Linux and
std.sim answer Ok to two RemoveAll calls. A call made once the others
have returned removes what is left, unless another handle holds an
entry of it open.

### `Rename`

```
export Rename(io: Io, src: str, dst: str): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Renames src to dst, replacing the file already at dst. NotFound when
src is missing; any other failure is the OS's, as its fs.Error member.
One runtime call; allocates nothing.

### `SetModified`

```
export SetModified(io: Io, p: str, t: time.Time): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Sets the last modification time of the file at p to t, following a
link; the access time is left as it is. NotFound when nothing is at p;
on Windows a time at or before 1601-01-01 is Os(0); any other failure
is the OS's, as its fs.Error member. One runtime call (utimensat on
Linux, SetFileInformationByHandle on Windows); allocates nothing.

### `SetPermissions`

```
export SetPermissions(io: Io, p: str, perm: Permissions): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Sets the mode bits of the file at p to perm.mode & 0o7777, following a
link (fchmodat on Linux). On Windows only the owner's write bit counts:
clearing it sets the read-only attribute, setting it clears the
attribute, and the other bits are ignored. NotFound when nothing is at
p; any other failure is the OS's, as its fs.Error member. One runtime
call; allocates nothing.

### `Stat`

```
export Stat(io: Io, p: str): Error!Info
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

The facts about the file at p, following links. NotFound when nothing
is at p; any other failure is the OS's, as its fs.Error member. One
runtime call; allocates nothing.

Under a simulated Io it takes two runtime calls, and permissions is 0:
a simulated file system has no mode bits.

### `StatLink`

```
export StatLink(io: Io, p: str): Error!Info
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

The facts about p itself, not following a link: a symbolic link is
reported as Kind.Link, with its own time and mode, not its target's,
and size 0 on both targets, as Entry reports it (Linux's lstat would
give the length of the target's path). Errors as Stat. One runtime call
(two under a simulated Io), as Stat; allocates nothing.

### `StatPath`

```
export StatPath(io: Io, p: &Path): Error!Info
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Stat for a Path that Walk or Dir.Next listed. It reads the path from its
bytes, so a name that is not UTF-8 is found (on Windows a name's
WTF-8). Errors as Stat. One runtime call (two under a simulated Io), as
Stat; allocates nothing.

### `Symlink`

```
export Symlink(io: Io, target: str, link: str): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Makes a symbolic link at link that points to target. target is stored
as written and need not exist; a relative target resolves from link's
directory. Exists when something is at link. Unsupported on Windows
without the privilege to make links (Developer Mode or an elevated
process). Any other failure is the OS's, as its fs.Error member. One
runtime call; allocates nothing.

### `TempDir`

```
export TempDir(a: &Arena, io: Io): Error|OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (Canceled, OutOfMemory, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

The directory for temporary files, in a. On Linux it is TMPDIR, else
/tmp. On Windows it is the first of TMP, TEMP, USERPROFILE and
SystemRoot that is set (GetTempPath2W's order), without a trailing
separator unless that separator follows the drive. The directory is
not checked to exist.

NotFound on Windows when none of the variables is set; OutOfMemory when
a cannot hold the path. O(environment) for the lookups; allocates the
path in a.

Under a simulated Io (a Sim's or a Tape's) no variable is read and the
answer is /tmp on every target, so a seed gives one run. A Recorder
over the native Io on Windows answers /tmp too, which the real file
system reads as `\tmp` on the working drive.

### `Unsupported`

```
export error Unsupported
```

An operation this target or this Io cannot do: a symbolic link without
the privilege on Windows, an Options set no target opens alike, a Copy
of something that is not a regular file, a WriteAt on a file opened
under append, or a call a simulated Io has no file system for.

### `Walk`

```
export Walk(h: &Heap, io: Io, root: str): Error|OutOfMemory!Walker from h
```

- Flows: The result borrows from h.
- Errors: Error|OutOfMemory (Canceled, OutOfMemory, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Starts a walk of the tree at root. The walk returns root first, at
depth 0, and then, when root is a directory, everything under it, depth
first, each directory's entries in the order the OS lists them. No link
is followed, root included: a link is returned as Kind.Link. Each
directory is opened inside its parent, so a rename above it cannot
carry the walk out of the tree.

NotFound when root is missing; any other failure is the OS's, as its
fs.Error member; OutOfMemory when h cannot hold root's path. One runtime
call (a stat that does not follow a link); allocates root's path on h.

A name is joined to its directory with the target's separator, and
with `/` on every target under a simulated Io (a Sim's or a Tape's), so
a seed gives one run.

### `WalkEntry`

```
export struct WalkEntry
```

One entry of a walk. path is the root's path, then each name below it
after a separator, in the OS's bytes (on Windows a name's WTF-8). kind
is what the entry is: Kind.Link for a link, which the walk does not
follow. depth is 0 for the root, 1 for its entries, and so on.

Fields:

- `path: Path`: The root's path, then each name below it after a separator.
- `kind: Kind`: What the entry is; Kind.Link for a link, which the walk does not follow.
- `depth: int`: 0 for the root, 1 for its entries, and so on.

### `Walker`

```
export linear view struct Walker
```

A depth-first walk of a directory tree, made by Walk. Walker is linear:
end it with Close. It holds one open directory per level of the entry
last returned, and its buffers on the heap it was made with.

### `Walker.Close`

```
export Walker.Close(self)
```

Ends the walk: closes every directory it holds and frees its buffers.
It cannot be cancelled and cannot fail. O(depth); allocates nothing.

### `Walker.Next`

```
export Walker.Next(mut self, a: &Arena, io: Io): Error|OutOfMemory!?WalkEntry from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (Canceled, OutOfMemory, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

The next entry of the walk, its path copied into a; none after the
last. A directory is entered by the next call, unless SkipDir comes
first.

Canceled when the scope is cancelled; the walk stays where it was, so a
later call tries the same step again. A directory that cannot be opened
or read fails once with the OS's error (Denied, ...), and the walk goes
on after it. OutOfMemory when a cannot hold the path or the walker's
heap cannot grow its buffers; the walk stays where it was, and the next
call returns the same entry.

O(1) runtime calls and O(path) copying per entry; allocates the path in
a, and grows the walker's buffers on its heap to the deepest path.

### `Walker.SkipDir`

```
export Walker.SkipDir(mut self)
```

Skips the directory last returned: the walk does not enter it. After an
entry that is not a directory it does nothing. O(1); allocates nothing.

### `Whence`

```
export enum Whence
```

Where File.Seek counts from.

Variants:

- `Start`: The start of the file.
- `Current`: The file's position.
- `End`: The end of the file.

### `WriteAll`

```
export WriteAll(io: Io, p: str, data: []u8): Error!void
```

- Errors: Error (Canceled, Utf8Error, Denied, Exists, InvalidPath, IsDirectory, NoSpace, NotDirectory, NotFound, Os(code: i64), Unsupported)

Writes data to the file at p, creating it or truncating it first, then
closes it. It does not sync the data to the device: open the file with
Create and call File.Sync for that. Fails with the fs.Error of the open,
a write or the close; the file is closed on every path. O(n); allocates
nothing.

Writes start at the beginning of the stream, so FIFOs and devices work
too, and a partial write is continued until every byte is written. A
write that makes no progress is Os(0) rather than an endless loop.

<!-- api:end -->
