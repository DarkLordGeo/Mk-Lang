# Mk

[![CI](https://github.com/zerodayokay/Mk-Lang/actions/workflows/ci.yml/badge.svg)](https://github.com/zerodayokay/Mk-Lang/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/zerodayokay/Mk-Lang)](https://github.com/zerodayokay/Mk-Lang/releases/latest)

Mk is a small compiled language for memory-safe systems programming without a garbage collector.

## Documentation

| | |
|---|---|
| [Overview](doc/overview.md) | A tour of the language, one feature at a time. Start here. |
| [Memory](doc/memory.md) | Ownership, borrowing and the rules the compiler checks. |
| [Reference](doc/reference.md) | The language, rule by rule. |
| [Standard library](doc/std/README.md) | What each module is for, the conventions they share, and [what a 0.x release may change](doc/std/README.md#9-stability). |
| [Errors](doc/errors.md) | Every error code and how to fix it. `mk explain E0503` prints one. |
| [Tools](doc/tools.md) | The `mk` command: build, run, test, format, fix, query. |
| [Packages](doc/packages.md) | Manifests, dependencies and workspaces. |
| [Editors](doc/editors.md) | `mk lsp` in VS Code, Neovim, Helix, Emacs and others. |
| [Cookbook](doc/cookbook.md) and [Examples](doc/examples.md) | Short programs for everyday tasks, and complete programs from a web service to a small kernel. |
| [All documents](doc/README.md) | Style, codebase layout, grammar, rationale and the compiler. |

## Why Mk

Mk is for software that has to be fast and correct: tools, servers, runtimes, kernels. The compiler checks ownership and borrowing, so a use after free or a data race does not compile, and it needs no lifetime annotations to do it. Tasks run in structured scopes, and a test can replay a whole concurrent program deterministically from a seed.

More and more code is written by AI agents and read by people. Mk is designed for both:

- **Small.** The whole language, borrowing rules included, fits in an agent's context window and in a person's head. A feature gets in only if it pays for itself.
- **Explicit.** Allocation, errors, I/O and borrowing show in signatures. You never read a callee's body to know whether it allocates, fails or keeps a reference.
- **One way to write each thing.** No macros, no inheritance, no implicit conversions, no hidden control flow. Fewer choices mean fewer ways to be wrong.
- **Errors that lead to the fix.** Every diagnostic has a code, `mk explain` explains it, and `mk fix` applies the fixes that are certain.

Code that is plain for a person to review is also code an agent gets right on the first try.

## A taste of Mk

`Long` returns the words of `text` longer than `n` bytes, in a list allocated in the arena `a`:

```mk
Long(a: &Arena, text: str into a, n: int): OutOfMemory!List[str] from a, text {
    var out := List[str].New(a)
    for word in text.Words() {
        if word.Len() > n {
            try out.Push(word)
        }
    }
    return out
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var arena := try Arena.New(sys.heap) // freed in one step at the end
    defer arena.Free()
    words := try Long(&arena, "the quick brown fox jumps over the lazy dog", 4)
    for w in words {
        try sys.Print("{w}\n", (w: w))
    }
}
```

```text
$ mk run long.mk
quick
brown
jumps
```

`OutOfMemory!List[str]` is the signature's promise: `Long` fails only with `OutOfMemory`, and `try` passes the error up. `from a, text` says the result borrows from the arena and the text, and `into a` lets the body store words of `text` in the arena's list. The compiler checks the body and every caller against the signature.

Free the arena while something still borrows from it, and the program does not compile:

```mk
export Main(sys: &System): OutOfMemory|WriteError!void {
    var arena := try Arena.New(sys.heap)
    names := List[str].New(&arena) // names borrows the arena
    arena.Free()                   // error: the arena is still borrowed
    try sys.Print("{n}\n", (n: names.Len()))
}
```

```text
$ mk run freed.mk
freed.mk:4:5: E0503 move-while-borrowed: arena conflicts with a live shared loan held by names
    |     arena.Free()                   // error: the arena is still borrowed
    |     ^^^^^^^^^^^^
    loan: arena (shared) at freed.mk:3:28
    holder: names
    signature: Free(self) ends self
    explain: mk explain E0503
1 error
```

## Getting started

### Install

A release bundle holds mk, the clang and lld it was tested with, and the documentation, so it needs neither Go nor LLVM. On Windows, in PowerShell:

```powershell
irm https://github.com/zerodayokay/Mk-Lang/releases/latest/download/install.ps1 | iex
```

On Linux (with curl or wget, and OpenSSL 3 or later for the signature check):

```sh
curl -fsSL https://github.com/zerodayokay/Mk-Lang/releases/latest/download/install.sh | sh
```

The script checks the release's signature against the release key written into it, and the archive against its SHA-256, before it installs anything. It unpacks the newest release under `%LOCALAPPDATA%\mk` or `~/.local/share/mk` and puts mk on your `PATH` (the user `PATH` on Windows, `~/.profile` on Linux). Open a new terminal and run `mk version`.

For a given release, set `$env:MK_VERSION = '0.1.0'` before the Windows line, or end the Linux line with `| sh -s -- --version 0.1.0`. `mk upgrade` moves to a newer release later ([mk upgrade and mk toolchain](doc/tools.md#mk-version-mk-upgrade-and-mk-toolchain)). To read a script before you run it, download it from the same address, or take it from `tools/install/` at the release's tag.

### Hello, world

```mk
export Main(sys: &System): WriteError!void {
    try sys.Print("hello, world\n")
}
```

Save it as `hello.mk`:

```sh
mk run hello.mk                # build and run
mk build --release hello.mk    # an optimized executable
```

A program of more than one file is a package:

```sh
mk new hello                   # writes hello/pkg.mk and hello/src/main.mk
cd hello
mk run                         # prints "hello from hello"
```

`mk test` runs the `test` blocks, `mk fmt` formats, `mk check` checks without building. [The `mk` command](doc/tools.md) lists every command, and [Packages](doc/packages.md) covers manifests and dependencies.

### Building from source

You need Go 1.27 or newer and LLVM 21 or newer (`clang` and `lld`).

```sh
git clone https://github.com/zerodayokay/Mk-Lang
cd Mk-Lang
go build -o mk ./cmd/mk        # on Windows: go build -o mk.exe ./cmd/mk
./mk version
```

Put `mk` on your `PATH`. `mk version` shows the LLVM it found. `--llvm DIR` or `MK_LLVM` names the directory; otherwise mk looks in the default install location (`C:\Program Files\LLVM\bin` on Windows, `/usr/lib/llvm-21/bin` on Linux) and on `PATH`, in the order [LLVM and toolchains](doc/tools.md#11-llvm-and-toolchains) gives. On Windows, use the installer from the LLVM releases page; on Ubuntu and Debian, install `clang-21` and `lld-21` from apt.llvm.org.

## Editor support

`mk lsp` is the language server built into `mk`: diagnostics, hover, completion, go to definition, references, rename, formatting and code actions in any editor that speaks LSP. [doc/editors.md](doc/editors.md) sets it up in VS Code, Neovim, Helix, Emacs, Sublime Text and other editors.

## Platforms

Mk builds programs for Windows and Linux on x86-64, and for freestanding x86-64 (no operating system). It does not build for macOS or AArch64; both are planned. [Targets](doc/reference.md#targets) lists every target and what `mk check` and `mk build` do on each.

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) covers how to build, test and change Mk itself.

## Security

Report a vulnerability as [SECURITY.md](SECURITY.md) describes.

## License

MIT. See [LICENSE](LICENSE).
