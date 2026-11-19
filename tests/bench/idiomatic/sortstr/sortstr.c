// sortstr, C, idiomatic std: the task of README.md with qsort, the C
// standard library's one sort, over views {pointer, length} into one
// buffer, through an indirect comparator (cmp below: memcmp of the common
// prefix, then the lengths, which is bytewise order; called through a
// function pointer for every comparison). Unstable. Usage: sortstr <n>
// <rounds> <seed>.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

struct view {
    const unsigned char *p;
    size_t n;
};

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

static int cmp(const void *a, const void *b) {
    const struct view *x = a, *y = b;
    size_t m = x->n < y->n ? x->n : y->n;
    int c = memcmp(x->p, y->p, m);
    if (c != 0) return c;
    return (x->n > y->n) - (x->n < y->n);
}

static uint64_t fnv(struct view v) {
    uint64_t h = 0xcbf29ce484222325ULL;
    for (size_t i = 0; i < v.n; i++) h = (h ^ v.p[i]) * 0x100000001b3ULL;
    return h;
}

static uint64_t check(const struct view *xs, size_t n) {
    uint64_t sum = 0;
    for (size_t i = 0; i < n; i++) {
        if (i > 0 && cmp(&xs[i - 1], &xs[i]) > 0) return 0xdead;
        sum += fnv(xs[i]) * (uint64_t)(i % 1000 + 1);
    }
    return sum;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: sortstr <n> <rounds> <seed>\n");
        return 2;
    }
    size_t n = (size_t)strtoll(argv[1], 0, 10);
    long long rounds = strtoll(argv[2], 0, 10);
    uint64_t st = (uint64_t)strtoll(argv[3], 0, 10);
    unsigned char *buf = malloc(n * 16);
    struct view *orig = malloc(n * sizeof *orig), *work = malloc(n * sizeof *work);
    if (!buf || !orig || !work) return 1;
    size_t p = 0;
    for (size_t i = 0; i < n; i++) {
        size_t start = p;
        st += 0x9e3779b97f4a7c15ULL;
        size_t l = 1 + (size_t)(mix(st) % 16);
        for (size_t j = 0; j < l; j++) {
            st += 0x9e3779b97f4a7c15ULL;
            buf[p++] = (unsigned char)(0x61 + mix(st) % 26);
        }
        orig[i].p = buf + start;
        orig[i].n = l;
    }
    uint64_t total = 0;
    for (long long r = 0; r < rounds; r++) {
        memcpy(work, orig, n * sizeof *work);
        qsort(work, n, sizeof *work, cmp);
        total += check(work, n);
    }
    printf("%llu\n", (unsigned long long)total);
    return 0;
}
