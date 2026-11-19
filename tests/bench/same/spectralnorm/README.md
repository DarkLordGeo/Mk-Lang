# spectralnorm

The Benchmarks Game's spectral-norm (`spectral-norm`), written scalar and single-threaded as its plain reference, not as its tuned programs. A division-bound float kernel whose matrix entries come from integer arithmetic on every use. Overflow class `never`.

```
A(i, j) = 1.0 / ((i+j)*(i+j+1)/2 + i + 1)       integer arithmetic, then one conversion
mulAv(v, out):  for i: s = 0; for j: s = s + A(i,j)*v[j]; out[i] = s
mulAtv(v, out): for i: s = 0; for j: s = s + A(j,i)*v[j]; out[i] = s
mulAtAv(v, out, tmp): mulAv(v, tmp); mulAtv(tmp, out)

u = n ones; v, tmp = n zeros
repeat 10 times: mulAtAv(u, v, tmp); mulAtAv(v, u, tmp)
vBv = sum u[i]*v[i]; vv = sum v[i]*v[i]          (one loop, index order)
print trunc(sqrt(vBv / vv) * 1e9)
```

`(i+j)*(i+j+1)` stays below 2^63 for every n used here. Each sum runs in index order with no FMA in any column, so the checksum is exact. The norm converges to 1.274224153..., so the checksum is near 1274224153 for every large n. Publish size and checksums: in `bench.json`.
