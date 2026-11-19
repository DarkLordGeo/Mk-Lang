// sieve, C: the reference algorithm of README.md.
// Sieve of Eratosthenes over n + 1 byte flags, repeated `rounds` times: set
// every flag to 1 with a loop, then for i from 2 to n count each set flag and
// clear its multiples from i * i up in steps of i. int64_t indices and
// counters (overflow is UB in C; no input of the stated sizes reaches it).
// No bounds checks. Built by tools/bench with clang 21: -O3 -march=x86-64-v2
// -ffp-contract=off -fms-runtime-lib=static -fuse-ld=lld.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

// -DBENCH_COUNT=1 builds the counting variant (tests/bench/README.md).
#ifndef BENCH_COUNT
#define BENCH_COUNT 0
#endif

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: sieve <n> <rounds>\n");
        return 2;
    }
    int64_t n = strtoll(argv[1], NULL, 10);
    int64_t rounds = strtoll(argv[2], NULL, 10);
    uint8_t *flags = malloc((size_t)n + 1);
    if (flags == NULL) {
        return 1;
    }
    int64_t total = 0;
    int64_t marks = 0;
    for (int64_t r = 0; r < rounds; r++) {
        for (int64_t i = 0; i < n + 1; i++) {
            flags[i] = 1;
        }
        int64_t count = 0;
        for (int64_t i = 2; i < n + 1; i++) {
            if (flags[i] != 0) {
                count += 1;
                int64_t j = i * i;
                while (j <= n) {
                    flags[j] = 0;
                    if (BENCH_COUNT) {
                        marks += 1;
                    }
                    j += i;
                }
            }
        }
        total += count;
    }
    printf("%lld\n", (long long)total);
    if (BENCH_COUNT) {
        printf("counts marks=%lld\n", (long long)marks);
    }
    free(flags);
    return 0;
}
