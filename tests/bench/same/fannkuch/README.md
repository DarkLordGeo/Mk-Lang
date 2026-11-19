# fannkuch

The Benchmarks Game's fannkuch-redux (`fannkuch-redux`), sequential, in its plain reference form: short array loops, swaps and a data-dependent while loop, all integer. Overflow class `never`.

```
perm1 = [0, 1, ..., n-1]; perm, count = n zeros
r = n; permCount = 0; checksum = 0; maxFlips = 0
loop:
    while r != 1: count[r-1] = r; r = r - 1
    perm = copy of perm1                           (an index loop)
    flips = 0; k = perm[0]
    while k != 0:
        i = 0; j = k
        while i < j: swap perm[i], perm[j]; i = i + 1; j = j - 1
        flips = flips + 1; k = perm[0]
    maxFlips = max(maxFlips, flips)
    checksum = checksum + flips if permCount is even, else checksum - flips
    loop:                                          next permutation
        if r == n: stop, print checksum * 1000 + maxFlips
        perm0 = perm1[0]
        for i in 0..r-1: perm1[i] = perm1[i+1]
        perm1[r] = perm0
        count[r] = count[r] - 1
        if count[r] > 0: break
        r = r + 1
    permCount = permCount + 1
```

64-bit signed integers everywhere (C `int64_t`, Rust and Zig `i64` with `usize` conversions for indexing, Go and Mk `int`). For n = 7 the known result is checksum 228 and 16 flips, so the program prints 228016. Publish size and checksums: in `bench.json`.
