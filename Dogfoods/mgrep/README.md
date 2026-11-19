# mgrep

A recursive search tool in the manner of ripgrep. It walks directories in a fixed order, skips binary files, searches files in parallel tasks, and prints matches with line numbers, context, columns and colour, in the same order whatever the number of carriers. It shows Mk's `std.simd` byte searches, `gen regex.Matcher`, channels and semaphores, and per-task arenas.

## Running it

From the program's folder:

```
cd Dogfoods/mgrep
mk run -- -w needle cases/data/tree/a.txt
```

```text
1:alpha needle
3:this line is longer than sixteen bytes and ends with needle
6:needles are not a word needle
8:last line without newline needle
```

```
mgrep [OPTIONS] PATTERN [PATH...]
mgrep [OPTIONS] -e PATTERN [PATH...]
mgrep [OPTIONS] -f FILE [PATH...]
```

`mgrep --help` lists every option. The common ones have grep's short forms:

| Option | Does |
|---|---|
| `-i`, `-F`, `-w`, `-v` | Ignore case, fixed strings, whole words, invert |
| `-c`, `-l`, `-o` | Count lines, file names only, only the matches |
| `-n` / `-N` | Line numbers on or off |
| `-A`, `-B`, `-C N` | Context after, before, around |
| `-m N`, `-q` | Stop a file after N matching lines; quiet, exit at the first match |
| `-r TEXT` | Replace each match, with `$1` and `$name` |
| `-g GLOBS`, `-d N`, `-a`, `-.` | Globs, depth, binary files too, hidden names |
| `-j N` | Search tasks |

Exit status: 0 when a line matched, 1 when none did, 2 on any error (a quiet search that matched exits 0).

## Design

- `Compile` picks the cheapest search for the patterns: a literal (first byte by `simd.IndexByte`, both cases by `simd.IndexAny`, the rest by `simd.Equal`), a byte class such as `[;#]` (`simd.IndexAny`), or a `std.regex` program. The shapes are recognised by matchers that `gen regex.Matcher` builds at compile time.
- `Search` skips lines that cannot match with vector searches and counts the newlines it skips with `simd.Count`; before-context waits in a `coll.Deque`, and columns are counted in terminal cells with `text.Width`.
- The walk sorts each directory's entries by their bytes, so every run lists the same files in the same order; `.mgrepignore` files and globs filter them.
- A feeder task hands file indexes to the search tasks through a `Chan`, a `sync.Semaphore` bounds the open files, each task searches in its own arena, and the main task prints reports in file order, holding early ones in a `HeapMap`. `-q` cancels the scope at the first match.
- Files up to 64 KiB are read in one call; a larger one has its first 1,024 bytes sniffed with `File.ReadAt`, and a NUL byte there marks it binary. Lines that are not UTF-8 (`simd.ValidUtf8`) are skipped and counted.
- Options are parsed by `gen cli.Args`; short options are expanded to long ones by hand.

## Limits

- Lines are searched as UTF-8 text; a regex never matches inside a line that is not UTF-8, and such lines are skipped.
- `-m N` stops at the Nth matching line and prints no context after it.
- One `--glob` value, with globs separated by commas; a glob matches base names, with `*` and `?` over bytes.
- `--max-filesize` and the binary sniff look at the size and the first 1,024 bytes when the file is listed.
- No memory limit option: the program holds one file per search task, and the reports that wait for their turn.

## Tests

The cases in `cases/` cover every option, regex errors, binary and invalid UTF-8 files, standard input, the same search at 1 and 16 carriers with byte-identical output, and denied reads and opens on a simulated disk. Run them with `go run ./tools/dogfood run -only mgrep` from the repository root.

## Section 18 lines

The standard-library lines this program uses, as `doc/reference.md` lists them; `go run ./tools/dogfood cover` reads this block.

```
Path.New(a: &Arena, bytes: []u8): OutOfMemory!Path from a
Path.AsBytes(&self): []u8 from self.bytes
Path.Str(&self): ?str from self.bytes
Path.Join(&self, a: &Arena, part: str): OutOfMemory!Path from a
Path.Base(&self): Path from self.bytes
Path.Parent(&self): ?Path from self.bytes
Path.Format(&self, w: mut Writer): WriteError!void
Reader.Read(mut self, buf: mut []u8): ReadError!int
Reader.ReadAll(mut self, buf: mut []u8): ReadError!int
Reader.Line(mut self, a: &Arena): ReadError|OutOfMemory!?str from a
Reader.Until(mut self, delim: u8, buf: mut []u8): ReadError!int
// std.fs. File and Error below are fs.File and fs.Error
type Error = NotFound | Exists | Denied | IsDirectory | NotDirectory | NoSpace | InvalidPath | Utf8Error | Os | Canceled | Unsupported
fs.ReadText(a: &Arena, io: Io, p: str): fs.Error|OutOfMemory!str from a
fs.ReadBytes(a: &Arena, io: Io, p: str): fs.Error|OutOfMemory![]u8 from a
fs.Open(io: Io, p: str): fs.Error!File
File.Read(mut self, io: Io, buf: mut []u8): fs.Error!int
File.ReadAt(&self, io: Io, pos: i64, buf: mut []u8): fs.Error!int
File.Reader(mut self, io: Io, buf: mut []u8): Reader from self, buf
File.Close(self, io: Io): fs.Error!void
File.Abandon(self)
fs.Stat(io: Io, p: str): fs.Error!Info
Dir.Open(io: Io, p: str): fs.Error!Dir
Dir.Next(mut self, a: &Arena, io: Io): fs.Error|OutOfMemory!?Entry from a
Dir.Close(self)
struct Entry {
struct Info {
// std.sync. Shared below is sync.Shared
Chan[T].New(a: &Alloc, capacity: int): OutOfMemory!Chan[T]
Chan[T].Send(&self, io: Io, x: T into self): Canceled|Closed!void
Chan[T].Recv(&self, io: Io): Canceled|Closed!T
Chan[T].Close(&self)
Chan[T].Free(self, a: &Alloc)
Semaphore.New(permits: int): Semaphore
Semaphore.Acquire(&self, io: Io, n: int): Canceled!void
Semaphore.TryAcquire(&self, n: int): bool
Semaphore.Release(&self, n: int)
// std.simd. Vec, Mask, Lane, CpuFeatures and Choice below are simd.Vec, simd.Mask, simd.Lane, simd.CpuFeatures and simd.Choice
simd.IndexByte(s: []u8, b: u8): ?int
simd.IndexAny(s: []u8, set: []u8): ?int
simd.Count(s: []u8, b: u8): int
simd.Equal(a: []u8, b: []u8): bool
simd.Compare(a: []u8, b: []u8): int
simd.ValidUtf8(s: []u8): bool
// std.coll
Deque[T].New(a: &Arena): Deque[T]
Deque[T].Push(mut self, x: T into self): OutOfMemory!void
Deque[T].Pop(mut self): ?T
Deque[T].PopFront(mut self): ?T
Deque[T].Get(&self, i: int): ?&T
Deque[T].Len(&self): int
// std.text
text.Width(s: str): int
// std.regex. Regex and Error below are regex.Regex and regex.Error
*
// std.cli. Error below is cli.Error
*
```
