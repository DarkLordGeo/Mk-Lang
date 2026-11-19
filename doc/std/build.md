# std.build

`std.build` gives a program two facts about its own build, both fixed at compile time: the bytes of a data file in the package (`build.File`), and the build profile (`build.Profile`). Reach for it to embed a template, a table or a banner in the executable instead of reading it at run time, or to keep debugging code out of a release build. To read files while the program runs, use [`std.fs`](fs.md).

Read [compile time](../reference.md#11-compile-time) in the reference first.

```mk
// verdict: parse
import std.build
```

## Contents

- [The main pieces](#the-main-pieces)
- [Profiles](#profiles)
- [Data files](#data-files)
- [Generators and compile-time limits](#generators-and-compile-time-limits)
- [Examples](#examples): [embed a file](#embed-a-file), [code for one profile](#code-for-one-profile), [paths the compiler refuses](#paths-the-compiler-refuses)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Embed a file's bytes in the program | `build.File(path)` in a `const` initializer |
| Read a file in a generator | `Gen.Input(path)` |
| Know which profile is being built | `build.Profile`, a `ProfileKind` |

## Profiles

`build.Profile` is a constant of type `ProfileKind`, set by `mk` for each build:

| Command | `build.Profile` |
|---|---|
| `mk build`, `mk run`, `mk check` | `Dev` |
| `mk build --release`, `mk run --release`, and `--profile size` | `Release` |
| `mk test` | `Test` |
| `mk build --profile checked`, `mk run --profile checked` | `Checked` |

Because it is a constant, an `if` on it removes the branch not taken: that code costs nothing in the build that skips it. A module that reads `build.Profile` is compiled once per profile, and may behave differently in each.

`Checked` is a release build that keeps the checks of a dev build. It is compiled and linked as `--release` is, with the same optimization, but contracts are checked, an error return records its trace, and code under `if build.Profile != build.ProfileKind.Release` stays. It takes no `--pgo` and reads no `default.mkprof`. `--release` with it is refused; `--symbols` works with it.

`Assert` and the implicit checks (overflow, bounds, `Cast`) run in every profile, `Release` included.

## Data files

`build.File(path)` returns the bytes of a file as a `[]u8` stored in the executable. It runs only at compile time: in a `const` or global initializer, or in a generator. A call anywhere else is E0705.

The path follows these rules, and breaking any of them is E0910 (`file-input`):

- It is relative to the root of the package that holds the calling module (the directory of its `pkg.mk`), not to the module's own directory, and written with `/`. A program run as one file with no `pkg.mk` reads relative to that file's directory.
- It has no `..`, `.` or empty element, no drive letter and no `\`, and it is not absolute.
- It names a regular file, reached through no link, of at most 64 MiB.

A change to a file read this way rebuilds exactly the modules that read it, and nothing else. `Gen.Input(path)` follows the same rules in a generator, relative to the package of the module that holds the `gen` line. A change to that file runs the `gen` line again.

## Generators and compile-time limits

The generator API and the limits of a compile-time evaluation are in the reference: [the generator API](../reference.md#the-generator-api), [quotes and splices](../reference.md#quotes-and-splices) and [fuel](../reference.md#fuel).

## Examples

### Embed a file

A package with a data file:

```text
banner/
    pkg.mk
    main.mk
    assets/banner.txt    "Mk 0.1" and a newline
```

```mk
// verdict: parse
import std.build

const Banner: []u8 = build.File("assets/banner.txt") // read once, at compile time

export Main(sys: &System): Utf8Error|WriteError!void {
    text := try str.From(Banner) // the bytes as they are in the file
    try sys.Print("{text}{n} bytes embedded\n", (text, n: Banner.Len()))
}
```

```text
$ mk run .
Mk 0.1
7 bytes embedded
```

- The program needs no file at run time: the 7 bytes are in the executable.
- Edit `assets/banner.txt` and run again: `mk` rebuilds `main.mk`, and the new text prints.
- `build.File` returns bytes. Check them with `str.From` when they must be UTF-8 text.

### Code for one profile

```mk
// verdict: ok
import std.build

ProfileName(): str {
    return match build.Profile {
        .Dev { "dev" }
        .Release { "release" }
        .Test { "test" }
        .Checked { "checked" }
    }
}

export Main(sys: &System): WriteError!void {
    try sys.Print("profile: {p}\n", (p: ProfileName()))
    if build.Profile != build.ProfileKind.Release {
        try sys.Print("extra checks on\n", ()) // left out of a release build
    }
}
```

```text
$ mk run profile.mk
profile: dev
extra checks on
$ mk run --release profile.mk
profile: release
$ mk run --profile checked profile.mk
profile: checked
extra checks on
```

- The `match` and the `if` are on a constant, so each build keeps one arm and drops the rest.
- `mk test` builds with `Test`, so a test can tell it runs under `mk test`:

```mk
// verdict: ok
import std.build

test "mk test builds in Test" {
    Assert(build.Profile == build.ProfileKind.Test)
}
```

### Paths the compiler refuses

```mk
// verdict: reject E0910
import std.build

const Secret: []u8 = build.File("../outside.txt") // <- E0910
```

```mk
// verdict: reject E0705
import std.build

// build.File runs only at compile time, so a function cannot call it.
Load(): []u8 {
    return build.File("data.txt") // <- E0705
}
```

- Files outside the package cannot be read: a package's build depends only on its own files.
- To choose a file by a value known at run time, embed each candidate in its own `const` and pick one, or read the file with [`std.fs`](fs.md).

## API

<!-- api:begin -->
Stability: Stable.

### `File`

```
export File(path: str): []u8
```

- Flows: The result borrows from path.

The bytes of the regular file at path, embedded in the program as static
data. path is relative to the root of the package that holds the calling
module, written with `/`, with no `..`, no absolute path and no drive
letter. The file must not be reached through a link and may hold at most 64
MiB; anything else is E0910. Compile time only: a call outside a const or
global initializer or a generator is E0705. Editing the file rebuilds
exactly the modules that read it.

### `Profile`

```
export const Profile: ProfileKind
```

The current build's profile, a compile-time constant. A module that reads it
is compiled separately for each profile, and may differ between them.

### `ProfileKind`

```
export enum ProfileKind
```

The profile a build runs in. `mk build`, `mk run` and `mk check` use Dev,
`--release` and `--profile size` use Release, `mk test` uses Test, and
`--profile checked` uses Checked: compiled and linked as Release is, with
contracts checked as in Dev.

Variants:

- `Dev`: The profile of mk build, mk run and mk check.
- `Release`: The profile of --release and --profile size.
- `Test`: The profile of mk test.
- `Checked`: The profile of --profile checked: compiled and linked as Release, with
  contracts checked as in Dev.

<!-- api:end -->
