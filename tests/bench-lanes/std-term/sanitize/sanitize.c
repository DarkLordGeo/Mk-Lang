/* The sanitize row (README.md): the same loop in C. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

static int64_t gen(uint8_t *b, int64_t n) {
    uint64_t st = 1;
    int64_t o = 0;
    for (int64_t i = 0; i < n; i++) {
        st += 0x9e3779b97f4a7c15ULL;
        uint64_t z = mix(st), k = z % 64;
        uint8_t v = (uint8_t)((z >> 8) % 95), w = (uint8_t)((z >> 8) % 32);
        if (k < 48) b[o++] = 0x20 + v;
        else if (k < 52) b[o++] = 0x0a;
        else if (k < 56) { b[o++] = 0xc3; b[o++] = 0xa9; }
        else if (k < 58) b[o++] = 0x1b;
        else if (k < 60) b[o++] = w;
        else if (k < 62) { b[o++] = 0xc2; b[o++] = 0x80 + w; }
        else { b[o++] = 0xe2; b[o++] = 0x82; b[o++] = 0xac; }
    }
    return o;
}

/* sanitize writes s to out with C0 controls but LF and tab, DEL and C1
   controls replaced by U+FFFD, each safe run copied whole; the length. */
static int64_t sanitize(uint8_t *out, const uint8_t *s, int64_t n) {
    int64_t o = 0, run = 0, i = 0;
    while (i < n) {
        uint8_t c = s[i];
        int bad = 0;
        if ((c < 0x20 && c != 0x0a && c != 0x09) || c == 0x7f) bad = 1;
        else if (c == 0xc2 && i + 1 < n && s[i + 1] >= 0x80 && s[i + 1] <= 0x9f) bad = 2;
        if (!bad) { i++; continue; }
        memcpy(out + o, s + run, (size_t)(i - run));
        o += i - run;
        out[o++] = 0xef; out[o++] = 0xbf; out[o++] = 0xbd;
        i += bad;
        run = i;
    }
    memcpy(out + o, s + run, (size_t)(n - run));
    return o + n - run;
}

int main(int argc, char **argv) {
    int64_t n = atoll(argv[1]), rounds = atoll(argv[2]);
    /* Both buffers zeroed, as every column allocates them. */
    uint8_t *b = calloc((size_t)(3 * n), 1), *out = calloc((size_t)(9 * n), 1);
    int64_t len = gen(b, n);
    uint64_t total = 0;
    for (int64_t r = 0; r < rounds; r++) {
        int64_t m = sanitize(out, b, len);
        total += (uint64_t)m;
        total += out[(r * 7919) % m];
    }
    printf("%llu\n", (unsigned long long)total);
    return 0;
}
