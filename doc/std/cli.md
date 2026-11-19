# std.cli

`std.cli` turns a struct into a command-line parser. You declare the options as exported fields and write `gen cli.Args(T)`. The compiler then writes `T.Parse`, which reads the arguments into a `T`, and `T.Usage`, which prints the help from the fields' `///` comments. Reach for it for the flags and options of a tool; for a single argument, read `sys.args` directly.

Read the [standard library guide](README.md) first, and the [Parse command-line options](../cookbook.md#parse-command-line-options) recipe for the shortest complete program.

```mk
// verdict: parse
import std.cli
```

## Contents

- [The main pieces](#the-main-pieces)
- [Fields and options](#fields-and-options)
- [Parsing rules](#parsing-rules)
- [Help](#help)
- [Errors](#errors)
- [Memory and cost](#memory-and-cost)
- [Examples](#examples): [parse a command line](#parse-a-command-line), [print help](#print-help), [explain an error](#explain-an-error)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Declare the options | A struct with exported fields, one per option, each with a `///` comment |
| Write the parser | `gen cli.Args(T)` |
| Parse the arguments | `T.Parse(a, args)`, usually with `sys.args[1..]` |
| Print the help | `T.Usage(w)` |
| Handle a bad command line | `cli.Error`: `Unknown`, `Invalid`, `Missing`, `OutOfMemory` |

`gen cli.Args(T)` emits two functions next to `T`:

```text
T.Parse(a: &Arena, args: []str): cli.Error!T from a, args
T.Usage(w: mut Writer): WriteError!void
```

## Fields and options

Each exported field of `T` is one argument. Its type says which kind:

| Field type | Argument | Given as |
|---|---|---|
| `bool` | A flag, false unless given | `--name`, `--name=true`, `--name=false` |
| `int`, `str` | A required option | `--name V` or `--name=V` |
| `?int`, `?str` | An optional option, `none` unless given | `--name V` or `--name=V` |
| `[]str` | The positional arguments (at most one such field) | anything not an option |

The option's name is the field's, with `_` written `-`: the field `dry_run` is `--dry-run`. Every field of `T` must be exported, since `Parse` builds the whole `T`. A field that is not exported, or has any other type, is an `E0903` error at that field.

## Parsing rules

- An argument that does not start with `-`, and `-` alone, is a positional.
- `--` ends the options: every argument after it is a positional.
- There are no short options, so `-x` is `Unknown`.
- An option that takes a value takes the next argument, whatever it is: in `--name --jobs`, the name is `"--jobs"`.
- When an option is given more than once, the last one wins.
- `Parse` stops at the first error.

## Help

`T.Usage` writes an `Options:` list for the flags and options and an `Arguments:` list for the positionals, and leaves out a list that would be empty. Each line shows the option, `<int>` or `<str>` for one that takes a value, and the field's `///` text; a required option ends with `(required)`. Write the help as full sentences in the fields' comments.

When `T` has a field `help: bool` and the arguments set it, `Parse` skips the checks for required options. It returns a `T` with `help` true and `0` or `""` in every required option that was not given. So `tool --help` reaches your `Usage` call even without the required options. `Unknown` and `Invalid` still fail.

## Errors

`T.Parse` fails with a member of `cli.Error`:

| Member | When |
|---|---|
| `Unknown(arg)` | `args[arg]` names no option, or is a positional and `T` has no `[]str` field |
| `Invalid(arg)` | `args[arg]` is a value its option cannot take, such as `many` for an `int`, or an option that needs a value comes last |
| `Missing(field)` | The required option of `T`'s field number `field` (counted from 0 in declaration order) was not given; of several, the first is reported |
| `OutOfMemory` | The arena could not hold the positionals |

The payloads are indexes rather than strings because an error's payload holds no reference. Look the argument up in the `args` you passed. `cli.Error` is open, so a `match` on it keeps a `_` arm.

## Memory and cost

The `str` values in the result are views of `args`, so the result borrows both `a` and `args` (`from a, args`). The positionals are a view of `args` too while they are one unbroken run; when options sit between them, they are copied into one list in `a`. `Parse` allocates nothing else, and `Usage` allocates nothing. The generator itself runs at compile time only.

## Examples

### Parse a command line

```mk
// verdict: ok
import std.cli

struct Opts {
    /// Print each file as it is built.
    export verbose: bool
    /// How many jobs to run at once.
    export jobs: ?int
    /// The name of the build.
    export name: str
    /// The files to build.
    export files: []str
}

gen cli.Args(Opts) // writes Opts.Parse and Opts.Usage

export Main(sys: &System): cli.Error|WriteError!void {
    o := try Opts.Parse(sys.arena, sys.args[1..]) // args[0] is the program
    jobs := o.jobs else { 1 } // an optional option not given is none
    try sys.Print("build {name} with {jobs} jobs\n", (name: o.name, jobs))
    for f in o.files {
        if o.verbose {
            try sys.Print("  {f}\n", (f))
        }
    }
}
```

```text
$ mk run build.mk -- --verbose --jobs=4 --name release a.mk b.mk
build release with 4 jobs
  a.mk
  b.mk
```

- `sys.args[0]` names the program itself, so the options start at `sys.args[1..]`.
- Without `--name`, `Parse` fails with `Missing`, and `Main` returns that error.

### Print help

```mk
// verdict: ok
import std.cli
import std.proc

struct Opts {
    /// Show this help and exit.
    export help: bool
    /// Write the result here.
    export out: str
    /// Stop after this many lines.
    export limit: ?int
    /// Leave files unchanged.
    export dry_run: bool
    /// The files to read.
    export files: []str
}

gen cli.Args(Opts)

export Main(sys: &System): WriteError!void {
    o := Opts.Parse(sys.arena, sys.args[1..]) else {
        var err := sys.Stderr()
        try err.Str("usage: tidy [options] files...\n")
        try Opts.Usage(mut err)
        try err.Flush()
        proc.Exit(2)
    }
    if o.help { // --help skips the check for the required --out
        var out := sys.Stdout()
        try out.Str("usage: tidy [options] files...\n")
        try Opts.Usage(mut out)
        try out.Flush()
        return
    }
    try sys.Print("{n} files to {out}\n", (n: o.files.Len(), out: o.out))
}
```

```text
$ mk run tidy.mk -- --help
usage: tidy [options] files...
Options:
  --help         Show this help and exit.
  --out <str>    Write the result here. (required)
  --limit <int>  Stop after this many lines.
  --dry-run      Leave files unchanged.
Arguments:
  <files>...     The files to read.
```

- Without `--help` and without `--out`, `Parse` fails, and the program prints the same help to standard error and exits with status 2.
- `Usage` writes to any `Writer`.

### Explain an error

```mk
// verdict: ok
import std.cli

struct Opts {
    /// The name of the build.
    export name: str
    /// How many jobs to run at once.
    export jobs: int
}

gen cli.Args(Opts)

// Explain turns a cli.Error into a message. The payloads are indexes:
// into args for Unknown and Invalid, into Opts's fields for Missing.
Explain(a: &Arena, args: []str, e: cli.Error): OutOfMemory!str from a, args {
    return match e {
        cli.Unknown(arg) { try a.Fmt("unknown argument {s}", (s: args[arg])) }
        cli.Invalid(arg) { try a.Fmt("bad value at {s}", (s: args[arg])) }
        cli.Missing(field) {
            names := ["--name", "--jobs"]
            try a.Fmt("{s} is required", (s: names[field]))
        }
        _ { "out of memory" }
    }
}

Show(sys: &System, args: []str): OutOfMemory|WriteError!void {
    o := Opts.Parse(sys.arena, args) else e {
        msg := try Explain(sys.arena, args, e)
        try sys.Print("error: {msg}\n", (msg))
        return
    }
    try sys.Print("name={name} jobs={jobs}\n", (name: o.name, jobs: o.jobs))
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    a1 := ["--name", "x", "-j", "3"] // no short options
    try Show(sys, a1[..])
    a2 := ["--name", "x", "--jobs", "many"]
    try Show(sys, a2[..])
    a3 := ["--name", "x", "--jobs"] // no value at the end
    try Show(sys, a3[..])
    a4 := ["--jobs", "2"]
    try Show(sys, a4[..])
    a5 := ["--name", "--jobs", "2"] // --name takes "--jobs", so "2" is a positional
    try Show(sys, a5[..])
}
```

```text
$ mk run errors.mk
error: unknown argument -j
error: bad value at many
error: bad value at --jobs
error: --name is required
error: unknown argument 2
```

## API

<!-- api:begin -->
Stability: Stable.

### `Args`

```
export Args(g: mut Gen, t: Type)
```

The generator behind `gen cli.Args(T)`: it writes a command-line parser
and a usage printer for the struct T, from T's fields and their `///`
comments: `T.Parse(a: &Arena, args: []str): cli.Error!T from a, args`
and `T.Usage(w: mut Writer): WriteError!void`.

Each field is one argument. A bool field is a flag (`--name`, or
`--name=true` and `--name=false`); an int or str field is a required
option (`--name V` or `--name=V`); a ?int or ?str field is an optional
one; one []str field takes the positional arguments. The option's name
is the field's with `_` written `-`, so `dry_run` is `--dry-run`. Every
field must be exported; another field, or a field of another type, is
E0903 at that field.

Parse rules: `--` ends the options; `-` alone and any argument not
starting with `-` is a positional; there are no short options, so `-x`
is Unknown. An option that takes a value takes the next argument,
whatever it is. The last of a repeated option wins. Parse stops at the
first error. Unknown and Invalid carry the index into args, Missing the
index of the field among T's fields; of several missing options, the
first declared is reported. When T has a field `help: bool` that ends
true, Parse skips the required checks and gives 0 or "" to each
required option not given, so the program can print Usage; Unknown and
Invalid still fail. The str values are views of args, and so are the
positionals while they are one unbroken run; otherwise they are copied
into one list in a.

Usage writes an `Options:` list and an `Arguments:` list, leaving out an
empty one, with each field's `///` text, and marks a required option
`(required)`.

Fails the generator at the first field that cannot be an argument.
O(fields squared) at compile time (the help column); allocates in the
run's arena.

### `Error`

```
export open type Error = Unknown | Missing | Invalid | OutOfMemory
```

Every way T.Parse fails.

### `Invalid`

```
export error Invalid(arg: int)
```

The argument at this index holds a value its option cannot take, or is
an option with no value at the end of args.

### `Missing`

```
export error Missing(field: int)
```

The required option of the field at this index was not given.

### `Unknown`

```
export error Unknown(arg: int)
```

An argument at this index of args names no option, or is a positional
and T takes none.

<!-- api:end -->
