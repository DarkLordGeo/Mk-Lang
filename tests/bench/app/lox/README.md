# lox (app, Tier A)

**An Mk-only row** (tests/bench/README.md, "Tier A"). The Mk side is the lox dogfood, `Dogfoods/lox`, unchanged: a bytecode compiler and VM with a mark-and-sweep collector. The row has no twin (a twin would be a whole interpreter), so it has no peer, no ratio to another language and no defect line: it is compared across mk binaries (`tools/bench compare --base <mk A> --head <mk B> -cols mk lox`).

**Task.** The program of the dogfood's bench script, `cases/scripts/bench.lox`, written on one line and run with `lox --eval`, since the script takes no size: a recursive `fib(n)`, a list of 100 strings at most, pushed N times and dropped at 100, and a loop summing `i % 7` M times; lox prints the three results.

**Check.** The SHA-256 of the standard output (`digest`). At the quick size the output is the dogfood's `cases/bench.out` (46368, 2, 599994).

**Environment.** `MK_CARRIERS=1`.

**Sizes.** The quick arguments keep bench.lox's counts (`fib(24)`, 20,000 pushes, 200,000 sums); the publish counts are provisional, and `verify -publish-size` fixes them in the quiet window.
