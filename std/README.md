# std: the source of the standard library

This directory holds the standard library, the runtime and the system layer, all written in Mk. `embed.go` embeds every file in the `mk` binary, so a program builds with nothing installed beside `mk`. This page is for people who change std. The library's documentation is in [doc/std/](../doc/std/README.md), and how to build and test Mk is in [CONTRIBUTING.md](../CONTRIBUTING.md).

## Layout

Each module `std.x` is the directory `x/`, and a submodule `std.x.y` is `x/y/`. A directory's `.mk` files form one module.

| Directory | Holds |
|---|---|
| `core/` | `std.core`: the prelude and the types and calls the other modules share. Only std imports it; a program sees its prelude names |
| `raw/` | `std.raw`: the compiler intrinsics, declared with no body |
| `target/` | `std.target`: the target's description, re-exported by `std.core` |
| `rt/`, `task/`, `sys/` | The runtime: the program's entry, the scheduler and tasks, and the system calls. `sys/windows`, `sys/linux` and `sys/nutshell` are one module each, chosen by the target |
| `free/` | `std.free`, and `free/sys`, the system layer of the freestanding target |
| `pkg/` | `std.pkg`: the types a `pkg.mk` manifest is written in |
| `corpus/` | `testprelude.mk`, the test prelude of [reference section 19](../doc/reference.md#19-test-prelude) |
| `testdata/` | Inputs of the Go tests |
| every other directory | One public module: `fs/` is `std.fs`, `db/sqlite/` is `std.db.sqlite` |

A module imports only from its own layer and the layers below ([layers](../doc/std/README.md#1-layers)); `internal/stdtest` checks it. Inside `core/`, a file's prefix is its layer:

| Prefix | Layer |
|---|---|
| `core_` | 0 |
| `mem_`, `coll_`, `text_` | 1 |
| `io_`, `sync_` | 2 |
| `tests_` | Test blocks, run by `mk test --std std.core` |

A function with no body in a std module is an intrinsic, which the compiler supplies.

## The manifest

`pkg.mk` is std's own manifest. `targets` maps `sys` and `linux` to the directories each OS uses, and `unsafe_modules` lists the directories whose code may hold `unsafe` blocks. A module not listed there cannot write `unsafe`, in std as in any package.

## Documentation

Each item's `///` comment is its documentation: `mk doc --std std.<module>` prints it, and the API section of `doc/std/<module>.md` is generated from it. After changing a `///` comment, regenerate the pages with `go test ./internal/driver -run TestStdPages -update`.
