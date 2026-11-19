# Mk

[![CI](https://github.com/zerodayokay/Mk-Lang/actions/workflows/ci.yml/badge.svg)](https://github.com/zerodayokay/Mk-Lang/actions/workflows/ci.yml)

Memory-safe systems programming without a garbage collector.

Mk is a small compiled language for software that has to be fast and correct: tools, servers, runtimes, kernels. The compiler checks ownership and borrowing, so a use after free or a data race does not compile, and it needs no lifetime annotations to do it. Allocation, errors and I/O are explicit: a function allocates, fails or touches the outside world only when its signature says so. Tasks run in structured scopes, and a test can replay a whole concurrent program deterministically from a seed.

```mk
// Count the words of a text, then print them by count.

struct Entry {
    word: str
    count: int
}

ByCount(a: &Entry, b: &Entry): bool {
    if a.count != b.count {
        return a.count > b.count
    }
    return a.word < b.word
}

// The result borrows from the arena and from the text: `from a, text` says so,
// and the compiler checks every caller against it.
Count(a: &Arena, text: str into a): OutOfMemory!List[Entry] from a, text {
    var counts := Map[str, int].New(a)
    for word in text.Words() {
        n := try counts.Ensure(word, 0)
        *n += 1
    }
    var out := List[Entry].New(a)
    for (key, value) in counts.Iter() {
        try out.Push(Entry(word: *key, count: *value))
    }
    out.Sort(ByCount)
    return out
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var arena := try Arena.New(sys.heap) // freed in one step at the end
    defer arena.Free()
    entries := try Count(&arena, "the cat saw the dog and the dog saw the cat")
    for e in entries {
        try sys.Print("{count} {word}\n", (count: e.count, word: e.word))
    }
}
```

```text
$ mk run words.mk
4 the
2 cat
2 dog
2 saw
1 and
```

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

## Why Mk

More and more code is written by AI agents and read by people. Mk is designed for both:

- **Small.** The whole language, borrowing rules included, fits in an agent's context window and in a person's head. A feature gets in only if it pays for itself; most sugar and cleverness does not.
- **Explicit.** Allocation, errors, I/O and borrowing show in signatures. You never have to read a callee's body to know whether it allocates, fails or keeps a reference.
- **One way to write each thing.** No macros, no inheritance, no implicit conversions, no hidden control flow. Fewer choices mean fewer ways to be wrong.
- **Errors that lead to the fix.** Every diagnostic has a code, `mk explain` explains it, and `mk fix` applies the fixes that are certain.

Code that is plain for a person to review is also code an agent gets right on the first try.

## Building from source

You need Go 1.27 or newer and LLVM 21 (`clang` and `lld`).

```sh
git clone https://github.com/zerodayokay/Mk-Lang
cd Mk-Lang
go build -o mk ./cmd/mk        # on Windows: go build -o mk.exe ./cmd/mk
./mk version
```

Put `mk` on your `PATH`. `mk version` shows the LLVM it found. `--llvm DIR` or `MK_LLVM` names the directory; otherwise mk looks in the default install location (`C:\Program Files\LLVM\bin` on Windows, `/usr/lib/llvm-21/bin` on Linux) and on `PATH`, in the order [LLVM and toolchains](doc/tools.md#11-llvm-and-toolchains) gives. On Windows, use the installer from the LLVM releases page; on Ubuntu and Debian, install `clang-21` and `lld-21` from apt.llvm.org.

## Hello, world

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

## Editor support

`mk lsp` is the language server built into `mk`: diagnostics, hover, completion, go to definition, references, rename, formatting and code actions in any editor that speaks LSP. [doc/editors.md](doc/editors.md) sets it up in VS Code, Neovim, Helix, Emacs, Sublime Text and other editors.

## Documentation

- [Overview](doc/overview.md): a tour of the language. Start here.
- [Memory](doc/memory.md): ownership, borrowing and the rules the compiler checks.
- [Reference](doc/reference.md): the language, rule by rule.
- [Standard library](doc/std/README.md): what each module is for, and the conventions they share.
- [Errors](doc/errors.md): every error code and how to fix it. `mk explain E0503` prints one.
- [Contributing](CONTRIBUTING.md): how to build, test and change Mk itself.

[doc/README.md](doc/README.md) lists every document: the cookbook, examples, tools, packages, style, rationale and the compiler.

## Platforms

Windows and Linux on x86_64, and freestanding x86_64. macOS and AArch64 are not supported.

## License

MIT. See [LICENSE](LICENSE).
