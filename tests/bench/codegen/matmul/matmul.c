// Codegen kernel matmul, C side (tests/bench/README.md "The codegen table";
// "The harness"). The same algorithm as
// matmul.mk's Fill and Run, and as ../../same/matmul: three flat row-major
// n * n double matrices; Fill (not timed) sets a and b from splitmix64 as
// (mix & 255) / 256.0, alternating a then b per element, and zeroes c; Run
// (timed) is one i-k-j product c[i*n+j] = c[i*n+j] + aik * b[k*n+j], then
// the sum of c, returned as trunc(total * 2^16). Every product is a
// multiple of 2^-16 and every partial sum stays below n^3 < 2^28 for
// n <= 512, so the sum is exact and its order does not change it. No FMA:
// tools/bench passes -ffp-contract=off, and Mk never contracts. C has
// no bounds or overflow checks; Mk checks the indices and their + and *.
// The buffers are 64-byte-aligned static arrays of fixed capacity, as Mk's
// are aligned to 64 bytes; n comes in at run time.
//
// Freestanding: no C library. tools/bench compiles it with clang 21
// (codegenCCommand in tools/bench/codegen.go) and links the object into the
// Mk program.
#include <stdint.h>

#define CAPN 512
#define CAP (CAPN * CAPN)

static _Alignas(64) double a[CAP];
static _Alignas(64) double b[CAP];
static _Alignas(64) double c[CAP];

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9u;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebu;
    return z ^ (z >> 31);
}

void bench_c_matmul_fill(uint64_t seed, int64_t n) {
    uint64_t st = seed;
    for (int64_t i = 0; i < n * n; i++) {
        st += 0x9e3779b97f4a7c15u;
        a[i] = (double)(mix(st) & 255) / 256.0;
        st += 0x9e3779b97f4a7c15u;
        b[i] = (double)(mix(st) & 255) / 256.0;
        c[i] = 0.0;
    }
}

int64_t bench_c_matmul_run(int64_t n) {
    for (int64_t i = 0; i < n; i++) {
        for (int64_t k = 0; k < n; k++) {
            double aik = a[i * n + k];
            for (int64_t j = 0; j < n; j++) {
                c[i * n + j] = c[i * n + j] + aik * b[k * n + j];
            }
        }
    }
    double total = 0.0;
    for (int64_t i = 0; i < n * n; i++) {
        total = total + c[i];
    }
    return (int64_t)(total * 65536.0);
}

// bench_c_matmul_addr is the data address of a Mk []f64 ({ptr, len},
// passed by reference under the Windows x64 ABI), so the Mk side can align
// its buffers to 64 bytes without std.raw. It reads the slice header only.
struct mk_slice {
    const void *ptr;
    int64_t len;
};

uint64_t bench_c_matmul_addr(const struct mk_slice *s) {
    return (uint64_t)(uintptr_t)s->ptr;
}
