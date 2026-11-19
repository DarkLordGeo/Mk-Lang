# nbody

Five bodies under gravity, leapfrog steps in `f64`: a float kernel with a square root and a division per pair, on data that fits in registers and L1. Overflow class `never` (only loop counters are integers, so the row has no `mk-wrap` column).

The state is structure-of-arrays: seven arrays of five `f64` (`x y z vx vy vz m`) in one struct, indexed `s.x[i]`. The initial state (every value exact in binary):

| Body | x | y | z | vx | vy | vz | m |
|---|---|---|---|---|---|---|---|
| 0 | 0 | 0 | 0 | 0 | 0 | 0 | 8 |
| 1 | 4 | 0 | 0.25 | 0 | 1.375 | 0 | 0.03125 |
| 2 | -2 | 0 | -0.125 | 0 | -2 | 0.0625 | 0.015625 |
| 3 | 6 | 0 | 0.5 | 0 | 1.125 | 0 | 0.0625 |
| 4 | -9 | 0 | 0 | 0 | -0.9375 | 0.03125 | 0.0078125 |

```
Dt = 0.0078125
advance(steps):
    repeat steps times:
        for i in 0..4: for j in i+1..4:
            dx = x[i]-x[j]; dy = y[i]-y[j]; dz = z[i]-z[j]
            d2 = (dx*dx + dy*dy) + dz*dz
            mag = Dt / (d2 * sqrt(d2))
            vx[i] = vx[i] - (dx*m[j])*mag      (and vy, vz)
            vx[j] = vx[j] + (dx*m[i])*mag      (and vy, vz)
        for i in 0..4:
            x[i] = x[i] + Dt*vx[i]             (and y, z)
energy():
    e = 0
    for i in 0..4:
        e = e + (0.5*m[i]) * ((vx[i]*vx[i] + vy[i]*vy[i]) + vz[i]*vz[i])
        for j in i+1..4:
            e = e - (m[i]*m[j]) / sqrt((dx*dx + dy*dy) + dz*dz)
print trunc(energy() * 1e9)                     toward zero, as a 64-bit integer
```

The parenthesisation above is the evaluation order in every language. No column contracts `a*b+c` into an FMA: C is built with `-ffp-contract=off`, Rust, Zig and Mk never contract, and Go writes each product that meets an addition as `float64(a*b)`, which its spec forbids fusing. `sqrt` is the correctly rounded square root in every language, so the checksum is exact: `-110176510` at the publish size `50000000`, `-112142077` at the quick size `10000` (the codegen kernel's checksum, same algorithm).
