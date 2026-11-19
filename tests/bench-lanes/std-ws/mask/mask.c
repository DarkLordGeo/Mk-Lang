// ws-mask, C: RFC 6455 5.3's masking as std.websocket's Mask does it, a
// scalar head to phase 0, 16 bytes a step with SSE2 (_mm_xor_si128 on
// unaligned loads and stores), a scalar tail. n bytes of splitmix64 output
// (seed 1) in a buffer that starts on a 64-byte boundary (BS-4), rounds
// times masked in place with round r's key (the four low
// bytes of (r + 1) * 0x9E3779B97F4A7C15) from phase r % 4; the checksum
// adds, per round, the byte at (r * 7919) % n and the returned phase.
// Unsigned arithmetic wraps. Usage: ws-mask <n> <rounds>.
#include <emmintrin.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int mask(uint8_t *b, size_t n, const uint8_t key[4], int phase) {
    size_t i = 0;
    int p = phase & 3;
    while (i < n && p != 0) {
        b[i] ^= key[p];
        p = (p + 1) & 3;
        i++;
    }
    if (n - i >= 16) {
        uint32_t kw;
        memcpy(&kw, key, 4);
        __m128i k = _mm_set1_epi32((int)kw);
        for (; n - i >= 16; i += 16) {
            __m128i v = _mm_loadu_si128((const __m128i *)(b + i));
            _mm_storeu_si128((__m128i *)(b + i), _mm_xor_si128(v, k));
        }
    }
    for (; i < n; i++) {
        b[i] ^= key[p];
        p = (p + 1) & 3;
    }
    return p;
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: ws-mask <n> <rounds>\n");
        return 2;
    }
    size_t n = (size_t)strtoull(argv[1], 0, 10);
    size_t rounds = (size_t)strtoull(argv[2], 0, 10);
    /* BS-4: the buffer starts on a 64-byte boundary in every column. */
    uint8_t *raw = malloc(n + 64);
    if (!raw) return 1;
    uint8_t *data = raw + ((64 - ((uintptr_t)raw & 63)) & 63);
    if ((uintptr_t)data % 64 != 0) {
        fprintf(stderr, "ws-mask: buffer not 64-byte aligned\n");
        return 3;
    }
    uint64_t st = 1;
    for (size_t i = 0; i < n;) {
        st += 0x9E3779B97F4A7C15ULL;
        uint64_t z = st;
        z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9ULL;
        z = (z ^ (z >> 27)) * 0x94D049BB133111EBULL;
        z ^= z >> 31;
        for (int k = 0; k < 8 && i < n; k++, i++) data[i] = (uint8_t)(z >> (8 * k));
    }
    uint64_t total = 0;
    for (size_t r = 0; r < rounds; r++) {
        uint64_t kw = ((uint64_t)r + 1) * 0x9E3779B97F4A7C15ULL;
        uint8_t key[4] = {(uint8_t)kw, (uint8_t)(kw >> 8), (uint8_t)(kw >> 16), (uint8_t)(kw >> 24)};
        int p = mask(data, n, key, (int)(r % 4));
        total += data[(r * 7919) % n] + (uint64_t)p;
    }
    printf("%llu\n", (unsigned long long)total);
    free(raw);
    return 0;
}
