# Mk documentation

[The README](../README.md) says what Mk is and how to build it. Start with the tour, then read the memory model. Use the rest as you need it.

## Learn the language

- [Overview](overview.md): a tour of the language, one feature at a time, for programmers who know C, Go or Rust. Start here.
- [Memory](memory.md): ownership, moves and borrowing. Each rule the compiler checks comes with an accepted program, a rejected one and the fix.
- [Cookbook](cookbook.md): short, complete programs for everyday tasks: files, maps, tasks, errors, HTTP, processes and C. Copy one and change it.
- [Examples](examples.md): complete programs, from a web service to a small kernel.

## Look things up

- [Reference](reference.md): the language and the standard library, section by section, ending with the prelude and every library signature.
- [Standard library](std/README.md): allocators, I/O, errors, naming, stability levels, and what each module is for.
- [Errors](errors.md): every error code, what triggers it and how to fix it. `mk explain E0503` prints one.
- [Grammar](grammar.md): the grammar in EBNF-style notation, for tool writers.

## Tools

- [Tools](tools.md): the `mk` command, the diagnostic format, `mk fix`, `mk query` and the JSON they write.
- [Packages](packages.md): manifests, dependencies, versions, `pkg.sum` and workspaces.
- [Editors](editors.md): `mk lsp` in VS Code and other editors.

## Writing larger programs

- [Style](style.md): how Mk code is written, and the rules `mk fmt` and `mk fix --canonical` apply.
- [Codebase](codebase.md): how to lay out a program of many modules, and how a library grows without breaking its users.

## Frameworks

- [web](../kits/web/README.md): middleware, path and query parameters, JSON bodies and validation on top of `std.http`.

## Background

- [Rationale](rationale.md): why Mk has the features it has, and what it takes from Rust, Zig and Go.
- [Compiler](compiler.md): how the compiler is built, for people who work on it.
- [Contributing](../CONTRIBUTING.md): how to build, test and change Mk.
