# std-sim benchmark rows

Rows for std.sim. No standard library among C, Rust, Zig and Go ships a simulated `Io` with a disk and a crash model (tests/bench/README.md: a language with no standard facility says so, and no third-party crate stands in), so `simkv`, `simwal` and `simfiles` have no baseline: they track the simulator's own cost. Go's standard library does ship a simulated scheduler and clock, `testing/synctest` (Go 1.25), so `simsched` has a Go column. Only `tools/bench` publishes a number from these rows.

| Row | Work | Mk | Baselines |
|---|---|---|---|
| `simkv` | `seeds` seeds from `first`: per seed a `sim.Sim`, then three lives of a write-ahead-log store (7 keys, 24 synced puts per life, recovery by the longest valid prefix rewritten through a synced temporary and a rename); lives 1 and 2 crash at a seeded point; every completed recovery is compared with the committed writes; prints `seeds N crashed C bad 0` | `sim.Sim`, `std.fs` over its `Io` | none: no std simulated Io |
| `simwal` | one `sim.Sim`; `records` appends of 20 bytes at the log's end, each followed by a `Sync`; a `Restart`; the whole log read back and summed; prints `records N sum S` (linear in N) | `sim.Sim`, `fs.File.WriteAt`, `Sync` | none: no std simulated Io |
| `simsched` | `tasks` tasks under one `sim.Sim`, each `rounds` times sleeping 1 ms of simulated time and sending its index to one channel; a collector task receives every value; prints the sum and the simulated time | `sim.Sim`, `Chan[int]`, `Io.Sleep` | Go: the same in one `testing/synctest` bubble |
| `simfiles` | one `sim.Sim`; `files` files created, then each statted and renamed; prints the count and the sum of the sizes (the namespace's lookups) | `sim.Sim`, `fs.WriteAll`, `fs.Stat`, `fs.Rename` | none: no std simulated Io |
