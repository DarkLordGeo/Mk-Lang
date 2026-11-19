// validutf8, C, SIMD: the kernel of README.md with <immintrin.h>, the
// Keiser-Lemire lookup validator (Keiser and Lemire, "Validating UTF-8 In
// Less Than One Instruction Per Byte", 2021; the algorithm of simdjson and
// simdutf): three 16-entry nibble lookups by pshufb, the third and fourth
// byte check by saturating subtraction, 64-byte chunks with an all-ASCII
// skip. At the build's fixed CPU target and no run-time dispatch (B16): SSE
// 16-byte vectors at x86-64-v2 (SSSE3 pshufb, SSE4.1 ptest), AVX2 32-byte
// vectors at x86-64-v3 (__AVX2__, set by -march). The buffer starts on a
// 64-byte boundary, as in every column (BS-4). Usage: validutf8 <size>
// <reps> <seed>.
#include <immintrin.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

// The 16 encodings of README.md; entry k is 1 + k / 4 bytes long.
static const uint8_t ENC[16][4] = {
    {0x61}, {0x20}, {0x30}, {0x7E},
    {0xC2, 0x80}, {0xDF, 0xBF}, {0xC3, 0xA9}, {0xD0, 0x96},
    {0xE0, 0xA0, 0x80}, {0xED, 0x9F, 0xBF}, {0xEE, 0x80, 0x80}, {0xE4, 0xB8, 0x96},
    {0xF0, 0x90, 0x80, 0x80}, {0xF4, 0x8F, 0xBF, 0xBF}, {0xF0, 0x9F, 0x98, 0x80}, {0xF3, 0xA0, 0x80, 0x80},
};

#define TOO_SHORT (1 << 0)
#define TOO_LONG (1 << 1)
#define OVERLONG_3 (1 << 2)
#define TOO_LARGE (1 << 3)
#define SURROGATE (1 << 4)
#define OVERLONG_2 (1 << 5)
#define TOO_LARGE_1000 (1 << 6)
#define OVERLONG_4 (1 << 6)
#define TWO_CONTS (1 << 7)
#define CARRY (TOO_SHORT | TOO_LONG | TWO_CONTS)

// The tables by the high nibble of the previous byte, its low nibble, and
// the high nibble of the byte.
static const uint8_t T1H[16] = {
    TOO_LONG, TOO_LONG, TOO_LONG, TOO_LONG, TOO_LONG, TOO_LONG, TOO_LONG, TOO_LONG,
    TWO_CONTS, TWO_CONTS, TWO_CONTS, TWO_CONTS,
    TOO_SHORT | OVERLONG_2,
    TOO_SHORT,
    TOO_SHORT | OVERLONG_3 | SURROGATE,
    TOO_SHORT | TOO_LARGE | TOO_LARGE_1000 | OVERLONG_4,
};
static const uint8_t T1L[16] = {
    CARRY | OVERLONG_3 | OVERLONG_2 | OVERLONG_4,
    CARRY | OVERLONG_2,
    CARRY,
    CARRY,
    CARRY | TOO_LARGE,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000 | SURROGATE,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
    CARRY | TOO_LARGE | TOO_LARGE_1000,
};
static const uint8_t T2H[16] = {
    TOO_SHORT, TOO_SHORT, TOO_SHORT, TOO_SHORT, TOO_SHORT, TOO_SHORT, TOO_SHORT, TOO_SHORT,
    TOO_LONG | OVERLONG_2 | TWO_CONTS | OVERLONG_3 | TOO_LARGE_1000 | OVERLONG_4,
    TOO_LONG | OVERLONG_2 | TWO_CONTS | OVERLONG_3 | TOO_LARGE,
    TOO_LONG | OVERLONG_2 | TWO_CONTS | SURROGATE | TOO_LARGE,
    TOO_LONG | OVERLONG_2 | TWO_CONTS | SURROGATE | TOO_LARGE,
    TOO_SHORT, TOO_SHORT, TOO_SHORT, TOO_SHORT,
};

#if defined(__AVX2__)
typedef __m256i V;
#define W 32
static V splat(uint8_t b) { return _mm256_set1_epi8((char)b); }
static V load(const uint8_t *p) { return _mm256_loadu_si256((const V *)p); }
static V table(const uint8_t *t) { return _mm256_broadcastsi128_si256(_mm_loadu_si128((const __m128i *)t)); }
static V lookup(V t, V idx) { return _mm256_shuffle_epi8(t, idx); }
static V shr4(V v) { return _mm256_and_si256(_mm256_srli_epi16(v, 4), splat(0x0F)); }
static V vand(V a, V b) { return _mm256_and_si256(a, b); }
static V vor(V a, V b) { return _mm256_or_si256(a, b); }
static V vxor(V a, V b) { return _mm256_xor_si256(a, b); }
static V subs(V a, V b) { return _mm256_subs_epu8(a, b); }
static int any_high(V v) { return _mm256_movemask_epi8(v) != 0; }
static int nonzero(V v) { return !_mm256_testz_si256(v, v); }
// The input shifted by n bytes, the gap filled from the previous vector.
#define PREV(cur, prev, n) _mm256_alignr_epi8(cur, _mm256_permute2x128_si256(prev, cur, 0x21), 16 - (n))
#else
typedef __m128i V;
#define W 16
static V splat(uint8_t b) { return _mm_set1_epi8((char)b); }
static V load(const uint8_t *p) { return _mm_loadu_si128((const V *)p); }
static V table(const uint8_t *t) { return _mm_loadu_si128((const V *)t); }
static V lookup(V t, V idx) { return _mm_shuffle_epi8(t, idx); }
static V shr4(V v) { return _mm_and_si128(_mm_srli_epi16(v, 4), splat(0x0F)); }
static V vand(V a, V b) { return _mm_and_si128(a, b); }
static V vor(V a, V b) { return _mm_or_si128(a, b); }
static V vxor(V a, V b) { return _mm_xor_si128(a, b); }
static V subs(V a, V b) { return _mm_subs_epu8(a, b); }
static int any_high(V v) { return _mm_movemask_epi8(v) != 0; }
static int nonzero(V v) { return !_mm_testz_si128(v, v); }
#define PREV(cur, prev, n) _mm_alignr_epi8(cur, prev, 16 - (n))
#endif

