# Changelog

Every user-visible change to Mk, newest first. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). What a 0.x release may change, at each stability level, is in [stability](doc/std/README.md#9-stability).

## 0.1.0 (2026-11-20)

The first public release of Mk: a small, memory-safe systems language with no garbage collector, compiled by a Go compiler that emits LLVM IR, with a runtime and standard library written in Mk and no C runtime.

### Language

- The language of [the reference](doc/reference.md) and [the memory model](doc/memory.md): ownership, moves, linear values and borrowing checked from signatures alone, with `from` and `into` clauses instead of lifetime parameters.
- Error sets in every signature that can fail, `try`, `fail`, `else` and `onerr`; no exceptions, and integer overflow traps in every build.
- Structs, enums, open sets (`open enum`, `open type E = A | B`), interfaces with explicit `impl`, generics with const parameters, `if` and `match` as values, range patterns, and `for` over iterators with record destructuring.
- Capturing function literals that cannot escape, generic instances as `fn` values, and `become f(args)`, a guaranteed tail call.
- Contracts: `requires`, `ensures` and `invariant`.
- Compile-time evaluation and generators (`gen`, `quote`), which can read the package's data files.
- Structured concurrency: tasks in scopes, channels, cancellation as an error, and deadlines.
- Systems features: `unsafe` blocks with a stated reason, raw pointers, SIMD vectors with `cpu` dispatch, inline `asm`, `naked` functions, `align(n)`, `section` and `extern var`.
- C interop: `import c.<name>` through Clang, `c_sources` linked against `std.libc` (written in Mk), linking DLLs, shared objects and static archives, and building a C-callable dynamic or static library with a generated header.
- A method declared without `export` is private to its module: calling it from another module (`x.M()`, `T.M(..)`, `T.M` as a value, `T.M[..]`, or a `for` loop's `Next`) is E0204, as for a private function. std's `json.Codec`, `csv.Codec`, `cli.Args` and `regex.Matcher` generators, and the web kit's `Body` and `Validate`, export the methods they write when the type is exported. A call from another module to a method a third-party generator writes without `export` does not build either: a generator should write `export` on the methods of an exported type.
- An operator whose operands are all integer literals is E0303 when the run-time operator would panic: `x: u8 = 200 + 100`, `0 - 1` as an unsigned type, a division by a literal zero, a shift past the width. The check does not depend on whether the code runs. The wrapping operators are unaffected, and initializers evaluated at compile time report E0901.
- The constant folder reports the type's minimum divided by -1, with `/` or `%`, as `divide by zero` (E0901), as the compile-time evaluator does.
- `fuel Limit` with a named const uses that const's value; any other fuel expression is E0705. `unsafe " "` (a reason of only white space) is E0101, as `unsafe ""` is.
- A variant, `some`, `ok` or `err` pattern on the left of `:=` is E0306 (a type parameter or primitive there is E0301); a same-module const that reads a same-module layout with `SizeOf` or `AlignOf`, named in a type, is E0908; a field of an indexed named const (`S[0].a`) as an array length is E0705, as `P.a` is.
- A store of a static through a parameter's referents is E0604 past the loan checker's bound too: also when the referents include more than 64 instances of one generic type, or more than 64 records holding distinct instances of one.

### Library

- A runtime written in Mk: the heap and arenas, a work-stealing M:N scheduler on IOCP (Windows) and io_uring with an epoll fallback (Linux), timers, limits, pinned threads, and panic reports with backtraces.
- A panic's backtrace shows the program's frames and std's, ending at Main, at the task's function or at the test (`test "name"`): the runtime's start, its support routines (printed as raw addresses) and the compiler's wrapper frames (`Main#report`, the test runner's Main) are left out.
- A dev build prints the error-return trace of an error that leaves Main, also in a program without tasks that never uses `sys.io`.
- The prelude: strings, slices, `List`, `Map`, `Arena`, `Io`, `System` and channels.
- Files, processes and pseudo-terminals, networking (TCP, UDP, Unix sockets, socket options), an HTTP/1.1 client and server, WebSocket, and TLS 1.3 (with a TLS 1.2 client) and x509, both Experimental.
- std.net: `Dial` resolves a host name. On Linux it reads `/etc/hosts`, then the nameservers, and dials each address in order, failing with the first one's error; on Windows it dials the system's first IPv4 answer, else its first IPv6 one. `Dial` of a host name that does not resolve fails with `InvalidAddress`, as `Resolve` does (on Linux, on Windows and under std.sim).
- std.http: a chunked request body's buffer doubles as it grows, so its arena use is linear in the body, not quadratic in the chunk count, and one-byte chunks cannot use far more than `body_bytes`.
- JSON, CSV, regex, text and Unicode, time with time zones, math, big numbers, URLs with IDNA, encodings with DER and PEM, compression (deflate, gzip, zlib, zstd decoding), hashes and crypto primitives (HMAC, HKDF, AES-GCM).
- `str.ToLower` and `str.ToUpper` follow Unicode 18.0.0, the version of std.text's and regex's tables, including the case mappings of U+0277 and U+A7DD, U+AB4B and U+AB6C, U+1DF40 and U+1DF41 and the other scalars Unicode 18.0.0 gave case mappings. `White_Space` is generated from UCD 18.0.0 too.
- Collections, sync primitives and atomics, SIMD routines, logging, command-line parsing, the terminal, embedded SQLite (`std.db.sqlite`), and `std.dl` for loading libraries at run time.
- std.term: on a Windows console, and under std.sim on Windows, Ctrl+Space (a NUL key) reads as `Char(' ')` with ctrl.
- Allocators in `std.mem`. `FixedHeap` is a general-purpose allocator over a caller's buffer: any block is freed and reused, and an allocation, a resize and a free are each O(1). `Fallback` serves from a buffer while requests fit it and from another allocator once they do not. `Limit` refuses what would take the bytes live through another allocator past a limit. `Failing` fails the requests a test picks by number, to reach the code that handles `OutOfMemory`.
- `std.sim`: simulated `Io` with a seeded scheduler, and record and replay at the `Io` boundary.
- `std.testing`: fuzz targets and benchmarks.
- `kits/web`, a first-party web framework: routing through plain middleware functions, path and query parameters, JSON bodies and generated validation.
- [Stability levels](doc/std/README.md#9-stability) (Stable, Provisional, Experimental) on every standard library module.

### Tools

- `mk build`, `run`, `check` and `test` (with `--sim`, `--prop`, `--fuzz` and `--cover`), `bench`, `fmt`, `fix`, `explain`, `query`, `doc`, `apidiff`, `cimport`, `repl`, `cache` and `profile merge`.
- Packages: manifests in Mk (`pkg.mk`), path, URL and git dependencies chosen by minimal version selection, `pkg.sum`, workspaces, `replace`, test-only dependencies, grants for a dependency's unsafe code, `internal` modules, vendoring, and `mk new`, `install`, `remove`, `update`, `tree` and `why`.
- Build profiles: dev, release, `--profile size` and `--profile checked`; incremental, deterministic and reproducible builds; profile-guided optimization.
- Every diagnostic has a code; `mk explain` explains it, and `mk fix` applies the fixes that are certain.
- Diagnostics for other languages' habits name the Mk form: a float written `1.` or `.5`; `G<int>(x)`; `Point{x: 1}` and Zig's `Point{ .x = 1 }`; Go's `F() int`; `c ? a : b`; `v, ok := m[k]`; `x, err := F()`; Zig's `.{x}`; `dyn I`. `.unwrap()`, `.expect()`, `.clone()` and `.to_string()` are E1221 naming the Mk form, and `mk fix` renames `.unwrap()` to `.Must()`.
- E0303 names `Trunc` only for a conversion between two integers, and E0705 at a run-time array length prints no empty `param` fact.
- `mk check`, `mk fix` and `mk lsp` recompile a proven `into` or `from` clause, following the callers' own clauses when the error moves up. A clause whose chain ends in another error (an E0505 or E0503 no clause clears), or which was not recompiled within the bound, is offered as an alternative naming that error, and `mk fix` never writes it.
- `mk check` handles a polymorphically recursive type whose recursion goes through a record (`next: ?&(a: G[?T], b: int)`) without taking minutes.
- `mk build -o dir/`, or `-o` naming an existing directory, writes `dir/<bin>[.exe]` for each bin. `mk doc std.fs` documents std's module, as `mk doc --std std.fs` does, and an unknown `std.` module is an error. `mk test --fuzz` output agrees in number ("1 input saved").
- `mk query find unwrap` lists `?T.Must` and `E!T.Must`; `mk query members Eq` (Hash, Format) answers the prelude interface; in a package that does not parse, `find` and `members` answer std's names with a note.
- `mk lsp`, a language server, and a VS Code extension.

### Platforms

- x86_64 Windows, x86_64 Linux (static executables) and x86_64 freestanding.
- `--target` with a target std has no system layer for (such as `aarch64-macos`) is refused with one E0202, `reason: no-std-target`, naming the targets `mk` builds for and those it only checks.
- LLVM 21 or newer (`clang` and `lld`) and, to build `mk` from source, Go 1.27 or newer.

### Known issues

- `--cover` counts a block on its first statement's line, and a function that nothing reaches has no row, so totals cover the reached functions only.
- A static Mk library linked into a host DLL or `.so` treats that host library's image as its own when it decides whether a fault is its own.
- A static Mk library linked into a host that uses MSVC's C runtime reports duplicate `memcpy` and `memset` symbols.
- `std.json`'s parser reaches the stack limit before 512 levels of nesting in a dev build. Parse untrusted text in a release build, or bound its depth.
- A test dependency that depends back on the package that tests it is E0202 `cycle`.
- The symbol-clash check reads C sources without the preprocessor, so a definition inside a disabled `#if` block can cause a false clash.
- Not supported: object outputs, Windows resources, a system-libc dynamic link in a Linux library, two Mk libraries in one process, weak symbols, aliases and delay-loading.
