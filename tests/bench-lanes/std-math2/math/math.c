// math, C, idiomatic std: <math.h>'s double and float functions (the
// platform C library: the UCRT on Windows, glibc on Linux) over 4,096
// inputs built from splitmix64 bits (README.md), reps passes, pass r
// starting at input r. The checksum adds bits >> 32 of each double result
// (bits >> 16 of each float result) with wrapping unsigned adds. One loop
// per function, each call direct; built with -ffp-contract=off like every
// C cell, and fma is the library's fma.
// The static UCRT picks an FMA3 and AVX2 body of exp, log, pow, sin, sinh,
// atan2, expf, logf and sinf, and the vfmadd instruction for fma, at run
// time on a CPU that has them (cbrt has one body)
// (its __use_fma3_lib flag). The table's one CPU target is x86-64-v2
// (tests/bench/README.md), so main turns that choice off first with the
// UCRT's own switch, _set_FMA3_enable(0), and exits with status 3 if
// _get_FMA3_enable() still reports it on. glibc (Linux) has no such switch:
// its choice is the environment's (GLIBC_TUNABLES), tools/bench's to set.
// Usage: math <fn> <reps> <seed>.
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define N 4096

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9ULL;
    z = (z ^ (z >> 27)) * 0x94D049BB133111EBULL;
    return z ^ (z >> 31);
}

static double f64of(uint64_t z, int lo, int hi, int sign_bit) {
    uint64_t e = (uint64_t)(lo + 1023) + (z >> 53) % (uint64_t)(hi - lo + 1);
    uint64_t sign = sign_bit ? (z & 0x0010000000000000ULL) << 11 : 0;
    uint64_t b = sign | e << 52 | (z & 0x000FFFFFFFFFFFFFULL);
    double d;
    memcpy(&d, &b, 8);
    return d;
}

static float f32of(uint64_t z, int lo, int hi, int sign_bit) {
    uint64_t e = (uint64_t)(lo + 127) + (z >> 53) % (uint64_t)(hi - lo + 1);
    uint64_t sign = sign_bit ? (z & 0x0010000000000000ULL) >> 21 : 0;
    uint32_t b = (uint32_t)(sign | e << 23 | (z & 0x7FFFFF));
    float f;
    memcpy(&f, &b, 4);
    return f;
}

static uint64_t bits64(double d) {
    uint64_t b;
    memcpy(&b, &d, 8);
    return b;
}

static uint32_t bits32(float f) {
    uint32_t b;
    memcpy(&b, &f, 4);
    return b;
}

static double xs[N], ys[N], zs[N];
static float fs[N];

#define LOOP1(F) for (int i = 0; i < N; i++) acc += bits64(F(xs[(i + r) & (N - 1)])) >> 32
#define LOOPF(F) for (int i = 0; i < N; i++) acc += (uint64_t)(bits32(F(fs[(i + r) & (N - 1)])) >> 16)

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: math <fn> <reps> <seed>\n");
        return 2;
    }
#if defined(_WIN32) && defined(_M_X64)
    (void)_set_FMA3_enable(0);
    if (_get_FMA3_enable() != 0) {
        fprintf(stderr, "math: the UCRT's FMA3 paths are still on\n");
        return 3;
    }
#endif
    const char *name = argv[1];
    long reps = atol(argv[2]);
    uint64_t st = strtoull(argv[3], NULL, 10);
    int lo = -10, hi = 10, sg = 1; // atan2
    if (!strcmp(name, "exp") || !strcmp(name, "expf")) { lo = -10; hi = 5; sg = 1; }
    else if (!strcmp(name, "log") || !strcmp(name, "logf")) { lo = -100; hi = 100; sg = 0; }
    else if (!strcmp(name, "sin") || !strcmp(name, "sinf")) { lo = -10; hi = 2; sg = 1; }
    else if (!strcmp(name, "sinlarge")) { lo = 20; hi = 60; sg = 1; }
    else if (!strcmp(name, "pow")) { lo = -4; hi = 3; sg = 0; }
    else if (!strcmp(name, "sinh")) { lo = -10; hi = 4; sg = 1; }
    else if (!strcmp(name, "cbrt")) { lo = -200; hi = 200; sg = 1; }
    else if (!strcmp(name, "fma")) { lo = -5; hi = 5; sg = 1; }
    int f32row = !strcmp(name, "expf") || !strcmp(name, "logf") || !strcmp(name, "sinf");
    for (int i = 0; i < N; i++) {
        st += 0x9E3779B97F4A7C15ULL;
        uint64_t a = mix(st);
        st += 0x9E3779B97F4A7C15ULL;
        uint64_t b = mix(st);
        st += 0x9E3779B97F4A7C15ULL;
        uint64_t c = mix(st);
        xs[i] = f64of(a, lo, hi, sg);
        if (f32row) fs[i] = f32of(a, lo, hi, sg);
        ys[i] = !strcmp(name, "pow") ? f64of(b, -4, 4, 1) : f64of(b, lo, hi, sg);
        zs[i] = f64of(c, lo, hi, sg);
    }
    uint64_t acc = 0;
    for (long r = 0; r < reps; r++) {
        if (!strcmp(name, "exp")) LOOP1(exp);
        else if (!strcmp(name, "log")) LOOP1(log);
        else if (!strcmp(name, "sin") || !strcmp(name, "sinlarge")) LOOP1(sin);
        else if (!strcmp(name, "sinh")) LOOP1(sinh);
        else if (!strcmp(name, "cbrt")) LOOP1(cbrt);
        else if (!strcmp(name, "pow")) {
            for (int i = 0; i < N; i++) { int k = (i + r) & (N - 1); acc += bits64(pow(xs[k], ys[k])) >> 32; }
        } else if (!strcmp(name, "atan2")) {
            for (int i = 0; i < N; i++) { int k = (i + r) & (N - 1); acc += bits64(atan2(ys[k], xs[k])) >> 32; }
        } else if (!strcmp(name, "fma")) {
            for (int i = 0; i < N; i++) { int k = (i + r) & (N - 1); acc += bits64(fma(xs[k], ys[k], zs[k])) >> 32; }
        }
        else if (!strcmp(name, "expf")) LOOPF(expf);
        else if (!strcmp(name, "logf")) LOOPF(logf);
        else if (!strcmp(name, "sinf")) LOOPF(sinf);
    }
    printf("%llu\n", (unsigned long long)acc);
    return 0;
}
