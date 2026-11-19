# kvserve (app, concurrency)

**Task.** A key-value server over TCP speaking kvd's line protocol, with every change appended to a write-ahead log before it is answered. The Mk side is `Dogfoods/kvd`, unchanged: `kvd serve`. The twins speak the subset of its protocol the load uses and write the same log. The spec is `kvserve.json`, not `bench.json`: a server is not a program hyperfine can time, so the row has its own measurement, `tools/bench kv` (`tools/bench/serverbench.go` and `kvbench.go`), as the idiomatic `http` row has.

**Command line.** Every column runs `<server> serve --addr 127.0.0.1:<port> --data <dir> --sync never`, on a fresh, empty `<dir>` for each server start, and serves until tools/bench kills it. The Mk column also gets two of kvd's own flags (`mk_args`):

- `--compact-after 1000000000`: kvd compacts by default every 1,000 changes, writing the whole store as a snapshot and cutting the log back. The twins do not compact, so the row keeps kvd from doing it during a run.
- `--max-clients 10000`: kvd's default is 1,000. That is above the row's 256 connections, but this flag keeps every connection and tools/bench's probe connection from ever meeting the limit.

**The protocol** (kvd's README and `src/proto`). A request is one line of ASCII words ended by `\n`: a command, a key, and for `SET` a value, which is the rest of the line and may hold spaces. The line is trimmed of white space. Commands are matched in upper or lower case, not mixed. Replies are one line each:

| Request | Reply | The log |
|---|---|---|
| `GET k` | `$value`, or `_` when `k` is missing | nothing |
| `SET k value` | `+OK` | a set record of `k` to `value` |
| `INCR k` | `:n`, where `n` is the value read as a 64-bit integer (0 when missing) plus 1; `-ERR NotAnInteger` when it is not one | a set record of `k` to `n` in decimal |
| `DEL k` | `:1`, or `:0` when `k` is missing | a delete record of `k`, only when it was there |
| `PING`, `QUIT` | `+PONG`, `+BYE` (and the connection ends) | nothing |

Every twin also answers kvd's errors for these commands, within the load's alphabet: `-ERR Empty` for a blank line, `UnknownCommand`, `MissingArgument`, `ExtraArgument`, `KeyTooLong(len: n, limit: 256)`, `ValueTooLong(...)`, `BadByte(offset: i)`, and `LineTooLong` past a line of 65,856 bytes. The load never sends one. kvd also answers `-ERR Utf8Error` for a value that is not UTF-8, which the twins do not (below).

**Outside the load's alphabet.** The table and the error list are the twins' contract for what the load sends: ASCII letters, digits and spaces, values that fit, and counters far from the 64-bit limit. Outside it, probes found these differences, which are stated rather than removed:

- **Trimming.** kvd trims all of Unicode's White_Space, and Go's `strings.TrimSpace` does too. Rust's `trim_ascii` leaves the vertical tab, and Zig trims only space, tab, CR and LF, so both answer `BadByte(offset: 1)` to `GET a` followed by a vertical tab (Zig also to a form feed).
- **A value that is not UTF-8.** kvd answers `-ERR Utf8Error`. Go and Zig store it, and Rust answers `BadByte(offset: 8)`.
- **`INCR` at the 64-bit limit.** After `SET m 9223372036854775807`, one `INCR m` ends kvd's whole process with `panic: overflow at src/server/change.mk:116:46` and exit status 101 (its overflow trap on `n + 1`, seen on kvd's standard error): no reply comes, and the next connection is refused. kvd's README does not say so yet. `rust-ovf` and `zig-safe` end the same way, on their overflow traps. The go, rust and zig-fast cells wrap to -9223372036854775808. The load's counters reach a few hundred.

A leading `+` and leading zeros are read as an integer by every column (`+5`, `007`), and `1_000` by none: the Zig twin parses a sign and digits, as kvd's `str.ParseInt` does, not with `std.fmt.parseInt`, which takes the `_`.

**The log** (`Dogfoods/kvd/src/wal/format.mk`). Every integer is little-endian.

- A 24-byte header: `KVDWAL1\n`, the generation (u64, 0 on a fresh directory), the CRC-32 of the first 16 bytes (u32), the clean-shutdown flag (u8, 0 while serving) and three zero bytes. It is written and synced when the server starts.
- Records: the payload's length (u32), the payload's CRC-32 (u32, IEEE), then the payload: the operation (u8: 1 set, 2 delete), the key's length (u16), the key and the value.

kvd, Go and Rust open the file to append. Zig creates it exclusively (`createFile` with `exclusive`) and writes it at its position (`writeStreamingAll`), which appends just the same, since one handle writes the file, under the log's mutex. A change is written before it is answered, and with `--sync never` it is never synced, so a crash of the machine can lose answered changes but a killed process cannot (kvd's README, Limits).

**The work of a change**, in every column, as kvd's `src/server/change.mk` does it:

1. take the log's mutex;
2. read the store under its read lock where the change depends on it (`DEL`: is the key there; `INCR`: its value);
3. append the record;
4. take the store's write lock and apply the change;
5. write the reply into the connection's buffered writer, release both, and flush.

So the log's order is the store's. `GET` takes the store's read lock only. kvd writes the reply while it still holds the log's mutex and the store's write lock; each twin writes it while it holds the log's mutex. Every column flushes after releasing them, once per reply.

**What kvd does that the twins do not.** These are part of kvd's own work under the load, so they are in its cell:

- It writes each record with three `WriteAll` calls (the 11-byte head, the key, the value). Each twin writes it with one call.
- It counts every command in its client table under a mutex, keeps atomic counters of the commands, changes and busy time, reads the clock twice per request, and checks each request against a slow-request limit.
- It reads each line through a 4 KiB reader into a 65,856-byte line buffer.
- It reports connections through its log task (`log.Sink` on standard error).

