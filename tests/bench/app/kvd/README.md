# kvd (app, Tier A)

**An Mk-only row** (tests/bench/README.md, "Tier A"). The Mk side is the kvd dogfood, `Dogfoods/kvd`, unchanged. The row has no twin, so it has no peer, no ratio to another language and no defect line: it is compared across mk binaries (`tools/bench compare --base <mk A> --head <mk B> -cols mk kvd`).

**Task.** `kvd sim --seeds A..B`: for each seed, std.sim runs a kvd server and three clients of 16 steps on a simulated disk and network, crashes it at a log write, faults the disk or the network, or shuts it down, recovers the server from its log (twice) and checks that every run recovered what its clients were told. It prints the summary of the runs and a digest. Nothing touches the real disk or network.

**Labelled.** kvd declares one unsafe module, `src/snap` (its snapshot recovery through `fs.Map`). The app table's rules keep a dogfood with an unsafe module out of a twinned `bench.json` row unchanged; an Mk-only row may keep it, as here, and says so.

**Why sim.** Not kvd's `bench` case. `kvd bench` loads an in-process server with its log synced on every write (`wal.SyncPolicy.Always`, fixed in `src/main/bench.mk`), so every write waits for a sync of the real disk, and it opens a data directory that must be fresh for each run (a second run on the same directory starts by recovering the first one's log), which an app row's output file does not give: tools/bench deletes a file before each run, not a directory. `sim` is the same server and recovery code, deterministic per seed. kvd's served path under load is the twinned row `app/kvserve`.

**Check.** The SHA-256 of the standard output (`digest`).

**Environment.** `MK_CARRIERS=1`.

**Sizes.** The quick arguments are the dogfood's `sim-carriers16` case's seed range (0..999); the publish range is provisional, and `verify -publish-size` fixes it in the quiet window.
