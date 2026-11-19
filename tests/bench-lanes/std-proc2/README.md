# std-proc2 benchmark rows (idiomatic standard library)

Rows for the idiomatic table of tests/bench/README.md. Each baseline uses its standard library's idiomatic form and prints one checksum line. Only `tools/bench` publishes a number from these rows.

| Row | Work | Mk | Baselines |
|---|---|---|---|
| `run` | `n` runs of the program itself with the argument `child`, which writes 64 bytes to standard output and exits 0; both output streams captured, standard input the null device, each exit waited for; prints `n bytes fails` | `proc.Run` | Go `os/exec` (`Cmd.Run` with buffers), Rust `std::process::Command::output`, Zig `std.process.run`; C has no std facility (skipped in `bench.json`) |

rt-io2's parked `spawn` row (`tests/bench-lanes/rt-io2/pending/spawn`) is the same child with every stream null and nothing captured; its Mk column needs `std.core` and so does not build outside std. `run` is the row of the public API.