The twins keep none of that state.

**No-delay.** kvd's listener sets `no_delay`, Go's `net` sets it on every TCP connection by default, and the Rust twin calls `set_nodelay`. Zig's `std.Io.net` has no such option (no `NODELAY` in `std/Io`, checked in zig 0.17.0's lib), so the Zig twin's connections keep Nagle's algorithm. Zig's std could set it through `std.os.windows.ws2_32.setsockopt` with `TCP.NODELAY` (`NODELAY = 1` in ws2_32.zig), which the twin does not do. Each reply is one write after its request arrives, with nothing else unacknowledged on the connection, so Nagle's algorithm does not hold it back. tools/bench's client sets no-delay on its side.

The twins' `--sync always` and `--sync N` paths were run only by `cmp-kv.sh` (`SYNC=always` and `SYNC=3`), where each twin's replies and log matched kvd's. Matching shows nothing about the syncs themselves, and the row runs `--sync never`.

**Load.** tools/bench's kv generator (`kvbench.go`), a raw client on Go's `net.Conn`, one goroutine per connection. It runs 256 keep-alive connections with one request outstanding on each. Connection `i` owns 64 keys, `c<i>k0` to `c<i>k63`: the even ones hold text, the odd ones counters. It draws its requests from splitmix(seed + i), with seed 38:

- `GET` 50% (any of its keys);
- `SET` 25% (a text key; 8 to 64 bytes of lower-case letters, digits and inner spaces);
- `INCR` 15% (a counter);
- `DEL` 10% (any of its keys).

Because each connection owns its keys, every reply is predictable from that connection's requests alone, and every reply is checked against the connection's model of its keys.

**The log check.** After each server stops, tools/bench reads the log it left behind (`kvWalCheck`). It checks:

- the fresh header (generation 0, not shut down cleanly);
- that every record is whole and its CRC-32 is right;
- that there is exactly one record per change the connections were answered for;
- that the store the records replay to equals the union of the connections' models;
- that the records of each connection's keys, in the log's order, are that connection's answered changes in order, values included (a digest per connection), so a wrong value later overwritten, or one connection's changes out of order, fails too.

The server is killed only after every connection has its last reply, so a twin that answers without logging, or logs what it did not answer, fails its cell.

**Measurement.** Like `http`: 5 s of warm-up, then 30 s measured; req/s, p50 and p99; three sweeps in rotated column order, each a fresh server on a fresh data directory. `tools/bench kv -publish` holds the machine lock, sets its own affinity to `0xff` (CPUs 0 to 7) before it starts a server, which inherits it, then to `0xff00` (CPUs 8 to 15) for the generator. The Go server and the generator run with GOMAXPROCS 8 and the Mk server with `MK_CARRIERS=8`. The Rust and Zig servers run one OS thread per connection on the same eight CPUs. Connections close with a reset (linger 0). 256 connections hold 512 sockets on the machine at once, below the 10,000-socket cap.

**Cells.**

| Column | Kind | Server | Model |
|---|---|---|---|
| mk | std | `Dogfoods/kvd`: std.net, std.sync (`RwLock` store, `Mutex` log), `TextMap[HeapText]` keyed with SipHash, std.fs append, std.hash `Crc32State` | tasks, `MK_CARRIERS=8` |
| go | std | `testdata/kvserve.go`: `net`, `bufio`, a map behind `sync.RWMutex`, `os.File` behind `sync.Mutex`, `hash/crc32` | goroutines, GOMAXPROCS 8 |
| rust, rust-ovf | hand | `kvserve.rs`: `std::net`, a `std::thread` per connection, `RwLock<HashMap>` (SipHash-1-3), `Mutex<File>`. The hand part is a CRC-32 with its table (about 25 lines), since Rust's std has none | OS threads |
| zig-fast, zig-safe | std | `kvserve.zig`: `std.Io.net` with `Io.Group.concurrent` as the http row, `StringHashMapUnmanaged` (Wyhash) behind `std.Io.RwLock`, `std.Io.File` behind `std.Io.Mutex`, `std.hash.Crc32` | OS threads |
| c | | none: C's std has no sockets, as in `http` | |

**Labelled: kvd's unsafe module.** kvd declares one unsafe module, `src/snap`, which maps a snapshot with `fs.Map` inside an `unsafe` block at recovery. A twinned row may not have one (tests/bench/README.md, "Applications"). This row is the one exception: a server row's spec may name its package's unsafe modules, and only server specs have the field. The row never reaches it:

- every server starts on an empty data directory, so recovery finds no snapshot, and `snap.Load` returns before `MapOwn`;
- `--compact-after 1000000000` keeps the server from writing a snapshot during a run.

`kvserve.json` names the module in `unsafe_modules` with this reason. `TestServerRowsMeetTheContract` passes the row only while the package declares exactly that module.

**Checks.** `tools/bench kv -check` builds every column and runs 400 requests on each of the 256 connections, every reply and the log checked, and times nothing. With an mk built from the tree it passed in every column (2026-11-16, unpinned), and again after a later fix, with the log check comparing each connection's changes in order (2026-11-16 02:02). Each column made the same log both times: 46,714 records, 12,405 keys and 1,776,155 bytes.

On one connection's scripted session, which included the error replies, each twin also matched `kvd serve` reply for reply, and its log was byte-identical to kvd's (a comparison script outside the tree). `kvd wal check` read each twin's log as clean.

`TestKVGoTwinAndLogCheck` builds the Go twin, checks it the same way, and shows that the log check refuses a flipped byte, a lost or torn last record, a header-only log, a model the log does not give, two of one connection's changes swapped, and a wrong value that a later change overwrites. No number of this row has been measured.
