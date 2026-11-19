// indexbyte, C, SIMD: the kernel of README.md with <immintrin.h>, at the
// build's fixed CPU target and no run-time dispatch (B16): SSE2 16-byte
// vectors at x86-64-v2, AVX2 32-byte vectors at x86-64-v3 (__AVX2__, set by
// -march). Four vectors a step, their compares ORed, one movemask test;
// then one vector a step; then bytes. The buffer starts on a 64-byte
// boundary, as in every column (BS-4). Usage: indexbyte <size> <reps>
// <seed>.
#include <immintrin.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

static int64_t index_byte(const uint8_t *s, size_t n, uint8_t b) {
    size_t i = 0;
#if defined(__AVX2__)
    const __m256i nb = _mm256_set1_epi8((char)b);
    for (; i + 128 <= n; i += 128) {
        __m256i e0 = _mm256_cmpeq_epi8(_mm256_loadu_si256((const __m256i *)(s + i)), nb);
        __m256i e1 = _mm256_cmpeq_epi8(_mm256_loadu_si256((const __m256i *)(s + i + 32)), nb);
        __m256i e2 = _mm256_cmpeq_epi8(_mm256_loadu_si256((const __m256i *)(s + i + 64)), nb);
        __m256i e3 = _mm256_cmpeq_epi8(_mm256_loadu_si256((const __m256i *)(s + i + 96)), nb);
        __m256i any = _mm256_or_si256(_mm256_or_si256(e0, e1), _mm256_or_si256(e2, e3));
        if (_mm256_movemask_epi8(any) != 0) {
            break;
        }
    }
    for (; i + 32 <= n; i += 32) {
        unsigned m = (unsigned)_mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_loadu_si256((const __m256i *)(s + i)), nb));
        if (m != 0) {
            return (int64_t)(i + (size_t)__builtin_ctz(m));
        }
    }
#else
    const __m128i nb = _mm_set1_epi8((char)b);
    for (; i + 64 <= n; i += 64) {
        __m128i e0 = _mm_cmpeq_epi8(_mm_loadu_si128((const __m128i *)(s + i)), nb);
        __m128i e1 = _mm_cmpeq_epi8(_mm_loadu_si128((const __m128i *)(s + i + 16)), nb);
        __m128i e2 = _mm_cmpeq_epi8(_mm_loadu_si128((const __m128i *)(s + i + 32)), nb);
        __m128i e3 = _mm_cmpeq_epi8(_mm_loadu_si128((const __m128i *)(s + i + 48)), nb);
        __m128i any = _mm_or_si128(_mm_or_si128(e0, e1), _mm_or_si128(e2, e3));
        if (_mm_movemask_epi8(any) != 0) {
            break;
        }
    }
    for (; i + 16 <= n; i += 16) {
        unsigned m = (unsigned)_mm_movemask_epi8(_mm_cmpeq_epi8(_mm_loadu_si128((const __m128i *)(s + i)), nb));
        if (m != 0) {
            return (int64_t)(i + (size_t)__builtin_ctz(m));
        }
    }
#endif
    for (; i < n; i++) {
        if (s[i] == b) {
            return (int64_t)i;
        }
    }
    return -1;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: indexbyte <size> <reps> <seed>\n");
        return 2;
    }
    size_t n = (size_t)strtoll(argv[1], 0, 10);
    long long reps = strtoll(argv[2], 0, 10);
    uint64_t st = (uint64_t)strtoll(argv[3], 0, 10);
    /* BS-4: the buffer starts on a 64-byte boundary in every column. */
    uint8_t *raw = malloc(n + 64);
    if (!raw) return 1;
    uint8_t *buf = raw + ((64 - ((uintptr_t)raw & 63)) & 63);
    if ((uintptr_t)buf % 64 != 0) {
        fprintf(stderr, "indexbyte: buffer not 64-byte aligned\n");
        return 3;
    }
    for (size_t i = 0; i < n; i++) {
        buf[i] = (uint8_t)((i * 131 + st) & 0x7F);
    }
    int64_t total = 0;
    for (long long r = 0; r < reps; r++) {
        st += 0x9e3779b97f4a7c15ULL;
        size_t pos = n - 1 - (size_t)(mix(st) % (n < 64 ? n : 64));
        uint8_t old = buf[pos];
        buf[pos] = 0xFF;
        total += index_byte(buf, n, 0xFF);
        buf[pos] = old;
    }
    printf("%lld\n", (long long)total);
    free(raw);
    return 0;
}
