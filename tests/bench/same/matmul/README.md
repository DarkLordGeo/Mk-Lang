# matmul

Dense `f64` matrix multiply, the i-k-j loop over flat row-major arrays: the inner loop is a vectorisable stream `c[i,:] += a[i,k] * b[k,:]`. Overflow class `never` (indices only).

```
matmul(n, rounds, seed):
    a, b, c = n*n f64 each, c all zero          (allocated once)
    st = seed
    for i in 0 .. n*n-1:
        st = st + 0x9e3779b97f4a7c15;  a[i] = (mix(st) & 255) / 256.0
        st = st + 0x9e3779b97f4a7c15;  b[i] = (mix(st) & 255) / 256.0
    repeat rounds times:
        for i in 0..n-1: for k in 0..n-1:
            aik = a[i*n+k]
            for j in 0..n-1:
                c[i*n+j] = c[i*n+j] + aik * b[k*n+j]
    total = sum of c[i] in index order
    print trunc(total * 2^16)
```

Every input is a multiple of 2^-8 in [0, 1), so every product is a multiple of 2^-16 and each round adds less than n to an entry of `c`, so after `rounds` rounds every entry is below `n * rounds` and every partial sum of `total` is below `n^3 * rounds`. For n = 512 and rounds at most 1024 that is below 2^37, and a multiple of 2^-16 below 2^37 needs at most 53 significant bits: all exact in `f64`, and `total * 2^16` is an exact integer below 2^53. The checksum therefore cannot depend on evaluation order, and a vectorised or reassociated loop gives the same bits as a scalar one. No FMA in any column (C `-ffp-contract=off`, Go `float64(a*b)`); at x86-64-v2 none exists anyway.

The inputs are 8-bit rather than 10-bit (multiples of 2^-10), which would hold the work to `n^3 * rounds` below 2^33, just under the 1 s floor for C.

Publish size and checksums: in `bench.json`.
