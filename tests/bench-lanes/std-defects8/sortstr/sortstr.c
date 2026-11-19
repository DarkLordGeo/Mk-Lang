/* sortstr, C, idiomatic std: the task of README.md with the C library's
   qsort over an array of pointers to 16-character NUL-terminated keys in
   one buffer, compared by strcmp. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const char hex[] = "0123456789abcdef";

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

static int cmp(const void *a, const void *b) {
    return strcmp(*(char *const *)a, *(char *const *)b);
}

static uint64_t unhex(const char *s) {
    uint64_t v = 0;
    for (int i = 0; i < 16; i++) {
        char c = s[i];
        uint64_t d = c <= '9' ? (uint64_t)(c - '0') : (uint64_t)(c - 'a' + 10);
        v = (v << 4) | d;
    }
    return v;
}

static uint64_t fold(char *const *xs, size_t n) {
    uint64_t sum = unhex(xs[n - 1]);
    for (size_t i = 0; i < n; i += 1024) sum += unhex(xs[i]) * (uint64_t)i;
    return sum;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: sortstr <n> <rounds> <seed>\n");
        return 2;
    }
    size_t n = (size_t)strtoull(argv[1], 0, 10);
    uint64_t rounds = strtoull(argv[2], 0, 10);
    uint64_t seed = strtoull(argv[3], 0, 10);
    char *buf = malloc(n * 17);
    char **xs = malloc(n * sizeof *xs);
    if (!buf || !xs) return 1;
    uint64_t total = 0;
    for (uint64_t r = 0; r < rounds; r++) {
        uint64_t st = seed + r * 0x5555555555555555ULL;
        for (size_t i = 0; i < n; i++) {
            st += 0x9e3779b97f4a7c15ULL;
            uint64_t z = mix(st);
            char *k = buf + i * 17;
            for (int j = 0; j < 16; j++) k[j] = hex[(z >> (60 - 4 * j)) & 15];
            k[16] = 0;
            xs[i] = k;
        }
        qsort(xs, n, sizeof *xs, cmp);
        total += fold(xs, n);
    }
    printf("%llu\n", (unsigned long long)total);
    free(xs);
    free(buf);
    return 0;
}
