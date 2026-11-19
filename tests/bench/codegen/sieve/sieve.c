// Codegen kernel sieve, C side (tests/bench/README.md "The codegen table";
// "The harness"). The same algorithm as
// sieve.mk's Fill and Run, and as ../../same/sieve: n + 1 byte flags set to
// 1 (Fill, not timed), then the sieve of Eratosthenes over them, counting
// the primes up to n (Run, timed): for each i from 2 with flags[i] set,
// clear i*i, i*i + i, ... up to n. Signed int64 indices; C has no bounds or
// overflow checks, Mk checks both, so the ratio prices them on a
// store-bound loop with a run-time stride. The buffer is a 64-byte-aligned
// static array of fixed capacity, as Mk's is aligned to 64 bytes; n comes
// in at run time.
//
// Freestanding: no C library. tools/bench compiles it with clang 21
// (codegenCCommand in tools/bench/codegen.go) and links the object into the
// Mk program.
#include <stdint.h>

#define CAP 16777217

static _Alignas(64) uint8_t flags[CAP];

void bench_c_sieve_fill(int64_t n) {
    for (int64_t i = 0; i < n + 1; i++) {
        flags[i] = 1;
    }
}

int64_t bench_c_sieve_run(int64_t n) {
    int64_t count = 0;
    for (int64_t i = 2; i < n + 1; i++) {
        if (flags[i] != 0) {
            count += 1;
            int64_t j = i * i;
            while (j <= n) {
                flags[j] = 0;
                j += i;
            }
        }
    }
    return count;
}

// bench_c_sieve_addr is the data address of a Mk []u8 ({ptr, len}, passed
// by reference under the Windows x64 ABI), so the Mk side can align its
// buffer to 64 bytes without std.raw. It reads the slice header only.
struct mk_slice {
    const void *ptr;
    int64_t len;
};

uint64_t bench_c_sieve_addr(const struct mk_slice *s) {
    return (uint64_t)(uintptr_t)s->ptr;
}
