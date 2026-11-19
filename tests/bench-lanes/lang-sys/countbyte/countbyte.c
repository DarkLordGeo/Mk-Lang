// countbyte, C: the CountByte loop with SSE2 intrinsics, what a C programmer
// writes for it (_mm_set1_epi8, _mm_loadu_si128, _mm_cmpeq_epi8,
// _mm_movemask_epi8 and _mm_popcnt_u32 per 16 bytes, then a scalar tail),
// over the input and passes of mk/countbyte.mk's comment (README.md). The
// SIMD table allows only the vector layer, so the popcount is the
// <immintrin.h> intrinsic, not a __builtin_. The buffer starts on a
// 64-byte boundary, and the program refuses to run, exit 3, if it does not
// (BS-4). Usage: countbyte <n> <reps> <seed>.
#include <immintrin.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static uint64_t mix(uint64_t x) {
    uint64_t z = x;
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ull;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebull;
    return z ^ (z >> 31);
}

static int64_t count_byte(const uint8_t *xs, int64_t len, uint8_t b) {
    int64_t n = 0;
    int64_t i = 0;
    __m128i needle = _mm_set1_epi8((char)b);
    for (; i + 16 <= len; i += 16) {
        __m128i v = _mm_loadu_si128((const __m128i *)(xs + i));
        n += _mm_popcnt_u32((unsigned)_mm_movemask_epi8(_mm_cmpeq_epi8(v, needle)));
    }
    for (; i < len; i++) {
        if (xs[i] == b) {
            n += 1;
        }
    }
    return n;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: countbyte <n> <reps> <seed>\n");
        return 2;
    }
    int64_t n = strtoll(argv[1], NULL, 10);
    int64_t reps = strtoll(argv[2], NULL, 10);
    uint64_t st = strtoull(argv[3], NULL, 10);
    /* BS-4: the buffer starts on a 64-byte boundary in this column. */
    uint8_t *raw = malloc((size_t)n + 64);
    if (raw == NULL) {
        return 1;
    }
    uint8_t *buf = raw + ((64 - ((uintptr_t)raw & 63)) & 63);
    if ((uintptr_t)buf % 64 != 0) {
        fprintf(stderr, "countbyte: buffer not 64-byte aligned\n");
        return 3;
    }
    for (int64_t i = 0; i < n; i++) {
        st += 0x9e3779b97f4a7c15ull;
        buf[i] = (uint8_t)mix(st);
    }
    uint64_t sum = 0;
    for (int64_t r = 0; r < reps; r++) {
        sum += (uint64_t)count_byte(buf, n, (uint8_t)r) * (uint64_t)(r % 1000 + 1);
    }
    printf("%llu\n", (unsigned long long)sum);
    free(raw);
    return 0;
}
