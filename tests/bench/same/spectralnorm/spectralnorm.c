// spectralnorm, C: the reference algorithm of README.md (the Benchmarks
// Game's spectral-norm, scalar and single-threaded, not its tuned SIMD
// programs). A(i, j) computed from int64_t on every use, double vectors from
// malloc, sqrt from <math.h>, -ffp-contract=off. No bounds checks.
// Built by tools/bench with clang 21: -O3 -march=x86-64-v2 -ffp-contract=off
// -fms-runtime-lib=static -fuse-ld=lld.
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static double a(int64_t i, int64_t j) {
    return 1.0 / (double)((i + j) * (i + j + 1) / 2 + i + 1);
}

static void mul_av(int64_t n, const double *v, double *out) {
    for (int64_t i = 0; i < n; i++) {
        double s = 0.0;
        for (int64_t j = 0; j < n; j++) {
            s = s + a(i, j) * v[j];
        }
        out[i] = s;
    }
}

static void mul_atv(int64_t n, const double *v, double *out) {
    for (int64_t i = 0; i < n; i++) {
        double s = 0.0;
        for (int64_t j = 0; j < n; j++) {
            s = s + a(j, i) * v[j];
        }
        out[i] = s;
    }
}

static void mul_atav(int64_t n, const double *v, double *out, double *tmp) {
    mul_av(n, v, tmp);
    mul_atv(n, tmp, out);
}

int main(int argc, char **argv) {
    if (argc != 2) {
        fprintf(stderr, "usage: spectralnorm <n>\n");
        return 2;
    }
    int64_t n = strtoll(argv[1], NULL, 10);
    double *u = malloc((size_t)n * sizeof(double));
    double *v = malloc((size_t)n * sizeof(double));
    double *tmp = malloc((size_t)n * sizeof(double));
    if (u == NULL || v == NULL || tmp == NULL) {
        return 1;
    }
    for (int64_t i = 0; i < n; i++) {
        u[i] = 1.0;
    }
    for (int k = 0; k < 10; k++) {
        mul_atav(n, u, v, tmp);
        mul_atav(n, v, u, tmp);
    }
    double vbv = 0.0, vv = 0.0;
    for (int64_t i = 0; i < n; i++) {
        vbv = vbv + u[i] * v[i];
        vv = vv + v[i] * v[i];
    }
    printf("%lld\n", (long long)(int64_t)(sqrt(vbv / vv) * 1e9));
    free(u);
    free(v);
    free(tmp);
    return 0;
}
