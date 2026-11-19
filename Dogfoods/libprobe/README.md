# libprobe

A tool that says what a machine's libraries load and export, in the manner of `ldd` and `dumpbin /exports`, for checking a deployment before an install. It shows Mk's run-time library loading (`std.dl`), calls through a looked-up address in `unsafe` code, a binary-format reader that checks every offset, per-target modules, and a system DLL named in `pkg.mk`'s `links`.

## Running it

From the program's folder:

```
cd Dogfoods/libprobe
mk run -- exports --summary --ordinals cases/data/tiny.pe
```

```text
tiny.dll: 5 functions, 4 named, ordinal base 1
    1  Add
    2  Forwarded -> KERNEL32.GetTickCount
    3  Multiply
    4  Version
    5  (by ordinal only)
```

The commands:

| Command | Does |
|---|---|
| `open LIB [SYMBOL...]` | Loads LIB as a program with plug-ins does, looks up each SYMBOL, releases it (`--keep` keeps it loaded) |
| `scan LIB...` | Tries each LIB under every search |
| `check FILE` | Checks a requirements file and reports what is missing |
| `call LIB SYMBOL [TEXT]` | Calls SYMBOL as `fn(): u32`, or with TEXT as `fn(*u8): i32` given TEXT NUL-terminated |
| `exports FILE` | Lists the names a PE file exports, forwarders and ordinal-only exports included (`--filter`, `--ordinals`, `--summary`, `--system`) |
| `version FILE...` | Prints each file's version resource (Windows) |

`--search` is `system` (the system's own libraries), `application` (the program's directory, then the system's) or `path` (an absolute path); with none, a name holding a separator is a path and any other name is `system`. The current directory and `PATH` are never searched. `call` takes the function's shape on trust: any shape other than the two above is undefined behaviour, as for any `unsafe` call.

A requirements file:

```
# what the installer needs
kernel32.dll system GetTickCount GetCurrentProcessId
version.dll application GetFileVersionInfoSizeW
C:/app/plugin.dll path PluginInit
```

Words are separated by spaces or tabs, `#` starts a comment, lines may end in `\r\n`, a symbol may be named once per library, and a file has at most 10,000 lines of at most 256 symbols each.

Exit status: 0 done, 1 a library, symbol or version is missing, 2 a bad command line, 3 not possible on this system, 4 an unreadable or malformed file.

## Design

- `open`, `scan`, `check` and `call` go through `std.dl`: open under a search, look up symbols, call one through its address, close or detach. `src/probe` is one of two `unsafe` modules.
- `exports` reads the PE export table from the file's bytes alone, checking every offset against the file before it reads it, so it answers on any system.
- `src/plat` is chosen per target in `pkg.mk`, so the Linux build names no Windows function.
- `version` reads the version resource through `version.dll`, which is not among the DLLs mk searches by itself, so `pkg.mk` names it in `links`.

## Limits

- On Linux, `std.dl` loads nothing outside system-libc mode, so `open`, `scan`, `check` and `call` answer `unsupported` with status 3, and so do `version` and `exports --system`; `exports` of a PE file works as on Windows.
- `exports` reads PE images only (no ELF), at most 65,536 functions, names of at most 512 bytes.
- `call` knows two function shapes.
- `version` prints the four numbers of the fixed file version. For system files Windows reports a compatibility version (6.2 rather than 10.0), because mk's executables declare OS version 6.0 in their header.
- No case reaches `InitFailed`: that needs a DLL whose initializer fails, which the cases cannot ship (no C and no binary code in the repository).

## Tests

The cases in `cases/` cover each command and its failures (a missing library or symbol, a bad requirements file, a truncated or foreign PE file), `exports` on a 1 KiB PE image with a forwarder and an ordinal-only export, and the `unsupported` answers on Linux. Run them with `go run ./tools/dogfood run -only libprobe` from the repository root.

## Section 18 lines

The standard-library lines this program uses, as `doc/reference.md` lists them; `go run ./tools/dogfood cover` reads this block.

```
// std.dl, Provisional. Library, Search and Error below are dl.Library, dl.Search and dl.Error
error NotFound
error Invalid
error InitFailed
error BadName
error Unsupported
error Os(code: i64)
type Error = NotFound | Invalid | InitFailed | BadName | Unsupported | Os
dl.Open(io: Io, name: str, search: Search): dl.Error!Library
Library.Symbol(&self, name: str): ?*u8
Library.Close(self, io: Io)
Library.Detach(self)
enum Search {
```
