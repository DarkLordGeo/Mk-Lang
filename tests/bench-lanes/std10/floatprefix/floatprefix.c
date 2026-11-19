// floatprefix, C, idiomatic std: strtod, which parses the longest prefix
// that is a float and sets its end pointer, over 4,096 texts
// "<a>.<b>e<e>;" built from splitmix64 words (README.md), reps passes, pass
// r starting at text r. Each result folds bits >> 32 of the value and the
// bytes used into a wrapping u64 sum. The UCRT's and glibc's strtod round
// correctly, so the columns agree.
// Usage: floatprefix <reps> <seed>.
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

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: floatprefix <reps> <seed>\n");
        return 2;
    }
    long long reps = strtoll(argv[1], 0, 10);
    uint64_t st = (uint64_t)strtoull(argv[2], 0, 10);
    static char texts[N][40];
    for (int i = 0; i < N; i++) {
        st += 0x9E3779B97F4A7C15ULL;
        uint64_t z = mix(st);
        snprintf(texts[i], sizeof texts[i], "%llu.%llue%d;", (unsigned long long)(z % 1000000),
                 (unsigned long long)((z >> 20) % 10000000), (int)((z >> 50) % 41) - 20);
    }
    uint64_t acc = 0;
    for (long long r = 0; r < reps; r++) {
        for (int i = 0; i < N; i++) {
            const char *s = texts[(i + r) & (N - 1)];
            char *end;
            double v = strtod(s, &end);
            uint64_t b;
            memcpy(&b, &v, 8);
            acc += (b >> 32) + (uint64_t)(end - s);
        }
    }
    printf("%llu\n", (unsigned long long)acc);
    return 0;
}
