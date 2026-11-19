// digits, C, idiomatic std: snprintf "%.16e" (variant exact17) and "%.6f"
// (variant fixed; the UCRT and glibc print the exact value correctly
// rounded) over 4,096 positive f64 inputs built from splitmix64 bits
// (README.md), reps passes, pass r starting at input r. The digits and exponent are read back from the
// text, trailing zeros dropped, and folded as every column folds them. C
// has no shortest form (variant shortest is skipped, bench.json).
// Usage: digits <variant> <reps> <seed>.
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

static uint64_t fold(const char *b, int n) {
    int k = 0;
    while (k < n && b[k] != 'e') k++;
    int last = 0;
    for (int i = 0; i < k; i++) {
        if (b[i] != '.' && b[i] != '0') last = i + 1;
    }
    uint64_t len = 0, sum = 0;
    for (int i = 0; i < last; i++) {
        if (b[i] != '.') {
            len++;
            if (i < last - 1) sum += (uint64_t)(b[i] - '0');
        }
    }
    long e = strtol(b + k + 1, 0, 10);
    return len * 1000003ULL + (uint64_t)(e + 400) * 1009ULL + sum;
}

/* fold_f reads ddd.dddddd: the digits with leading and trailing zeros
   dropped, the exponent from the point's place. */
static uint64_t fold_f(const char *b, int n) {
    char ds[64];
    int nd = 0, k = n;
    for (int i = 0; i < n; i++) {
        if (b[i] == '.') k = i;
        else ds[nd++] = b[i];
    }
    int lo = 0, dp = k;
    while (lo < nd && ds[lo] == '0') { lo++; dp--; }
    while (nd > lo && ds[nd - 1] == '0') nd--;
    if (nd == lo) return 400ULL * 1009ULL;
    uint64_t sum = 0;
    for (int i = lo; i < nd - 1; i++) sum += (uint64_t)(ds[i] - '0');
    return (uint64_t)(nd - lo) * 1000003ULL + (uint64_t)(dp - 1 + 400) * 1009ULL + sum;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: digits <variant> <reps> <seed>\n");
        return 2;
    }
    int fixed = strcmp(argv[1], "fixed") == 0;
    if (!fixed && strcmp(argv[1], "exact17") != 0) {
        fprintf(stderr, "digits: C has no shortest form\n");
        return 2;
    }
    long long reps = strtoll(argv[2], 0, 10);
    uint64_t st = (uint64_t)strtoull(argv[3], 0, 10);
    static double xs[N];
    for (int i = 0; i < N; i++) {
        st += 0x9E3779B97F4A7C15ULL;
        uint64_t z = mix(st);
        uint64_t e = fixed ? 1003 + (z >> 53) % 81 : 723 + (z >> 53) % 601;
        uint64_t b = (e << 52) | (z & 0x000FFFFFFFFFFFFFULL);
        memcpy(&xs[i], &b, 8);
    }
    uint64_t acc = 0;
    char buf[64];
    for (long long r = 0; r < reps; r++) {
        for (int i = 0; i < N; i++) {
            double x = xs[(i + r) & (N - 1)];
            if (fixed) {
                int n = snprintf(buf, sizeof buf, "%.6f", x);
                acc += fold_f(buf, n);
            } else {
                int n = snprintf(buf, sizeof buf, "%.16e", x);
                acc += fold(buf, n);
            }
        }
    }
    printf("%llu\n", (unsigned long long)acc);
    return 0;
}
