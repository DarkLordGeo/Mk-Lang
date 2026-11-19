# fmtints

Integer to decimal text, the textbook conversion written out in every language: a division-by-constant loop, short byte copies and stores. It does not call any std formatter; formatting with each language's std is the idiomatic `fmt` row. The codegen table's `fmtints` kernel is the same loop in process. Overflow class `never`.

```
fmtints(n, rounds, seed):
    xs = n int64; st = seed
    for i in 0..n-1:                           not part of the work: one pass
        st = st + 0x9e3779b97f4a7c15
        xs[i] = (mix(st) mod 2000000001) - 1000000000      in [-10^9, 10^9]
    buf = n*12 bytes; tmp = 20 bytes; total = 0
    repeat rounds times:
        pos = 0
        for i in 0..n-1:
            x = xs[i]
            if x < 0: buf[pos] = '-'; pos = pos + 1; u = -x   else u = x      (u unsigned 64-bit)
            k = 20
            loop: k = k - 1; tmp[k] = '0' + u mod 10; u = u / 10; if u == 0: break
            for j in k..19: buf[pos] = tmp[j]; pos = pos + 1
            buf[pos] = '\n'; pos = pos + 1
        sum = the sum of buf[0..pos-1] as integers
        total = total + sum
    print total
```

Types, the same in every column: the values, `sum` and `total` are signed 64-bit (C `int64_t`, Rust and Zig `i64`, Go and Mk `int`), `u` and the splitmix state unsigned 64-bit, the bytes `u8`; positions and indices (`pos`, `k`, `i`, `j`) are signed 64-bit (C `int64_t`, Go and Mk `int`) except in Rust and Zig, which index with `usize`. Each value takes at most 11 bytes plus the newline, so `n*12` bytes always suffice, and `total` stays far below 2^63. Publish size and checksums: in `bench.json`.
