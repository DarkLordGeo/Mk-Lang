/* sort, C, idiomatic std: the task of README.md with the C library's
   qsort and a three-way comparison of uint64_t. */
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

static uint64_t fold(const uint64_t *xs, size_t n) {
    uint64_t sum = xs[n - 1];
    for (size_t i = 0; i < n; i += 1024) sum += xs[i] * (uint64_t)i;
    return sum;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: sort <n> <rounds> <seed>\n");
        return 2;
    }
    size_t n = (size_t)strtoull(argv[1], 0, 10);
    uint64_t rounds = strtoull(argv[2], 0, 10);
    uint64_t seed = strtoull(argv[3], 0, 10);
    uint64_t *xs = malloc(n * sizeof *xs);
    if (!xs) return 1;
    uint64_t total = 0;
    for (uint64_t r = 0; r < rounds; r++) {
        uint64_t st = seed + r * 0x5555555555555555ULL;
        for (size_t i = 0; i < n; i++) {
            st += 0x9e3779b97f4a7c15ULL;
            xs[i] = mix(st);
        }
        qsort(xs, n, sizeof *xs, cmp);
        total += fold(xs, n);
    }
    printf("%llu\n", (unsigned long long)total);
    free(xs);
    return 0;
}
