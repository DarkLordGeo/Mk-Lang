// sort, C, idiomatic std: the task of README.md with the C standard
// library's one sort, qsort, through an indirect comparator (cmp below,
// called through a function pointer for every comparison; C has no typed
// or inlined std sort). Unstable. Usage: sort <n> <rounds> <seed>.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

static int cmp(const void *a, const void *b) {
    uint64_t x = *(const uint64_t *)a, y = *(const uint64_t *)b;
    return (x > y) - (x < y);
}

static uint64_t check(const uint64_t *xs, size_t n) {
    uint64_t sum = 0;
    for (size_t i = 0; i < n; i++) {
        if (i > 0 && xs[i - 1] > xs[i]) return 0xdead;
        sum += xs[i] * (uint64_t)(i % 1000 + 1);
    }
    return sum;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: sort <n> <rounds> <seed>\n");
        return 2;
    }
    size_t n = (size_t)strtoll(argv[1], 0, 10);
    long long rounds = strtoll(argv[2], 0, 10);
    uint64_t st = (uint64_t)strtoll(argv[3], 0, 10);
    uint64_t *xs = malloc(n * sizeof *xs);
    if (!xs) return 1;
    uint64_t total = 0;
    for (long long r = 0; r < rounds; r++) {
        for (int p = 0; p < 4; p++) {
            for (size_t i = 0; i < n; i++) {
                if (p == 0) {
                    st += 0x9e3779b97f4a7c15ULL;
                    xs[i] = mix(st);
                } else if (p == 1) {
                    xs[i] = (uint64_t)(n - i);
                } else if (p == 2) {
                    xs[i] = (uint64_t)(i % 1000);
                } else {
                    st += 0x9e3779b97f4a7c15ULL;
                    xs[i] = mix(st) >> 60;
                }
            }
            qsort(xs, n, sizeof *xs, cmp);
            total += check(xs, n);
        }
    }
    printf("%llu\n", (unsigned long long)total);
    free(xs);
    return 0;
}
