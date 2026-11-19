# intsum

Prices Mk's checked integer add against C's `add nsw` (the overflow-check budget: within 5 percent). Overflow class `never`.

```
intsum(n, rounds, seed):
    xs = n int64, all zero                     (allocated once, not part of the work)
    st = seed
    for i in 0 .. n-1:                         fill: splitmix64, wrapping by design
        st = st + 0x9e3779b97f4a7c15
        xs[i] = mix(st) & 1023                 mix is splitmix64's finaliser (tests/bench/README.md)
    total = 0
    for r in 0 .. rounds-1:
        s = 0
        for i in 0 .. n-1:                     the measured loop: index loop, signed 64-bit add
            s = s + xs[i]
        total = total + s
        xs[r mod n] = s & 1023                 a write-back, so no pass can be hoisted or folded
    print total
```

Publish size `1000000 12000 1`: 8 MB of values (resident in L3 on the measuring machine's 9800X3D) summed 12,000 times; the fill is one pass, so the sum is more than 99.9 percent of the work. Values are below 1024, so `total` stays below 2^44 and never overflows. Checksums: `6136887200936` at the publish size, `2562590043` at the quick size `100000 50 1`.

Columns: `mk` (checked `+`), `mk-wrap` (the same with `+%` for `s` and `total`), and every other column. The ratio `mk / c` is the overflow check's price; `mk-wrap / c` is what is left once the check is gone (bounds checks stay in both Mk columns).