#define NV (64 / W)

struct state {
    V t1h, t1l, t2h, maxv;
    V err, prev, prev_incomplete;
};

// check is the error vector of one input vector against the one before.
static V check(const struct state *s, V in, V prev) {
    V p1 = PREV(in, prev, 1);
    V sc = vand(vand(lookup(s->t1h, shr4(p1)), lookup(s->t1l, vand(p1, splat(0x0F)))), lookup(s->t2h, shr4(in)));
    V p2 = PREV(in, prev, 2);
    V p3 = PREV(in, prev, 3);
    V must23 = vor(subs(p2, splat(0xE0 - 0x80)), subs(p3, splat(0xF0 - 0x80)));
    return vxor(vand(must23, splat(0x80)), sc);
}

static void chunk(struct state *s, const uint8_t *p) {
    V v[NV];
    V all = splat(0);
    for (int j = 0; j < NV; j++) {
        v[j] = load(p + j * W);
        all = vor(all, v[j]);
    }
    if (!any_high(all)) {
        s->err = vor(s->err, s->prev_incomplete);
        return;
    }
    V prev = s->prev;
    for (int j = 0; j < NV; j++) {
        s->err = vor(s->err, check(s, v[j], prev));
        prev = v[j];
    }
    s->prev_incomplete = subs(v[NV - 1], s->maxv);
    s->prev = v[NV - 1];
}

static int valid_utf8(const uint8_t *b, size_t n) {
    uint8_t maxb[W];
    memset(maxb, 0xFF, W);
    maxb[W - 3] = 0xF0 - 1;
    maxb[W - 2] = 0xE0 - 1;
    maxb[W - 1] = 0xC0 - 1;
    struct state s;
    s.t1h = table(T1H);
    s.t1l = table(T1L);
    s.t2h = table(T2H);
    s.maxv = load(maxb);
    s.err = splat(0);
    s.prev = splat(0);
    s.prev_incomplete = splat(0);
    size_t i = 0;
    for (; i + 64 <= n; i += 64) {
        chunk(&s, b + i);
    }
    if (i < n) {
        uint8_t tail[64] = {0};
        memcpy(tail, b + i, n - i);
        chunk(&s, tail);
    }
    s.err = vor(s.err, s.prev_incomplete);
    return !nonzero(s.err);
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: validutf8 <size> <reps> <seed>\n");
        return 2;
    }
    size_t n = (size_t)strtoll(argv[1], 0, 10);
    long long reps = strtoll(argv[2], 0, 10);
    uint64_t st = (uint64_t)strtoll(argv[3], 0, 10);
    size_t blen = n < 65536 ? n : 65536;
    uint8_t *blk = malloc(blen);
    /* BS-4: the buffer starts on a 64-byte boundary in every column. */
    uint8_t *raw = malloc(n + 64);
    if (!blk || !raw) return 1;
    uint8_t *buf = raw + ((64 - ((uintptr_t)raw & 63)) & 63);
    if ((uintptr_t)buf % 64 != 0) {
        fprintf(stderr, "validutf8: buffer not 64-byte aligned\n");
        return 3;
    }
    size_t p = 0;
    for (;;) {
        st += 0x9e3779b97f4a7c15ULL;
        size_t k = (size_t)(mix(st) & 15);
        size_t l = 1 + k / 4;
        if (p + l > blen) break;
        memcpy(blk + p, ENC[k], l);
        p += l;
    }
    for (; p < blen; p++) blk[p] = 0x61;
    size_t i = 0;
    for (; i + blen <= n; i += blen) memcpy(buf + i, blk, blen);
    for (; i < n; i++) buf[i] = 0x61;
    size_t span = n < 64 ? n : 64;
    uint64_t total = 0;
    for (long long r = 0; r < reps; r++) {
        st += 0x9e3779b97f4a7c15ULL;
        uint64_t z = mix(st);
        size_t pos = n - 1 - (size_t)((z >> 1) % span);
        uint8_t old = buf[pos];
        if (z & 1) buf[pos] = (uint8_t)(z >> 56);
        total = total * 31 + (uint64_t)valid_utf8(buf, n);
        buf[pos] = old;
    }
    printf("%llu\n", (unsigned long long)total);
    free(raw);
    free(blk);
    return 0;
}
