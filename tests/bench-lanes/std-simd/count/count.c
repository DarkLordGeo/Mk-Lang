// count, C, SIMD: the kernel of README.md with <immintrin.h>, at the build's
// fixed CPU target and no run-time dispatch (B16): each compare's mask (all
// ones in a matching byte) is subtracted from one of four byte counters, a
// step of four vectors (64 bytes at x86-64-v2, 128 at v3, chosen by
// __AVX2__), and every 255 steps the counters are summed with _mm*_sad_epu8;
// then one vector a step (_mm_popcnt_u32 of the movemask), then bytes. This
// is std.simd.Count's method. The buffer starts on a 64-byte boundary
// (BS-4). Usage: count <size> <reps> <seed>.
#include <immintrin.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

static int64_t count(const uint8_t *s, size_t n, uint8_t b) {
    size_t i = 0;
    int64_t total = 0;
#if defined(__AVX2__)
    const __m256i nd = _mm256_set1_epi8((char)b), z = _mm256_setzero_si256();
    while (i + 128 <= n) {
        __m256i c0 = z, c1 = z, c2 = z, c3 = z;
        for (int steps = 0; steps < 255 && i + 128 <= n; steps++, i += 128) {
            c0 = _mm256_sub_epi8(c0, _mm256_cmpeq_epi8(_mm256_loadu_si256((const __m256i *)(s + i)), nd));
            c1 = _mm256_sub_epi8(c1, _mm256_cmpeq_epi8(_mm256_loadu_si256((const __m256i *)(s + i + 32)), nd));
            c2 = _mm256_sub_epi8(c2, _mm256_cmpeq_epi8(_mm256_loadu_si256((const __m256i *)(s + i + 64)), nd));
            c3 = _mm256_sub_epi8(c3, _mm256_cmpeq_epi8(_mm256_loadu_si256((const __m256i *)(s + i + 96)), nd));
        }
        __m256i t = _mm256_add_epi64(_mm256_add_epi64(_mm256_sad_epu8(c0, z), _mm256_sad_epu8(c1, z)),
                                     _mm256_add_epi64(_mm256_sad_epu8(c2, z), _mm256_sad_epu8(c3, z)));
        total += _mm256_extract_epi64(t, 0) + _mm256_extract_epi64(t, 1) + _mm256_extract_epi64(t, 2) +
                 _mm256_extract_epi64(t, 3);
    }
    for (; i + 32 <= n; i += 32) {
        total += _mm_popcnt_u32((unsigned)_mm256_movemask_epi8(
            _mm256_cmpeq_epi8(_mm256_loadu_si256((const __m256i *)(s + i)), nd)));
    }
#else
    const __m128i nd = _mm_set1_epi8((char)b), z = _mm_setzero_si128();
    while (i + 64 <= n) {
        __m128i c0 = z, c1 = z, c2 = z, c3 = z;
        for (int steps = 0; steps < 255 && i + 64 <= n; steps++, i += 64) {
            c0 = _mm_sub_epi8(c0, _mm_cmpeq_epi8(_mm_loadu_si128((const __m128i *)(s + i)), nd));
            c1 = _mm_sub_epi8(c1, _mm_cmpeq_epi8(_mm_loadu_si128((const __m128i *)(s + i + 16)), nd));
            c2 = _mm_sub_epi8(c2, _mm_cmpeq_epi8(_mm_loadu_si128((const __m128i *)(s + i + 32)), nd));
            c3 = _mm_sub_epi8(c3, _mm_cmpeq_epi8(_mm_loadu_si128((const __m128i *)(s + i + 48)), nd));
        }
        __m128i t = _mm_add_epi64(_mm_add_epi64(_mm_sad_epu8(c0, z), _mm_sad_epu8(c1, z)),
                                  _mm_add_epi64(_mm_sad_epu8(c2, z), _mm_sad_epu8(c3, z)));
        total += _mm_cvtsi128_si64(t) + _mm_extract_epi64(t, 1);
    }
    for (; i + 16 <= n; i += 16) {
        total += _mm_popcnt_u32((unsigned)_mm_movemask_epi8(_mm_cmpeq_epi8(_mm_loadu_si128((const __m128i *)(s + i)), nd)));
    }
#endif
    for (; i < n; i++) {
        total += s[i] == b;
    }
    return total;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: count <size> <reps> <seed>\n");
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
        fprintf(stderr, "count: buffer not 64-byte aligned\n");
        return 3;
    }
    for (size_t i = 0; i < n; i++) {
        buf[i] = (uint8_t)(((uint64_t)i * 0x9e3779b97f4a7c15ULL + st) >> 60);
    }
    uint64_t total = 0;
    for (long long r = 0; r < reps; r++) {
        st += 0x9e3779b97f4a7c15ULL;
        total = total * 31 + (uint64_t)count(buf, n, (uint8_t)(mix(st) & 15));
    }
    printf("%llu\n", (unsigned long long)total);
    free(raw);
    return 0;
}
