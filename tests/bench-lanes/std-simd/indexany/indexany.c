// indexany, C, SIMD: the kernel of README.md with <immintrin.h>, at the
// build's fixed CPU target and no run-time dispatch: Hyperscan's
// Truffle, the exact byte-class test of std.simd.IndexAny for sets of four
// or more bytes. Two 16-byte tables indexed by a byte's low nibble, one for
// bytes below 0x80 (_mm*_shuffle_epi8 zeroes the lane when the top bit is
// set) and one for bytes above (the byte XOR 0x80), each entry a bit per
// high nibble mod 8, ANDed with that bit looked up by the high nibble; one
// vector a step (16 bytes at x86-64-v2, 32 at v3, chosen by __AVX2__), the
// first member by __builtin_ctz of the movemask, then bytes. The tables are
// built once per call from the set. The buffer starts on a 64-byte boundary
// as in every column. Usage: indexany <size> <reps> <seed>.
#include <immintrin.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const uint8_t SET[8] = {'\t', '\n', '\r', ' ', '"', ',', ':', '='};

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

static int64_t index_any(const uint8_t *s, size_t n, const uint8_t *set, size_t k) {
    uint8_t lo[16] = {0}, hi[16] = {0};
    for (size_t j = 0; j < k; j++) {
        uint8_t x = set[j];
        if (x < 0x80) lo[x & 15] |= (uint8_t)(1u << (x >> 4));
        else hi[x & 15] |= (uint8_t)(1u << ((x >> 4) - 8));
    }
    size_t i = 0;
#if defined(__AVX2__)
    const __m256i tlo = _mm256_broadcastsi128_si256(_mm_loadu_si128((const __m128i *)lo));
    const __m256i thi = _mm256_broadcastsi128_si256(_mm_loadu_si128((const __m128i *)hi));
    const __m256i bits = _mm256_setr_epi8(1, 2, 4, 8, 16, 32, 64, -128, 1, 2, 4, 8, 16, 32, 64, -128,
                                          1, 2, 4, 8, 16, 32, 64, -128, 1, 2, 4, 8, 16, 32, 64, -128);
    const __m256i top = _mm256_set1_epi8((char)0x80), low4 = _mm256_set1_epi8(15), z = _mm256_setzero_si256();
    for (; i + 32 <= n; i += 32) {
        __m256i v = _mm256_loadu_si256((const __m256i *)(s + i));
        __m256i row = _mm256_or_si256(_mm256_shuffle_epi8(tlo, v), _mm256_shuffle_epi8(thi, _mm256_xor_si256(v, top)));
        __m256i bit = _mm256_shuffle_epi8(bits, _mm256_and_si256(_mm256_srli_epi16(v, 4), low4));
        unsigned m = ~(unsigned)_mm256_movemask_epi8(_mm256_cmpeq_epi8(_mm256_and_si256(row, bit), z));
        if (m != 0) return (int64_t)(i + (size_t)__builtin_ctz(m));
    }
#else
    const __m128i tlo = _mm_loadu_si128((const __m128i *)lo), thi = _mm_loadu_si128((const __m128i *)hi);
    const __m128i bits = _mm_setr_epi8(1, 2, 4, 8, 16, 32, 64, -128, 1, 2, 4, 8, 16, 32, 64, -128);
    const __m128i top = _mm_set1_epi8((char)0x80), low4 = _mm_set1_epi8(15), z = _mm_setzero_si128();
    for (; i + 16 <= n; i += 16) {
        __m128i v = _mm_loadu_si128((const __m128i *)(s + i));
        __m128i row = _mm_or_si128(_mm_shuffle_epi8(tlo, v), _mm_shuffle_epi8(thi, _mm_xor_si128(v, top)));
        __m128i bit = _mm_shuffle_epi8(bits, _mm_and_si128(_mm_srli_epi16(v, 4), low4));
        unsigned m = 0xFFFFu & ~(unsigned)_mm_movemask_epi8(_mm_cmpeq_epi8(_mm_and_si128(row, bit), z));
        if (m != 0) return (int64_t)(i + (size_t)__builtin_ctz(m));
    }
#endif
    for (; i < n; i++) {
        uint8_t x = s[i];
        uint8_t row = x < 0x80 ? lo[x & 15] : hi[x & 15];
        if ((row >> ((x >> 4) & 7)) & 1) return (int64_t)i;
    }
    return -1;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: indexany <size> <reps> <seed>\n");
        return 2;
    }
    size_t n = (size_t)strtoll(argv[1], 0, 10);
    long long reps = strtoll(argv[2], 0, 10);
    uint64_t st = (uint64_t)strtoll(argv[3], 0, 10);
    /* The buffer starts on a 64-byte boundary in every column. */
    uint8_t *raw = malloc(n + 64);
    if (!raw) return 1;
    uint8_t *buf = raw + ((64 - ((uintptr_t)raw & 63)) & 63);
    if ((uintptr_t)buf % 64 != 0) {
        fprintf(stderr, "indexany: buffer not 64-byte aligned\n");
        return 3;
    }
    for (size_t i = 0; i < n; i++) {
        buf[i] = (uint8_t)(0x40 | (((uint64_t)i * 131 + st) & 0x3F));
    }
    uint64_t span = n < 64 ? n : 64;
    int64_t total = 0;
    for (long long r = 0; r < reps; r++) {
        st += 0x9e3779b97f4a7c15ULL;
        uint64_t m = mix(st);
        size_t pos = n - 1 - (size_t)(m % span);
        uint8_t old = buf[pos];
        buf[pos] = SET[(m >> 32) & 7];
        total += index_any(buf, n, SET, sizeof SET);
        buf[pos] = old;
    }
    printf("%lld\n", (long long)total);
    free(raw);
    return 0;
}
