# sieve

The sieve of Eratosthenes over byte flags: a store-bound inner loop with a run-time stride, and a branchy outer loop. Overflow class `never`.

```
sieve(n, rounds):
    flags = n + 1 bytes                        (allocated once)
    total = 0
    repeat rounds times:
        for i in 0 .. n:      flags[i] = 1     a plain loop in every language
        count = 0
        for i in 2 .. n:
            if flags[i] != 0:
                count = count + 1
                j = i * i
                while j <= n:
                    flags[j] = 0
                    j = j + i
        total = total + count
    print total
```

Signed 64-bit counters `count` and `total` in every column (C `int64_t`, Rust and Zig `i64`, Go and Mk `int`), and signed 64-bit indices (C `int64_t`, Go and Mk `int`) except in Rust and Zig, which index with `usize`; `i * i` stays below n^2 < 2^63 for every n used here. Publish size `20000000 46` (20 MB of flags): checksum `58447922` (46 times the 1,270,607 primes up to 2*10^7). Quick size `2000000 2`: `297866` (2 times 148,933).

`mk-wrap` advances `count`, `j` and `total` with `+%`; `i * i` stays checked.
