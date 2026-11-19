# sortints

One pdqsort, written once per language. It is Go 1.27's `pdqsortOrdered` (`slices/zsortordered.go`, the same algorithm as std's `Slice[T].Sort` in `std/core/core_sort.mk`) specialised to `int64` and `<`, so no column calls its standard library's sort and no comparator is passed. Overflow class `never`: the sort does only index arithmetic, and the checksum stays below 2^60 (below). The fill uses splitmix64, which wraps by design.

```
sortints(n, rounds, seed):
    xs = n int64                                  (allocated once)
    st = seed; total = 0
    for r in 0 .. rounds-1:
        for p in 0 .. 3:
            for i in 0 .. n-1:                    fill pattern p
                p0 random:   st = st + G; xs[i] = mix(st) & 0xFFFFF
                p1 reversed: xs[i] = n - i
                p2 sawtooth: xs[i] = i mod 1000
                p3 16 keys:  st = st + G; xs[i] = mix(st) >> 60
            pdqsort(xs, 0, n, bitlen(n))
            total = total + check(xs)
    print total

check(xs): -1 if some xs[i-1] > xs[i], else the sum of xs[i] * (i mod 1000)
bitlen(n): the number of bits of n (a loop of shifts; Go's bits.Len)
G = 0x9e3779b97f4a7c15, mix = splitmix64's finaliser (tests/bench/README.md)
```

`pdqsort(s, a, b, limit)` is Go's, with its constants: insertion sort at 12 or fewer, the Tukey ninther from 50, `maxSwaps` 12, five steps of partial insertion sort from 50, `breakPatterns` with the xorshift seeded by the length, heapsort when `limit` reaches 0, recursion into the shorter side and a loop on the longer. Every helper takes the slice and two indices, and swaps by a temporary. Its quirk is kept: the backward shift loop of `partialInsertionSort` runs `j >= 1`, not `j > a`, as in Go.

Patterns: the random keys exercise partitioning; the reversed input takes the decreasing-hint path (`reverseRange`, then partial insertion sort finishes); the sawtooth and the 16 keys reach `partitionEqual` and `breakPatterns`.

Bounds: every `s[i]` is checked in Mk, Rust, Zig ReleaseSafe and Go, and unchecked in C and Zig ReleaseFast. `n` is at most 2^20, so every key is below 2^20 and each pattern's `check` is below 2^20 * 2^10 * 2^20 = 2^50; with 4 patterns and at most 1,024 rounds the total stays below 2^62.

Sizes: publish `1000000 16 1`, quick `20000 2 1`. Checksums are in `bench.json`.
