// fannkuch, C: the reference algorithm of README.md (the Benchmarks Game's
// fannkuch-redux, sequential, not its tuned programs). Three int64_t arrays
// of n from malloc, the same loops and the same next-permutation step as
// every other column. No bounds checks. Built by tools/bench with clang 21:
// -O3 -march=x86-64-v2 -ffp-contract=off -fms-runtime-lib=static -fuse-ld=lld.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

int main(int argc, char **argv) {
    if (argc != 2) {
        fprintf(stderr, "usage: fannkuch <n>\n");
        return 2;
    }
    int64_t n = strtoll(argv[1], NULL, 10);
    int64_t *perm = malloc((size_t)n * sizeof(int64_t));
    int64_t *perm1 = malloc((size_t)n * sizeof(int64_t));
    int64_t *count = malloc((size_t)n * sizeof(int64_t));
    if (perm == NULL || perm1 == NULL || count == NULL) {
        return 1;
    }
    for (int64_t i = 0; i < n; i++) {
        perm1[i] = i;
    }
    int64_t r = n, permCount = 0, checksum = 0, maxFlips = 0;
    int done = 0;
    while (!done) {
        while (r != 1) {
            count[r - 1] = r;
            r -= 1;
        }
        for (int64_t i = 0; i < n; i++) {
            perm[i] = perm1[i];
        }
        int64_t flips = 0;
        int64_t k = perm[0];
        while (k != 0) {
            int64_t i = 0, j = k;
            while (i < j) {
                int64_t t = perm[i];
                perm[i] = perm[j];
                perm[j] = t;
                i += 1;
                j -= 1;
            }
            flips += 1;
            k = perm[0];
        }
        if (flips > maxFlips) {
            maxFlips = flips;
        }
        if (permCount % 2 == 0) {
            checksum += flips;
        } else {
            checksum -= flips;
        }
        for (;;) {
            if (r == n) {
                done = 1;
                break;
            }
            int64_t perm0 = perm1[0];
            for (int64_t i = 0; i < r; i++) {
                perm1[i] = perm1[i + 1];
            }
            perm1[r] = perm0;
            count[r] -= 1;
            if (count[r] > 0) {
                break;
            }
            r += 1;
        }
        permCount += 1;
    }
    printf("%lld\n", (long long)(checksum * 1000 + maxFlips));
    free(perm);
    free(perm1);
    free(count);
    return 0;
}
