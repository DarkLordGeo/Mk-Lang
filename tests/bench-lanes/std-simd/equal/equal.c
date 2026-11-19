// equal, C, SIMD: the kernel of README.md with <immintrin.h>, at the build's
// fixed CPU target and no run-time dispatch (B16): four vector XORs ORed
// and tested once a step (_mm*_testz_si*, glibc's __memcmpeq shape; 64
// bytes a step at x86-64-v2, 128 at v3, chosen by __AVX2__), then one
// vector a step, then bytes. This is std.simd.Equal's method. Both buffers
// start on a 64-byte boundary (BS-4). Usage: equal <size> <reps> <seed>.
#include <immintrin.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

static int equal(const uint8_t *a, const uint8_t *b, size_t n) {
    size_t i = 0;
#if defined(__AVX2__)
    for (; i + 128 <= n; i += 128) {
        __m256i x0 = _mm256_xor_si256(_mm256_loadu_si256((const __m256i *)(a + i)), _mm256_loadu_si256((const __m256i *)(b + i)));
        __m256i x1 = _mm256_xor_si256(_mm256_loadu_si256((const __m256i *)(a + i + 32)), _mm256_loadu_si256((const __m256i *)(b + i + 32)));
        __m256i x2 = _mm256_xor_si256(_mm256_loadu_si256((const __m256i *)(a + i + 64)), _mm256_loadu_si256((const __m256i *)(b + i + 64)));
        __m256i x3 = _mm256_xor_si256(_mm256_loadu_si256((const __m256i *)(a + i + 96)), _mm256_loadu_si256((const __m256i *)(b + i + 96)));
        __m256i o = _mm256_or_si256(_mm256_or_si256(x0, x1), _mm256_or_si256(x2, x3));
        if (!_mm256_testz_si256(o, o)) return 0;
    }
    for (; i + 32 <= n; i += 32) {
        __m256i x = _mm256_xor_si256(_mm256_loadu_si256((const __m256i *)(a + i)), _mm256_loadu_si256((const __m256i *)(b + i)));
        if (!_mm256_testz_si256(x, x)) return 0;
    }
#else
    for (; i + 64 <= n; i += 64) {
        __m128i x0 = _mm_xor_si128(_mm_loadu_si128((const __m128i *)(a + i)), _mm_loadu_si128((const __m128i *)(b + i)));
        __m128i x1 = _mm_xor_si128(_mm_loadu_si128((const __m128i *)(a + i + 16)), _mm_loadu_si128((const __m128i *)(b + i + 16)));
        __m128i x2 = _mm_xor_si128(_mm_loadu_si128((const __m128i *)(a + i + 32)), _mm_loadu_si128((const __m128i *)(b + i + 32)));
        __m128i x3 = _mm_xor_si128(_mm_loadu_si128((const __m128i *)(a + i + 48)), _mm_loadu_si128((const __m128i *)(b + i + 48)));
        __m128i o = _mm_or_si128(_mm_or_si128(x0, x1), _mm_or_si128(x2, x3));
        if (!_mm_testz_si128(o, o)) return 0;
    }
    for (; i + 16 <= n; i += 16) {
        __m128i x = _mm_xor_si128(_mm_loadu_si128((const __m128i *)(a + i)), _mm_loadu_si128((const __m128i *)(b + i)));
        if (!_mm_testz_si128(x, x)) return 0;
    }
#endif
    for (; i < n; i++) {
        if (a[i] != b[i]) return 0;
    }
    return 1;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: equal <size> <reps> <seed>\n");
        return 2;
    }
    size_t n = (size_t)strtoll(argv[1], 0, 10);
    long long reps = strtoll(argv[2], 0, 10);
    uint64_t st = (uint64_t)strtoll(argv[3], 0, 10);
    /* BS-4: both buffers start on a 64-byte boundary in every column. */
    uint8_t *ra = malloc(n + 64), *rb = malloc(n + 64);
    if (!ra || !rb) return 1;
    uint8_t *a = ra + ((64 - ((uintptr_t)ra & 63)) & 63);
    uint8_t *b = rb + ((64 - ((uintptr_t)rb & 63)) & 63);
    if ((uintptr_t)a % 64 != 0 || (uintptr_t)b % 64 != 0) {
        fprintf(stderr, "equal: buffer not 64-byte aligned\n");
        return 3;
    }
    for (size_t i = 0; i < n; i++) {
        a[i] = b[i] = (uint8_t)(((uint64_t)i * 0x9e3779b97f4a7c15ULL + st) >> 56);
    }
    uint64_t span = n < 64 ? n : 64;
    uint64_t total = 0;
    for (long long r = 0; r < reps; r++) {
        st += 0x9e3779b97f4a7c15ULL;
        uint64_t m = mix(st);
        size_t pos = n - 1 - (size_t)(m % span);
        uint8_t d = (uint8_t)((m >> 32) & 1);
        b[pos] ^= d;
        total = total * 31 + (uint64_t)equal(a, b, n);
        b[pos] ^= d;
    }
    printf("%llu\n", (unsigned long long)total);
    free(ra);
    free(rb);
    return 0;
}
