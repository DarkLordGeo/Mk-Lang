# raft (app, Tier A)

**An Mk-only row** (tests/bench/README.md, "Tier A"). The Mk side is the raft dogfood, `Dogfoods/raft`, unchanged. The row has no twin (std.sim has no peer in another language's std), so it has no ratio to another language and no defect line: it is compared across mk binaries (`tools/bench compare --base <mk A> --head <mk B> -cols mk raft`).

**Task.** `raft sim --seeds A..B --partitions --loss 5`, the dogfood's bench case over more seeds: for each seed, std.sim runs a Raft cluster with network partitions and 5 percent message loss, then the history is checked for linearizability and replayed. It prints how many runs were linearizable and replayed identically, and the history digest.

**Check.** The SHA-256 of the standard output (`digest`).

**Environment.** `MK_CARRIERS=1`.

**Sizes.** The quick arguments are the dogfood's bench case (seeds 1..300); the publish range is provisional, and `verify -publish-size` fixes it in the quiet window.
