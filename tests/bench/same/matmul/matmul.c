// matmul, C: the reference algorithm of README.md.
// Three flat double arrays of n * n from malloc, row-major, the i-k-j loop,
// c[i*n+j] = c[i*n+j] + aik * b[k*n+j], repeated `rounds` times into the same
// c. int64_t indices (overflow UB, never reached). Every value is a multiple
// of 2^-8 below 1, so every product and partial sum is exact and the
// checksum does not depend on summation order. No bounds checks. Built by
// tools/bench with clang 21: -O3 -march=x86-64-v2 -ffp-contract=off
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
        fprintf(stderr, "usage: matmul <n> <rounds> <seed>\n");
        return 2;
    }
    int64_t n = strtoll(argv[1], NULL, 10);
    int64_t rounds = strtoll(argv[2], NULL, 10);
    uint64_t st = strtoull(argv[3], NULL, 10);
    double *a = malloc((size_t)(n * n) * sizeof(double));
    double *b = malloc((size_t)(n * n) * sizeof(double));
    double *c = calloc((size_t)(n * n), sizeof(double));
    if (a == NULL || b == NULL || c == NULL) {
        return 1;
    }
    for (int64_t i = 0; i < n * n; i++) {
        st += 0x9e3779b97f4a7c15u;
        a[i] = (double)(mix(st) & 255) / 256.0;
        st += 0x9e3779b97f4a7c15u;
        b[i] = (double)(mix(st) & 255) / 256.0;
    }
    for (int64_t r = 0; r < rounds; r++) {
        for (int64_t i = 0; i < n; i++) {
            for (int64_t k = 0; k < n; k++) {
                double aik = a[i * n + k];
                for (int64_t j = 0; j < n; j++) {
                    c[i * n + j] = c[i * n + j] + aik * b[k * n + j];
                }
            }
        }
    }
    double total = 0.0;
    for (int64_t i = 0; i < n * n; i++) {
        total = total + c[i];
    }
    printf("%lld\n", (long long)(int64_t)(total * 65536.0));
    free(a);
    free(b);
    free(c);
    return 0;
}
