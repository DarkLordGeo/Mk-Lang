# cli (idiomatic table)

Workload: build one argument vector of `n` arguments in memory, then parse it `reps` times with the language's standard argument parser. The vector is `n / 10` groups of seven arguments, `--verbose`, `--count=<c>`, `--name`, `n<c>`, `--level`, `<3c>`, `--tag=t<c>`, for `c` from 0, then the positionals `f0`, `f1`, ... up to `n` arguments. Every option repeats, so the last value wins; the positionals come after the options, so a parser that stops at the first positional (Go `flag`) reads the same values as one that intersperses (Mk).

Checksum: per rep, `count + level + len(name) + len(tag) + len(files) + (verbose ? 1 : 0) + len(files[rep mod len(files)])`, summed over reps (wrapping u64).

| Column | Parser | Setup outside the timed reps |
|---|---|---|
| mk | `gen cli.Args(Opts)`, `Opts.Parse(a, argv)`: one pass, the strs are views of `argv`, the positionals a view (no allocation) | the vector, in the arena |
| go | `flag.NewFlagSet`, `Bool`, `Int`, `String` once; `fs.Parse(argv)` per rep, `fs.Args()` the positionals | the vector and the FlagSet |
| c | none: ISO C has no parser, and `getopt_long` is not in the MSVC CRT | |
| rust | none in std (`std::env::args` only; clap is not std) | |
| zig | none in std (the process argument iterator only) | |

Both columns build the vector inside the timed process, so its construction is in every run's time; at `n = 1000` it is 1,000 small strings against `reps x 1000` parsed arguments.
