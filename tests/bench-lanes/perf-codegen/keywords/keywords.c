// keywords, C, same algorithm: README.md's keyword classifier, a chain of
// length-and-memcmp tests against 16 literals per word, as a C lexer
// writes it. The keywords are {pointer, length} pairs (Mk's str); the words
// are {offset, length} views of one malloc'd buffer, generated with a byte
// loop as Mk's. n, rounds, the offsets, the lengths and the loop indices are
// int64_t (Mk's int), so i % 1000 is a signed remainder as in Mk; the
// splitmix64 state, the classes and the checksum are uint64_t (overflow
// class "wraps"). No bounds checks (C has none). Built by tools/bench with
// clang 21 -O3 -march=x86-64-v2. Usage: keywords <n> <rounds> <seed>.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

struct str {
    const char *p;
    int64_t n;
};

#define S(lit) {lit, (int64_t)(sizeof(lit) - 1)}

static const struct str keywords[16] = {S("if"), S("else"), S("for"), S("while"), S("return"), S("break"),
    S("continue"), S("fn"), S("let"), S("var"), S("const"), S("match"), S("struct"), S("enum"), S("import"),
    S("export")};

#define KW(s, k) if (n == (int64_t)(sizeof(s) - 1) && memcmp(p, s, sizeof(s) - 1) == 0) return k

// classify is 1 + the index of the keyword p[0..n) equals, or 0.
static uint64_t classify(const unsigned char *p, int64_t n) {
    KW("if", 1); KW("else", 2); KW("for", 3); KW("while", 4);
    KW("return", 5); KW("break", 6); KW("continue", 7); KW("fn", 8);
    KW("let", 9); KW("var", 10); KW("const", 11); KW("match", 12);
    KW("struct", 13); KW("enum", 14); KW("import", 15); KW("export", 16);
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: keywords <n> <rounds> <seed>\n");
        return 2;
    }
    int64_t n = strtoll(argv[1], NULL, 10);
    int64_t rounds = strtoll(argv[2], NULL, 10);
    uint64_t st = strtoull(argv[3], NULL, 10);
    unsigned char *buf = malloc((size_t)n * 8);
    int64_t *offs = malloc((size_t)(n + 1) * sizeof *offs);
    if (buf == NULL || offs == NULL) {
        return 1;
    }
    int64_t p = 0;
    for (int64_t i = 0; i < n; i++) {
        offs[i] = p;
        st += 0x9e3779b97f4a7c15ULL;
        uint64_t z = mix(st);
        uint64_t kind = z % 4;
        if (kind < 2) {
            struct str k = keywords[(int64_t)((z >> 8) % 16)];
            for (int64_t j = 0; j < k.n; j++) {
                buf[p] = (unsigned char)k.p[j];
                p += 1;
            }
            if (kind == 1) {
                buf[p - 1] = (unsigned char)(0x61 + (z >> 16) % 26);
            }
        } else {
            int64_t l = 1 + (int64_t)((z >> 8) % 8);
            for (int64_t j = 0; j < l; j++) {
                st += 0x9e3779b97f4a7c15ULL;
                buf[p] = (unsigned char)(0x61 + mix(st) % 26);
                p += 1;
            }
        }
    }
    offs[n] = p;
    uint64_t total = 0;
    for (int64_t r = 0; r < rounds; r++) {
        for (int64_t i = 0; i < n; i++) {
            total += classify(buf + offs[i], offs[i + 1] - offs[i]) * (uint64_t)(i % 1000 + 1);
        }
    }
    printf("%llu\n", (unsigned long long)total);
    return 0;
}
