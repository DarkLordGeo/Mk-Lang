// Codegen kernel intsum, C side (tests/bench/README.md "The codegen table";
// "The harness"). The same algorithm as
// intsum.mk's Fill and Run, and as ../../same/intsum: n int64 values from
// splitmix64 masked to 0..1023 (Fill, not timed), then `passes` index-loop
// passes summing them, each pass writing its sum & 1023 back into element
// r % n so no pass can be hoisted (Run, timed). Signed int64: C's overflow is
// UB (the licence for `add nsw`); Mk's + is checked, so the ratio prices
// Mk's checked add and bounds checks against C's unchecked loop. The buffer
// is a 64-byte-aligned static array of fixed capacity, as Mk's is aligned to
// 64 bytes; the loop bound n comes in at run time, so clang knows neither
// the trip count nor more alignment than Mk's side does.
//
// Freestanding: no C library. tools/bench compiles it with clang 21:
// --target=x86_64-pc-windows-msvc -std=c17 -O3 -march=x86-64-v2
// -ffp-contract=off -ffreestanding -c, refuses any undefined symbol except
// the ones the Mk runtime defines (memcpy, memmove, memset, memcmp, bcmp,
// __chkstk, _fltused), and links the object into the Mk program.
#include <stdint.h>

#define CAP 4194304

static _Alignas(64) int64_t xs[CAP];

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9u;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebu;
    return z ^ (z >> 31);
}

void bench_c_intsum_fill(uint64_t seed, int64_t n) {
    uint64_t st = seed;
    for (int64_t i = 0; i < n; i++) {
        st += 0x9e3779b97f4a7c15u;
        xs[i] = (int64_t)(mix(st) & 1023);
    }
}

int64_t bench_c_intsum_run(int64_t n, int64_t passes) {
    int64_t total = 0;
    for (int64_t r = 0; r < passes; r++) {
        int64_t s = 0;
        for (int64_t i = 0; i < n; i++) {
            s += xs[i];
        }
        total += s;
        xs[r % n] = s & 1023;
    }
    return total;
}

// bench_c_intsum_addr is the data address of a Mk slice ({ptr, len}, passed by
// reference under the Windows x64 ABI), so the Mk side can align its buffer
// to 64 bytes without std.raw. It reads the slice header only.
struct mk_slice {
    const void *ptr;
    int64_t len;
};

uint64_t bench_c_intsum_addr(const struct mk_slice *s) {
    return (uint64_t)(uintptr_t)s->ptr;
}
