// mandelbrot, C: the reference algorithm of README.md.
// Scalar double, one point at a time, at most 50 iterations, the escape test
// after each step, the README's parenthesisation; -ffp-contract=off.
// int64_t counters. No intrinsics, no hand vectorisation. Built by
// tools/bench with clang 21: -O3 -march=x86-64-v2 -ffp-contract=off
// -fms-runtime-lib=static -fuse-ld=lld.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#define MAX_ITER 50

int main(int argc, char **argv) {
    if (argc != 2) {
        fprintf(stderr, "usage: mandelbrot <n>\n");
        return 2;
    }
    int64_t n = strtoll(argv[1], NULL, 10);
    double fn = (double)n;
    int64_t total = 0;
    for (int64_t y = 0; y < n; y++) {
        double ci = (2.0 * (double)y) / fn - 1.0;
        for (int64_t x = 0; x < n; x++) {
            double cr = (2.0 * (double)x) / fn - 1.5;
            double zr = 0.0, zi = 0.0;
            int64_t i = 0;
            while (i < MAX_ITER) {
                double tr = (zr * zr - zi * zi) + cr;
                double ti = (2.0 * zr) * zi + ci;
                zr = tr;
                zi = ti;
                if (zr * zr + zi * zi > 4.0) {
                    break;
                }
                i += 1;
            }
            total += i;
        }
    }
    printf("%lld\n", (long long)total);
    return 0;
}
