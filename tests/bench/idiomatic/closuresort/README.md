# closuresort (idiomatic, internal comparison)

**What it measures.** The cost of a capturing `fn` literal passed to std's `Slice[T].Sort`, against the form it replaces, a context struct and a named function passed through a `SortWith` that std no longer has. The compiler lowers a capturing literal to the second form: one record of shared references to the captured names, passed as a hidden trailing parameter to every call of the comparator. So the two columns should run alike, and this row checks that they do. Both must run the same sort for the ratio to mean that: mk-ctx's copy of the sort differs from std's in two edits, so in this run the row has the mk column alone, and the like-for-like pair is mkcap against mk-ctx (Cells). It is an internal comparison of two Mk programs, labelled as such, never a cross-language ranking (`sortints` and `sort` are those).

**Task.** `closuresort <n> <rounds> <seed>`:

```
st = seed; for i in 0..n: st += 0x9e3779b97f4a7c15; src[i] = splitmix64(st) & 0xFFFFF
for r in 0..rounds:
    origin = splitmix64(seed + r) & 0xFFFFF
    xs = copy of src
    sort xs by (x - origin)^2, ascending (unstable pdqsort)
    for i in 0..n: total += (xs[i] - origin)^2 * (i + 1)      (wrapping u64)
print total
```

Equal keys have equal distances, so the checksum does not depend on where ties land. Sizes: quick `100000 2 7` (9606843552447588921), publish `10000000 2 7` (13223099190716565378).

**Cells.**

| Column | Form |
|---|---|
| mk | `xs.Sort(fn(a: &i64, b: &i64): bool { da := *a - origin ... })`: `origin` is a local of `Main`, captured |
| mk-ctx | **Held.** `sortw.SortWith(mut xs[..], &ctx, Closer)` with `struct ByDistance { origin: i64 }` and `Closer(c: &ByDistance, a: &i64, b: &i64): bool`. `mkctx/sortw/sortw.mk` is std's former `SortWith` (from an earlier `std/core/core_sort.mk`, the `Pdq*W` functions) with two edits: `SwapAt` calls `Slice.Swap` (std's `SwapAt` is std-internal and unsafe; `Swap` checks the same indices once more before it), and `BitLen` is a loop instead of `raw.Clz64`. Against mk's std `Sort` that mixes those edits into the capture's cost, so it is not published beside it |
| mkcap (staged) | the capture of mk through `mkcap/sortw/sortw.mk`, which is `mkctx/sortw/sortw.mk` with the context argument taken out and nothing else (`TestClosuresortSameSort` derives one from the other). Written and not yet run; once a run checks its checksum it becomes the row's mk column (`sources` mk = `mkcap`) beside mk-ctx, and the pair's ratio is the capture's cost alone |
| c, rust, zig, go | not in the row: it compares two Mk forms |

Both columns are built as every Mk cell is (`mk build --release --pgo=off --cpu x86-64-v2`). Overflow class `wraps` (the checksum); the distance arithmetic is checked `i64` in both.
