/* deepfree, C, idiomatic std: the task of README.md with one array of
   strings per round, grown by realloc doubling, each string a malloc
   copy of its slice (nul-terminated, its length kept beside it; an empty
   slice is a null pointer and allocates no block, as in Mk, Rust and
   Zig), read back once, then released by free of every string and of
   the array. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

typedef struct {
    char *p;
    size_t len;
} text;

static uint64_t round_(const char *src, size_t n, uint64_t seed) {
    text *l = 0;
    size_t len = 0, cap = 0;
    uint64_t st = seed;
    for (size_t i = 0; i < n; i++) {
        st += 0x9e3779b97f4a7c15ULL;
        uint64_t v = mix(st);
        size_t k = (size_t)(v % 64);
        size_t off = (size_t)((v >> 16) % 4032);
        char *t = 0; /* an empty string owns no block, as in Mk, Rust and Zig */
        if (k > 0) {
            t = malloc(k + 1);
            if (!t) abort();
            memcpy(t, src + off, k);
            t[k] = 0;
        }
        if (len == cap) {
            cap = cap ? cap * 2 : 4;
            text *g = realloc(l, cap * sizeof *l);
            if (!g) abort();
            l = g;
        }
        l[len].p = t;
        l[len].len = k;
        len++;
    }
    uint64_t sum = 0;
    for (size_t i = 0; i < len; i++) {
        sum += l[i].len;
        if (l[i].len > 0) sum += (uint64_t)(unsigned char)l[i].p[0] * 7 + (uint64_t)(unsigned char)l[i].p[l[i].len - 1];
    }
    for (size_t i = 0; i < len; i++) free(l[i].p);
    free(l);
    return sum;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: deepfree <n> <rounds> <seed>\n");
        return 2;
    }
    size_t n = (size_t)strtoull(argv[1], 0, 10);
    uint64_t rounds = strtoull(argv[2], 0, 10);
    uint64_t seed = strtoull(argv[3], 0, 10);
    char buf[4096];
    uint64_t st = seed ^ 0x5555555555555555ULL;
    for (int i = 0; i < 4096; i++) {
        st += 0x9e3779b97f4a7c15ULL;
        buf[i] = (char)(mix(st) % 26 + 'a');
    }
    uint64_t total = 0;
    for (uint64_t r = 0; r < rounds; r++) total += round_(buf, n, seed + r);
    printf("%llu\n", (unsigned long long)total);
    return 0;
}
