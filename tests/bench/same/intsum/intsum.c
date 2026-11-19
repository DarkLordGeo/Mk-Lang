// intsum, C: the reference algorithm of README.md.
// Same data and loops as every other column: n int64 values from splitmix64
// masked to 0..1023, then `rounds` passes of an index loop summing them, each
// pass writing its sum & 1023 back into element r % n so no pass can be
// hoisted or folded. Signed int64 throughout: the sum's overflow is UB in C
// (the licence for `add nsw`), which no input of the stated sizes reaches.
// No bounds checks (C has none). Built by tools/bench with clang 21:
// -O3 -march=x86-64-v2 -ffp-contract=off -fms-runtime-lib=static -fuse-ld=lld.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9u;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebu;
    return z ^ (z >> 31);
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: intsum <n> <rounds> <seed>\n");
        return 2;
    }
    int64_t n = strtoll(argv[1], NULL, 10);
    int64_t rounds = strtoll(argv[2], NULL, 10);
    uint64_t st = strtoull(argv[3], NULL, 10);
    int64_t *xs = malloc((size_t)n * sizeof *xs);
    if (xs == NULL) {
        return 1;
    }
    for (int64_t i = 0; i < n; i++) {
        st += 0x9e3779b97f4a7c15u;
        xs[i] = (int64_t)(mix(st) & 1023);
    }
    int64_t total = 0;
    for (int64_t r = 0; r < rounds; r++) {
        int64_t s = 0;
        for (int64_t i = 0; i < n; i++) {
            s += xs[i];
        }
        total += s;
        xs[r % n] = s & 1023;
    }
    printf("%lld\n", (long long)total);
    free(xs);
    return 0;
}
