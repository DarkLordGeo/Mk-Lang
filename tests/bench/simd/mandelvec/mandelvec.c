// mandelvec, C, SIMD: the kernel of README.md (same/mandelbrot's algorithm,
// L points at once) with <immintrin.h>, at the build's fixed CPU target and
// no run-time dispatch: SSE2 2-lane f64 vectors at x86-64-v2, AVX
// 4-lane vectors at x86-64-v3 (__AVX2__, set by -march). Each lane stops
// counting at its escape; the loop ends when no lane is active or at 50
// iterations. Products and sums are separate instructions
// (-ffp-contract=off), so every count is the scalar one. Usage:
// mandelvec <n>.
#include <immintrin.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#define MAX_ITER 50

static int64_t point(double cr, double ci) {
    double zr = 0.0, zi = 0.0;
    int64_t i = 0;
    while (i < MAX_ITER) {
        double tr = (zr * zr - zi * zi) + cr;
        double ti = (2.0 * zr) * zi + ci;
        zr = tr;
        zi = ti;
        if (zr * zr + zi * zi > 4.0) break;
        i += 1;
    }
    return i;
}

int main(int argc, char **argv) {
    if (argc != 2) {
        fprintf(stderr, "usage: mandelvec <n>\n");
        return 2;
    }
    int64_t n = strtoll(argv[1], NULL, 10);
    double fn = (double)n;
    int64_t total = 0;
#if defined(__AVX2__)
    enum { L = 4 };
#else
    enum { L = 2 };
#endif
    for (int64_t y = 0; y < n; y++) {
        double ci = (2.0 * (double)y) / fn - 1.0;
        int64_t x = 0;
        for (; x + L <= n; x += L) {
            double crs[L];
            for (int l = 0; l < L; l++) crs[l] = (2.0 * (double)(x + l)) / fn - 1.5;
#if defined(__AVX2__)
            __m256d cr = _mm256_loadu_pd(crs), civ = _mm256_set1_pd(ci);
            __m256d zr = _mm256_setzero_pd(), zi = _mm256_setzero_pd(), cnt = _mm256_setzero_pd();
            __m256d active = _mm256_castsi256_pd(_mm256_set1_epi64x(-1));
            const __m256d two = _mm256_set1_pd(2.0), four = _mm256_set1_pd(4.0), one = _mm256_set1_pd(1.0);
            for (int i = 0; i < MAX_ITER; i++) {
                __m256d tr = _mm256_add_pd(_mm256_sub_pd(_mm256_mul_pd(zr, zr), _mm256_mul_pd(zi, zi)), cr);
                __m256d ti = _mm256_add_pd(_mm256_mul_pd(_mm256_mul_pd(two, zr), zi), civ);
                zr = tr;
                zi = ti;
                __m256d esc = _mm256_cmp_pd(_mm256_add_pd(_mm256_mul_pd(zr, zr), _mm256_mul_pd(zi, zi)), four, _CMP_GT_OQ);
                active = _mm256_andnot_pd(esc, active);
                if (_mm256_movemask_pd(active) == 0) break;
                cnt = _mm256_add_pd(cnt, _mm256_and_pd(active, one));
            }
            double c[L];
            _mm256_storeu_pd(c, cnt);
#else
            __m128d cr = _mm_loadu_pd(crs), civ = _mm_set1_pd(ci);
            __m128d zr = _mm_setzero_pd(), zi = _mm_setzero_pd(), cnt = _mm_setzero_pd();
            __m128d active = _mm_castsi128_pd(_mm_set1_epi64x(-1));
            const __m128d two = _mm_set1_pd(2.0), four = _mm_set1_pd(4.0), one = _mm_set1_pd(1.0);
            for (int i = 0; i < MAX_ITER; i++) {
                __m128d tr = _mm_add_pd(_mm_sub_pd(_mm_mul_pd(zr, zr), _mm_mul_pd(zi, zi)), cr);
                __m128d ti = _mm_add_pd(_mm_mul_pd(_mm_mul_pd(two, zr), zi), civ);
                zr = tr;
                zi = ti;
                __m128d esc = _mm_cmpgt_pd(_mm_add_pd(_mm_mul_pd(zr, zr), _mm_mul_pd(zi, zi)), four);
                active = _mm_andnot_pd(esc, active);
                if (_mm_movemask_pd(active) == 0) break;
                cnt = _mm_add_pd(cnt, _mm_and_pd(active, one));
            }
            double c[L];
            _mm_storeu_pd(c, cnt);
#endif
            for (int l = 0; l < L; l++) total += (int64_t)c[l];
        }
        for (; x < n; x++) total += point((2.0 * (double)x) / fn - 1.5, ci);
    }
    printf("%lld\n", (long long)total);
    return 0;
}
