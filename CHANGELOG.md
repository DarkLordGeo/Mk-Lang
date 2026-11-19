# Changelog

Every user-visible change to Mk, newest first. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## 0.1.0 (2026-11-19)

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

### Library

- A runtime written in Mk: the heap and arenas, a work-stealing M:N scheduler on IOCP (Windows) and io_uring with an epoll fallback (Linux), timers, limits, pinned threads, and panic reports with backtraces.
- The prelude: strings, slices, `List`, `Map`, `Arena`, `Io`, `System` and channels.
- Files, processes and pseudo-terminals, networking (TCP, UDP, Unix sockets, socket options), an HTTP/1.1 client and server, WebSocket, and TLS 1.3 (with a TLS 1.2 client) and x509, both Experimental.
- JSON, CSV, regex, text and Unicode, time with time zones, math, big numbers, URLs with IDNA, encodings with DER and PEM, compression (deflate, gzip, zlib, zstd decoding), hashes and crypto primitives (HMAC, HKDF, AES-GCM).
- Collections, sync primitives and atomics, SIMD routines, logging, command-line parsing, the terminal, embedded SQLite (`std.db.sqlite`), and `std.dl` for loading libraries at run time.
- `std.sim`: simulated `Io` with a seeded scheduler, and record and replay at the `Io` boundary.
- `std.testing`: fuzz targets and benchmarks.
- `kits/web`, a first-party web framework: routing through plain middleware functions, path and query parameters, JSON bodies and generated validation.
- Stability levels (Stable, Provisional, Experimental) on every standard library module.

### Tools

- `mk build`, `run`, `check` and `test` (with `--sim`, `--prop`, `--fuzz` and `--cover`), `bench`, `fmt`, `fix`, `explain`, `query`, `doc`, `apidiff`, `cimport`, `repl`, `cache` and `profile merge`.
- Packages: manifests in Mk (`pkg.mk`), path, URL and git dependencies chosen by minimal version selection, `pkg.sum`, workspaces, `replace`, test-only dependencies, grants for a dependency's unsafe code, `internal` modules, vendoring, and `mk new`, `install`, `remove`, `update`, `tree` and `why`.
- Build profiles: dev, release, `--profile size` and `--profile checked`; incremental, deterministic and reproducible builds; profile-guided optimization.
- Every diagnostic has a code; `mk explain` explains it, and `mk fix` applies the fixes that are certain.
- `mk lsp`, a language server, and a VS Code extension.

### Platforms

- x86_64 Windows, x86_64 Linux (static executables) and x86_64 freestanding.
- LLVM 21 or newer (`clang` and `lld`) and, to build `mk` from source, Go 1.27 or newer.

### Known issues

- `--cover` counts a block on its first statement's line, and a function that nothing reaches has no row, so totals cover the reached functions only.
- A static Mk library linked into a host DLL or `.so` treats that host library's image as its own when it decides whether a fault is its own.
- A static Mk library linked into a host that uses MSVC's C runtime reports duplicate `memcpy` and `memset` symbols.
- `std.json`'s parser reaches the stack limit before 512 levels of nesting in a dev build. Parse untrusted text in a release build, or bound its depth.
- A test dependency that depends back on the package that tests it is E0202 `cycle`.
- The symbol-clash check reads C sources without the preprocessor, so a definition inside a disabled `#if` block can cause a false clash.
- Not supported: object outputs, Windows resources, a system-libc dynamic link in a Linux library, two Mk libraries in one process, weak symbols, aliases and delay-loading.
