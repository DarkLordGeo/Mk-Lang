// fmt, C, idiomatic std: the task of README.md with snprintf and strtoll
// for the i64, and snprintf("%.17g") and strtod for the f64. C's std has
// no shortest round-trip format, so the float is printed with 17
// significant digits, which round-trips but is longer than the other
// columns' output; the cell is labelled so. Usage: fmt <n> <rounds>
// <seed>.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: fmt <n> <rounds> <seed>\n");
        return 2;
    }
    long long n = strtoll(argv[1], 0, 10);
    long long rounds = strtoll(argv[2], 0, 10);
    uint64_t st = (uint64_t)strtoll(argv[3], 0, 10);
    char buf[64];
    uint64_t sum = 0;
    int64_t ok = 0;
    for (long long r = 0; r < rounds; r++) {
        for (long long i = 0; i < n; i++) {
            st += 0x9e3779b97f4a7c15ULL;
            uint64_t z = mix(st);
            int64_t x = (int64_t)(z >> 1);
            if (z & 1) x = -x;
            st += 0x9e3779b97f4a7c15ULL;
            double f = (double)(mix(st) >> 11) * (1.0 / 9007199254740992.0) * 1e6;
            snprintf(buf, sizeof buf, "%lld", (long long)x);
            int64_t y = strtoll(buf, 0, 10);
            snprintf(buf, sizeof buf, "%.17g", f);
            double g = strtod(buf, 0);
            sum += (uint64_t)y + (uint64_t)(int64_t)g;
            if (g == f) ok++;
        }
    }
    printf("%lld\n", (long long)(sum + (uint64_t)ok));
    return 0;
}
