# fm

A two-pane terminal file manager and pager, in the manner of Midnight Commander, with every operation also available as a command for scripts. It shows Mk's terminal support (`std.term`), pseudo-terminals (`proc.Pty`), text measured by display width, and a UI session tested on a simulated machine.

## Running it

From the program's folder:

```
cd Dogfoods/fm
mk run -- ls cases/data/tree
```

```text
.
  docs/
    guide.txt  29
  notes.txt  739
  zeta.txt  5
```

With no command, `fm [LEFT [RIGHT]]` runs on the terminal with a directory in each pane. Its keys:

| Keys | Do |
|---|---|
| up/down, j/k, pgup/pgdn, home/end | Move |
| enter, right / backspace, left, h | Open / parent directory |
| tab | The other pane |
| c F5, m F6, d F8 | Copy, move, delete (each asks y/n; a directory that holds anything asks for its name) |
| n F7, l | New directory, link in the other pane |
| `!` | Run a shell command on a pseudo-terminal; its output opens in the pager |
| g, t, =, s, r | Go to, temp directory, same directory in both panes, sizes of both trees, reload |
| q F10, ctrl-c | Quit (exit 0), quit (exit 130) |

A paste goes to the pasted directory; a click selects a row and the wheel moves. Enter on a file opens the pager, which wraps at word boundaries by display width and re-wraps on a resize.

The commands do the same work from a pipe:

| Command | Does |
|---|---|
| `ls [--times] [--depth N] [--skip NAME] DIR` | Lists the tree under DIR |
| `view [--width N] [--mem BYTES] FILE` | Wraps FILE to N columns (80) |
| `cp [--yes] SRC DST`, `mv SRC DST`, `rm [--yes] PATH` | Copy, move, remove; `cp` and `rm` ask on standard input |
| `mkdir PATH`, `link TARGET LINK`, `stat PATH`, `touch [--bytes] TIME PATH` | Make a directory or link, describe a path, set a time (RFC 3339) |
| `du [--timeout MS] DIR...` | Tree sizes, one task each |
| `pwd`, `batch [FILE]` | The current directory; run commands one a line |
| `selftest --seed N` | A scripted UI session on a simulated machine, with its transcript |

`--zone local|utc|+HH:MM` sets how times show; `--transcript FILE` writes one line per terminal event. Exit codes: 0 done, 1 failed, 2 bad usage, 3 over the `--mem` budget, 124 past `--timeout`, 130 interrupted.

## Design

- The UI runs in raw mode on the alternate screen with bracketed paste, mouse and focus reports and the kitty keyboard protocol (`term.Open`); the terminal's modes are restored on every exit.
- Each event works in a frame arena reset before the next, and every pane rereads its directory (`fs.Walk`, `fs.StatLink`), so what fm shows is never older than the last key.
- `--transcript` records each event and the state it left (each pane as `[*]dir:selected#k/n`, or the prompt or pager view, then the status message). Terminal tests judge that file and the exit code, never the screen.
- `!` runs the command through the system shell on a pseudo-terminal (`proc.Pty`, `Command.terminal`): keys typed while it runs are passed to it, and a resize resizes it.
- `du` and `s` add up trees in tasks of their own; `selftest` runs the UI on `std.sim` with a read fault injected.
- The pager wraps by grapheme cluster and display width (`text.Width`), so wide characters count as two columns.

## Limits

- A pane rereads its directory for each event: fine for the directories people browse, slow for one of a hundred thousand entries.
- The pager holds a whole file (4 MiB at most) and wraps it again for each event; tabs count as no columns, as `text.Width` counts them.
- A shell command's output shows after it ends, not while it runs; after it ends, one key returns to fm.
- Paths are `str`, so a name that is not UTF-8 is listed (as `ls -b` shows it, `\xHH` for each byte outside printable ASCII) but cannot be opened. `touch --bytes` can make such a file but cannot set its time.

## Tests

The cases in `cases/` cover the commands, bad options and zones, the `--mem` budget and `--timeout`, a `batch` script, `selftest` under three seeds, and terminal sessions typed on a pseudo-terminal (navigation, the pager, copy, move, delete, a paste, a shell command, a resize, Ctrl+C). Run them with `go run ./tools/dogfood run -only fm` from the repository root.

## Section 18 lines

The standard-library lines this program uses, as `doc/reference.md` lists them; `go run ./tools/dogfood cover` reads this block.

```
// std.fs
fs.Walk(h: &Heap, io: Io, root: str): fs.Error|OutOfMemory!Walker from h
Walker.Next(mut self, a: &Arena, io: Io): fs.Error|OutOfMemory!?WalkEntry from a
Walker.SkipDir(mut self)
Walker.Close(self)
fs.MakeDirAll(io: Io, p: str): fs.Error!void
fs.RemoveAll(io: Io, p: str): fs.Error!void
fs.Copy(io: Io, src: str, dst: str): fs.Error!i64
fs.TempDir(a: &Arena, io: Io): fs.Error|OutOfMemory!str from a
fs.StatLink(io: Io, p: str): fs.Error!Info
fs.StatPath(io: Io, p: &Path): fs.Error!Info
fs.OpenPath(io: Io, p: &Path, o: Options): fs.Error!File
fs.Cwd(a: &Arena, io: Io): fs.Error|OutOfMemory!str from a
fs.Absolute(a: &Arena, io: Io, p: str): fs.Error|OutOfMemory!str from a
enum Kind {
struct WalkEntry {
struct Info {
// std.proc
Pty.Open(io: Io, cols: int, rows: int): proc.Error!Pty
Pty.Resize(&self, io: Io, cols: int, rows: int): proc.Error!void
Pty.Read(&self, io: Io, buf: mut []u8): proc.Error!int
Pty.WriteAll(&self, io: Io, data: []u8): proc.Error!void
Pty.Close(self)
struct Command {
// std.term
*
// std.text
text.WordBounds(s: str): Bounds
Bounds.Next(mut self): ?int
// std.time
Zone.Local(a: &Arena, io: Io): time.Error|OutOfMemory!Zone from a
Time.In(self, z: &Zone): Date
struct Date {
```
