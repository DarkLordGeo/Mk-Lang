// search, C, idiomatic std: the task of README.md with the C standard
// library's one binary search, bsearch, through an indirect comparator (cmp
// below, called through a function pointer for every probe). bsearch finds
// exact matches only and returns a pointer or NULL; the row's checksum adds
// 1 for a miss, so C needs no insertion point. Keys uint64_t, distinct.
// Usage: search <n> <lookups> <seed>.
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

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: search <n> <lookups> <seed>\n");
        return 2;
    }
    size_t n = (size_t)strtoll(argv[1], 0, 10);
    long long lookups = strtoll(argv[2], 0, 10);
    uint64_t st = (uint64_t)strtoll(argv[3], 0, 10);
    uint64_t *xs = malloc(n * sizeof *xs);
    if (!xs) return 1;
    for (size_t i = 0; i < n; i++) xs[i] = 2 * (uint64_t)i + 1;
    uint64_t span = 2 * (uint64_t)n + 1;
    uint64_t total = 0;
    for (long long j = 0; j < lookups; j++) {
        st += 0x9e3779b97f4a7c15ULL;
        uint64_t k = mix(st) % span;
        const uint64_t *p = bsearch(&k, xs, n, sizeof *xs, cmp);
        if (p) total += (uint64_t)(p - xs) + 1;
        else total += 1;
    }
    printf("%llu\n", (unsigned long long)total);
    free(xs);
    return 0;
}
