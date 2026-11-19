# arena-reset (idiomatic, arena allocation and reset)

**Task.** Build a linked list of n small nodes in an arena, walk it, and reset the arena, many rounds over one arena, with each language's standard arena. This row prices `Arena`'s bump allocation and `Reset`. `arena-reset <n> <rounds> <seed>`:

```
a = one arena, made before the rounds
st = seed; total = 0
for r in 0 .. rounds-1:
    head = none
    for i in 0 .. n-1:
        st = st + G
        node = a new node in a: { val: mix(st), next: head }      (one allocation per node)
        head = node
    i = 0; s = 0
    for node from head along next: i = i + 1; s = s + node.val * i  (wrapping)
    total = total + s + i
    reset a, with the std's own reset policy (below)
```

`G` and `mix` are splitmix64's (tests/bench/README.md, "Inputs"). A node is 16 bytes (a `u64` and a reference), so the row prices the arena's bump path per small object and its reset, and the walk reads every node once.

**Cells.**

| Column | Arena | Node, link, reset |
|---|---|---|
| mk | `Arena.New(sys.heap)` | `Arena.Put(Node(...))`, a `?&Node` link, `Arena.Reset` |
| zig-fast, zig-safe | `std.heap.ArenaAllocator` over `std.heap.page_allocator` | `create(Node)`, a `?*const Node` link, `reset(.retain_capacity)` |
| rust | none: no std arena (bumpalo is a crate) | |
| go | none: no std arena (the `arena` experiment was removed) | |
| c | none: no std arena | |

**Reset policies.** The two resets differ, and each column uses its own std's:
- Mk's `Arena.Reset` keeps some chunks and returns the others to the heap. Its doc comment (std/core/mem_arena.mk, read for this text only, not for the Mk program): "Keeps the first chunk (it holds the header), the current one, the largest ordinary chunk, and the current aligned chunk, the largest aligned one, and bumps both current ones from their start; every other chunk goes back to the heap. next is unchanged, so growth resumes where it was." doc/reference.md says only "`Reset()` to reuse". How often a round goes back to the heap for chunks this row's 16 MiB a round needs is not stated here: Mk's chunk growth was not read for this text.
- Zig's `reset(.retain_capacity)` keeps all of its capacity. Its doc comment (Zig 0.17 lib/std/heap/ArenaAllocator.zig, `ResetMode`): "This will pre-heat the arena for future allocations by allocating a large enough buffer for all previously done allocations. ... If `reset()` is used in a loop, this means that after the biggest operation, no memory allocations are performed anymore." The code frees every chunk of a list but the last and resizes that one to the list's total capacity.



Sizes: n = 2^20 in both sizes; quick 4 rounds, publish 200 rounds. The publish size is not yet checked against the 1 s floor: `tools/bench verify -publish-size arena-reset` on a quiet machine sets it. Overflow class `wraps`.
