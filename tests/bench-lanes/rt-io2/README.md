# rt-io2 benchmark rows (idiomatic standard library)

Rows of the runtime's I/O entries for the idiomatic table. The row left is parked under `pending/` with its file named `bench.json.pending`, so `tools/bench` does not discover it yet. Its Mk column calls the runtime's entries through `std.core`'s wrappers (`core.IoSpawn`, `core.IoProcWait`), and a program outside std cannot import `std.core` (E0202, std-internal), so it does not build. The public modules (`proc.Run`, `net.Bind` and `Datagram`) enable a row by moving it to `tests/bench-lanes/rt-io2/<row>/`, renaming the file `bench.json` and rewriting the Mk program over their API with the same work and output. The C, Rust and Go columns pass `tools/bench verify` with the checksums given. Each baseline uses its standard library's idiomatic form; every program prints one checksum line. Only `tools/bench` publishes a number from these rows.

| Row | Work | Mk | Baselines |
|---|---|---|---|
| `spawn` | `n` runs of the program itself with the argument `child`, which exits 0 at once; every stream null; each exit code waited for; prints `n sum fails` | `core.IoSpawn` (empty `args[0]`: this program), `core.IoProcWait` | Go `os/exec`, Rust `std::process::Command`, C `CreateProcessW` (Windows) and `posix_spawn` (Linux) |

## Enabled rows

| Row | Work | Mk | Baselines |
|---|---|---|---|
| `task-pingpong` | two tasks pass a value back and forth over two rendezvous channels, `n` rounds; each answer is `i * 3 + 1`; prints `n sum 0` | `Chan[u64]` capacity 0, the partner spawned by `s.go` in one scope: each waiting `Send` and `Recv` parks a task through std.task's `WaitWord` | Go: goroutines over unbuffered channels; Rust: two threads over `sync_channel(0)` (std has no task runtime); C: two threads, each mailbox a word waited on with `WaitOnAddress` (Windows) or the futex (Linux); Zig skipped (`skip` says why) |
| `stat` | the facts of one existing file (the row's generated input, a `words` file of 4,097 bytes), `n` times, each answer's size added; prints `n sum fails` | `fs.Stat` (over the `stat` entry: one blocking-pool job per call) | Go `os.Stat`, Rust `std::fs::metadata`, Zig `std.Io.Dir.statFile`, C `GetFileAttributesExW` (Windows, the call Go's and Rust's std make first) and `stat(2)` (Linux); each follows links |

`readdir` (a directory of 100,000 entries listed with sizes) waits for a directory-tree data kind in `tools/bench gen`: every column must list the same tree, and the generator only writes files today.

`udp-pingpong` lives at `tests/bench-lanes/std-net2/udp-pingpong` (its Mk column over `net.Bind` and `Datagram`, the same work and checksums), so it has no parked copy here. `spawn` stays parked: the `run` row (`tests/bench-lanes/std-proc2/run`) is the public-API row of the same child.
