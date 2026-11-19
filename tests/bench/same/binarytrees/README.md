# binarytrees

The Benchmarks Game's binary-trees with the allocator fixed, so the row measures recursion, stores and loads, not five different allocators. It covers what a separate `bintrees` kernel would measure. Overflow class `never`.

**Allocator, the same in every language.** One buffer of `2^(maxDepth+2) + 1` nodes, allocated and zeroed once at the start. A node is two unsigned 32-bit indices `{left, right}` (8 bytes). A bump index `top` starts at 1; index 0 is the null node. A tree is freed by resetting `top`. No language's allocator runs per node. The buffer and `top` live together in one pool record passed to `build` by pointer (C `struct pool *`, Rust `&mut Pool`, Zig `*Pool`, Go `*pool`, Mk `mut Pool`), and `check` takes the node buffer; no column keeps them in globals, which would let one optimizer prove that the node stores never alias `top` while the others must prove it another way.

```
build(depth):
    idx = top; top = top + 1
    if depth > 0: l = build(depth-1); r = build(depth-1); node[idx] = {l, r}
    else:         node[idx] = {0, 0}
    return idx
check(idx): if node[idx].left == 0: 1 else 1 + check(left) + check(right)

minDepth = 4; maxDepth = max(minDepth + 2, n)
top = 1; total = check(build(maxDepth + 1))            stretch tree
top = 1; long = build(maxDepth); mark = top             long-lived tree
for d = minDepth; d <= maxDepth; d = d + 2:
    repeat 2^(maxDepth - d + minDepth) times:
        top = mark; total = total + check(build(d))
total = total + check(long)
print total
```

The idiomatic binary-trees, with each language's own allocation (malloc and free, `Box`, Go's GC, Zig's `ArenaAllocator`, Mk's arena), would be an idiomatic row, not this one. Publish size and checksums: in `bench.json`.
