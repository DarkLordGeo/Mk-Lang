# term-sanitize (same-algorithm table)

**Task.** Write untrusted text to a terminal with its control and escape bytes replaced, as `term.Sanitize` does. `sanitize <n> <rounds>`:

```
st = 1
n times: st = st + G; z = mix(st); k = z mod 64; v = (z >> 8) mod 95; w = (z >> 8) mod 32
    k < 48: ' ' + v      k < 52: LF        k < 56: C3 A9 (e-acute)   k < 58: ESC
    k < 60: w (a C0)     k < 62: C2, 80 + w (a C1)                   else: E2 82 AC (euro)
rounds times (r from 0): out = sanitize(text); total += len(out); total += out[(r * 7919) mod len(out)]   (wrapping)
sanitize: each byte below 0x20 but LF and TAB, 0x7F, and each pair C2 80..9F becomes EF BF BD; every safe run is copied whole
```

`mix` is splitmix64's finalizer and G is 0x9e3779b97f4a7c15, as in the other rows.

| Column | What runs |
|---|---|
| mk | `Gen` and the scan store at an index into two buffers allocated once and zeroed (`Arena.Slice`), safe runs copied with `CopyFrom` |
| c | the same, buffers from `calloc`, runs copied with `memcpy` |
| rust | the same, buffers `vec![0u8; n]`, runs copied with `copy_from_slice` |
| zig | the same, buffers from `page_allocator` then `@memset` to 0, runs copied with `@memcpy` |
| go | the same, buffers from `make`, runs copied with `copy` |

**The same data structures.** Every column generates the text and writes the sanitized output with indexed stores into two fixed buffers (3n and 9n bytes), allocated once and zeroed, as `binarytrees` allocates its pool. No column appends to a growable container (`push`, `extend_from_slice`, `append`, with a capacity check on every run and replacement), and every column's buffers are zeroed, as Mk's `Arena.Slice` zero-fills its 12n bytes. Each language's zeroed allocation is its own: `calloc`, `vec![0; n]` and `make` may take zero pages from the OS without writing them, where Mk's arena and Zig's `@memset` write every byte, so the Mk column still touches 12n bytes the C, Rust and Go columns may not; std has no allocation that skips the fill.

No standard library of C, Rust, Zig or Go has a terminal sanitizer, so every column is the same algorithm written by hand, and the ratio compares code generation only.

The Mk column does not run std's `term.Sanitize`: its 16-byte vector scan (hand vectorisation, which this table forbids) and its writes through `Text.Writer`, which validates each run as UTF-8 (a different data structure from the peers' buffer), would both break the same-algorithm contract of tests/bench/README.md. `term.Sanitize` itself has no row: a fair one is a SIMD-table row with the same vector scan in C, Rust and Zig and a sink without validation on every side, which needs a public byte-buffer Writer for Mk.
