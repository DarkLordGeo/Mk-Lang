// fmtints, C: the reference algorithm of README.md. The textbook conversion
// written out (one digit per division by 10 into a 20-byte temporary,
// copied out byte by byte), not printf: the idiomatic fmt row compares the
// std formatters. A uint8_t output buffer from malloc, int64_t positions and
// sums (overflow UB, never reached), uint64_t digits. No bounds checks.
// Built by tools/bench with clang 21: -O3 -march=x86-64-v2 -ffp-contract=off
// -fms-runtime-lib=static -fuse-ld=lld.
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
        fprintf(stderr, "usage: fmtints <n> <rounds> <seed>\n");
        return 2;
    }
    int64_t n = strtoll(argv[1], NULL, 10);
    int64_t rounds = strtoll(argv[2], NULL, 10);
    uint64_t st = strtoull(argv[3], NULL, 10);
    int64_t *xs = malloc((size_t)n * sizeof(int64_t));
    uint8_t *buf = malloc((size_t)n * 12);
    if (xs == NULL || buf == NULL) {
        return 1;
    }
    for (int64_t i = 0; i < n; i++) {
        st += 0x9e3779b97f4a7c15u;
        xs[i] = (int64_t)(mix(st) % 2000000001u) - 1000000000;
    }
    uint8_t tmp[20] = {0};
    int64_t total = 0;
    for (int64_t r = 0; r < rounds; r++) {
        int64_t pos = 0;
        for (int64_t i = 0; i < n; i++) {
            int64_t x = xs[i];
            uint64_t u;
            if (x < 0) {
                buf[pos] = 45;
                pos += 1;
                u = (uint64_t)(-x);
            } else {
                u = (uint64_t)x;
            }
            int64_t k = 20;
            for (;;) {
                k -= 1;
                tmp[k] = (uint8_t)(48 + u % 10);
                u = u / 10;
                if (u == 0) {
                    break;
                }
            }
            for (int64_t j = k; j < 20; j++) {
                buf[pos] = tmp[j];
                pos += 1;
            }
            buf[pos] = 10;
            pos += 1;
        }
        int64_t sum = 0;
        for (int64_t i = 0; i < pos; i++) {
            sum += buf[i];
        }
        total += sum;
    }
    printf("%lld\n", (long long)total);
    free(xs);
    free(buf);
    return 0;
}
