// Codegen kernel fmtints, C side (tests/bench/README.md "The codegen table";
// "The harness"). The same algorithm as
// fmtints.mk's Fill and Run, and as ../../same/fmtints: n int64 values from
// splitmix64 as (mix % 2000000001) - 1000000000 (Fill, not timed); Run
// (timed) writes each in decimal with a leading '-' when negative and a
// '\n' after it, one digit per division by 10 into a 20-byte temporary,
// copied out, then returns the sum of all bytes written. C has no bounds or
// overflow checks; Mk checks every index and every signed +. The buffers
// are 64-byte-aligned static arrays of fixed capacity, as Mk's are aligned
// to 64 bytes; n comes in at run time.
//
// Freestanding: no C library. tools/bench compiles it with clang 21
// (codegenCCommand in tools/bench/codegen.go) and links the object into the
// Mk program.
#include <stdint.h>

#define CAP 1048576

static _Alignas(64) int64_t xs[CAP];
static _Alignas(64) uint8_t buf[CAP * 12];

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9u;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebu;
    return z ^ (z >> 31);
}

void bench_c_fmtints_fill(uint64_t seed, int64_t n) {
    uint64_t st = seed;
    for (int64_t i = 0; i < n; i++) {
        st += 0x9e3779b97f4a7c15u;
        xs[i] = (int64_t)(mix(st) % 2000000001u) - 1000000000;
    }
}

int64_t bench_c_fmtints_run(int64_t n) {
    uint8_t tmp[20] = {0};
    int64_t pos = 0;
    for (int64_t i = 0; i < n; i++) {
        int64_t x = xs[i];
        uint64_t u;
        if (x < 0) {
            buf[pos] = 45;
            pos += 1;
            u = (uint64_t)(-x);
        } else {
            u = (uint64_t)x;
        }
        int64_t k = 20;
        for (;;) {
            k -= 1;
            tmp[k] = (uint8_t)(48 + u % 10);
            u = u / 10;
            if (u == 0) {
                break;
            }
        }
        for (int64_t j = k; j < 20; j++) {
            buf[pos] = tmp[j];
            pos += 1;
        }
        buf[pos] = 10;
        pos += 1;
    }
    int64_t sum = 0;
    for (int64_t i = 0; i < pos; i++) {
        sum += buf[i];
    }
    return sum;
}

// The data addresses of a Mk []i64 and []u8 ({ptr, len}, passed by
// reference under the Windows x64 ABI), so the Mk side can align its
// buffers to 64 bytes without std.raw. They read the slice header only.
struct mk_slice {
    const void *ptr;
    int64_t len;
};

uint64_t bench_c_fmtints_addr64(const struct mk_slice *s) {
    return (uint64_t)(uintptr_t)s->ptr;
}

uint64_t bench_c_fmtints_addr8(const struct mk_slice *s) {
    return (uint64_t)(uintptr_t)s->ptr;
}
