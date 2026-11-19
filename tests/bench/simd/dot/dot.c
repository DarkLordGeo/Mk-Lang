// dot, C, SIMD: the kernel of README.md with <immintrin.h>, at the build's
// fixed CPU target and no run-time dispatch: SSE 4-lane f32 vectors at
// x86-64-v2 (four of them hold the 16 partial sums), AVX 8-lane vectors at
// x86-64-v3 (two of them; __AVX2__ set by -march). Products and sums are
// separate instructions (_mm*_mul_ps, then _mm*_add_ps; -ffp-contract=off)
//, so every column does the same f32 arithmetic in the same order.
// Both buffers start on a 64-byte boundary, as in every column.
// Usage: dot <n> <reps> <seed>.
#include <immintrin.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static float dot(const float *x, const float *y, size_t n) {
    float s[16];
    size_t i = 0;
#if defined(__AVX2__)
    __m256 a0 = _mm256_setzero_ps(), a1 = _mm256_setzero_ps();
    for (; i + 16 <= n; i += 16) {
        a0 = _mm256_add_ps(a0, _mm256_mul_ps(_mm256_loadu_ps(x + i), _mm256_loadu_ps(y + i)));
        a1 = _mm256_add_ps(a1, _mm256_mul_ps(_mm256_loadu_ps(x + i + 8), _mm256_loadu_ps(y + i + 8)));
    }
    _mm256_storeu_ps(s, a0);
    _mm256_storeu_ps(s + 8, a1);
#else
    __m128 a0 = _mm_setzero_ps(), a1 = _mm_setzero_ps(), a2 = _mm_setzero_ps(), a3 = _mm_setzero_ps();
    for (; i + 16 <= n; i += 16) {
        a0 = _mm_add_ps(a0, _mm_mul_ps(_mm_loadu_ps(x + i), _mm_loadu_ps(y + i)));
        a1 = _mm_add_ps(a1, _mm_mul_ps(_mm_loadu_ps(x + i + 4), _mm_loadu_ps(y + i + 4)));
        a2 = _mm_add_ps(a2, _mm_mul_ps(_mm_loadu_ps(x + i + 8), _mm_loadu_ps(y + i + 8)));
        a3 = _mm_add_ps(a3, _mm_mul_ps(_mm_loadu_ps(x + i + 12), _mm_loadu_ps(y + i + 12)));
    }
    _mm_storeu_ps(s, a0);
    _mm_storeu_ps(s + 4, a1);
    _mm_storeu_ps(s + 8, a2);
    _mm_storeu_ps(s + 12, a3);
#endif
    for (size_t w = 8; w >= 1; w /= 2) {
        for (size_t j = 0; j < w; j++) {
            s[j] = s[j] + s[j + w];
        }
    }
    float r = s[0];
    for (; i < n; i++) {
        float p = x[i] * y[i];
        r = r + p;
    }
    return r;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: dot <n> <reps> <seed>\n");
        return 2;
    }
    size_t n = (size_t)strtoll(argv[1], 0, 10);
    long long reps = strtoll(argv[2], 0, 10);
    uint64_t seed = (uint64_t)strtoll(argv[3], 0, 10);
    /* Both buffers start on a 64-byte boundary in every column. */
    char *xraw = malloc(n * sizeof(float) + 64), *yraw = malloc(n * sizeof(float) + 64);
    if (!xraw || !yraw) return 1;
    float *x = (float *)(xraw + ((64 - ((uintptr_t)xraw & 63)) & 63));
    float *y = (float *)(yraw + ((64 - ((uintptr_t)yraw & 63)) & 63));
    if ((uintptr_t)x % 64 != 0 || (uintptr_t)y % 64 != 0) {
        fprintf(stderr, "dot: buffer not 64-byte aligned\n");
        return 3;
    }
    for (size_t i = 0; i < n; i++) {
        x[i] = (float)(((uint64_t)i * 0x9e3779b97f4a7c15ULL + seed) >> 48) * 0x1p-16f - 0.5f;
        y[i] = (float)(((uint64_t)i * 0xbf58476d1ce4e5b9ULL + seed) >> 48) * 0x1p-16f - 0.5f;
    }
    uint64_t total = 0;
    for (long long r = 0; r < reps; r++) {
        x[(size_t)r % n] += 1.0f;
        float d = dot(x, y, n);
        uint32_t bits;
        memcpy(&bits, &d, sizeof bits);
        total += bits;
    }
    printf("%llu\n", (unsigned long long)total);
    free(yraw);
    free(xraw);
    return 0;
}
