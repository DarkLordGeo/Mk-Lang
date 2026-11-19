# dispatchalt (same algorithm, std-dyn)

`dispatch` with the target alternating (`objs[i & 1]`), so the indirect branch is predicted and the per-call cost around it shows: in Mk the adapter's key load, compare and branch and its stack frame, which `dispatch`'s mispredicts (the same in every column) hide. Same data structures, columns and arithmetic as `../dispatch/README.md`; only the index changes. The argument still comes from splitmix64, so the calls cannot be folded. Only `tools/bench` publishes a number from this row.

```
objs = [pair(&add, Add.Step), pair(&xor, Xor.Step)]   as in dispatch
s = seed; sum = 0
for i in 0..n:
    s = s + 0x9E3779B97F4A7C15
    z = mix(s)
    sum = sum + objs[i & 1].Step(z >> 1)
print sum
```

All arithmetic is `u64` and wraps (overflow class `wraps`). Sizes: `quick` 1,000,000 calls, publish 500,000,000. Checksums from a Go reference of the pseudo-code (seed 1): quick 5382184753454588733, publish 5376492962469361624.
