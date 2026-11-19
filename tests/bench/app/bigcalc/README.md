# bigcalc (app, Tier A)

**An Mk-only row** (tests/bench/README.md, "Tier A"). The Mk side is the bigcalc dogfood, `Dogfoods/bigcalc`, unchanged: a big-number RPN calculator on `std.big`. The row has no twin, so it has no peer, no ratio to another language and no defect line: it is compared across mk binaries (`tools/bench compare --base <mk A> --head <mk B> -cols mk bigcalc`).

**Task.** The lines of the dogfood's bench script, `cases/data/bench.rpn`, given to `bigcalc calc`, which evaluates each argument as a line of RPN and then prints the top of the stack: a factorial's bit length, a Fibonacci number mod 1,000,000,007, a Mersenne number stored as `$m`, a Fermat test of it (`3^(m-1) mod m`), the integer square root of a power of ten mod 7, the gcd of two Mersenne numbers, and the inverse of a constant mod `$m`, each printed by `p`.

**Check.** The SHA-256 of the standard output (`digest`). At the quick size the output is the dogfood's `cases/bench.out` and then 443 once more, the top of the stack.

**Environment.** `MK_CARRIERS=1`.

**Sizes.** The quick arguments keep bench.rpn's operands; the publish operands are provisional (30,000!, the 200,000th Fibonacci number, the Mersenne primes 2^19937 - 1 and 2^11213 - 1, 10^30000), and `verify -publish-size` fixes them in the quiet window.
