# ws-mask (SIMD table)

Task: `ws-mask <n> <rounds>`. The input is `n` bytes of splitmix64 output: `st = 1`; per 8 bytes `st += 0x9e3779b97f4a7c15`, `z = mix(st)` (`intsum`'s finalizer), bytes `z >> 0, z >> 8, ...` little-endian, the last word cut at `n`. Per round `r` in `0..rounds`: `kw = (r + 1) * 0x9e3779b97f4a7c15` (wrapping), the key is `kw`'s four low bytes, little-endian; mask the whole buffer in place with that key from phase `r % 4` (RFC 6455 5.3: byte `i` is XORed with `key[(phase + i) % 4]`), and add to a wrapping `u64` sum the byte at `(r * 7919) % n` and the phase the mask returns (`(phase + n) % 4`). The program prints the sum.

The buffer starts on a 64-byte boundary in every column but Mk's (the labelled exception of tests/bench/README.md: an arena slice, whose alignment Mk code cannot set without std.raw).

Variants, each at x86-64-v2 with a baseline of `rounds = 0` (the fill, subtracted): `l1v2` `16384 2000000` checksum `258138996`; `l3v2` `33554432 1000` checksum `129433`; `dramv2` `1073741824 32` checksum `4293`. Quick (`65537 1000`) `130636` for each.

Columns, one kernel in each language's own SIMD layer (the SIMD table; the same-algorithm table forbids intrinsics and hand vectorisation): a scalar head until the phase is 0, then the key repeated over a 16-byte vector XORed into 16 bytes a step on unaligned loads and stores, then a scalar tail.
- Mk: `std/websocket/frame.mk`'s `Mask`, copied byte for byte. It is not exported, so no program can call it; `TestStdWs_BenchMaskIsTheLibrarys` (std/std-ws_bench_test.go) fails when the copy and the library differ.
- C: `<emmintrin.h>` SSE2, `_mm_xor_si128`.
- Rust: `std::arch::x86_64`'s SSE2, `_mm_xor_si128` on `_mm_loadu_si128`, each pointer from a checked 16-byte slice.
- Zig: `@Vector(16, u8)`.
- Go, scalar (labelled as such): 8 bytes a step as one `uint64` XOR, the shape of gorilla/websocket's `maskBytes`. Go has no vector operation without assembly, and no WebSocket in its standard library.

The reference for the job is uWebSockets' and simdjson's byte kernels: a vector XOR with the key splat, no per-byte branch. `n = 16384` (l1v2) is std.websocket's write chunk, so a round's data stays in L1, as a client's chunk does between `PutHead` and its `WriteAll`; l3v2 and dramv2 stand for `Recv`, which unmasks a one-frame message whole.
