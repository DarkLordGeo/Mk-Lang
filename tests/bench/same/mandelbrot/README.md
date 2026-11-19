# mandelbrot

The scalar Mandelbrot set over an n by n grid: a branchy float loop with a data-dependent trip count. Overflow class `never`. The vector form is bench-std's SIMD row.

```
MaxIter = 50
total = 0
for y in 0..n-1:
    ci = (2.0 * y) / n - 1.0
    for x in 0..n-1:
        cr = (2.0 * x) / n - 1.5
        zr = 0.0; zi = 0.0; i = 0
        while i < MaxIter:
            tr = (zr*zr - zi*zi) + cr
            ti = (2.0*zr)*zi + ci
            zr = tr; zi = ti
            if zr*zr + zi*zi > 4.0: break
            i = i + 1
        total = total + i
print total
```

`total` is the number of completed iterations over all points, so a point inside the set adds 50. The parenthesisation above is the evaluation order everywhere, and no column fuses a product into an addition, so the count is exact. Publish size and checksums: in `bench.json`.
