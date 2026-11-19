// text-utf16, C: <uchar.h> (C11), mbrtoc16 over the text into a malloc'd
// char16_t buffer (encode), and c16rtomb over one unit buffer back into a
// malloc'd byte buffer (decode), one character a call, as the standard
// gives them; over the input and passes of mk/text-utf16.mk's comment
// (README.md). Usage: text-utf16 <encode|decode> <mib> <reps> <seed>.
//
// The UCRT of the benchmark target (x86_64-pc-windows-msvc) converts UTF-8
// with both functions whatever the locale (Microsoft's documentation of
// mbrtoc16 and c16rtomb). Elsewhere they use the locale's multibyte
// encoding, so the program asks for a UTF-8 locale and stops if there is
// none. Every return value is checked: an invalid sequence or an unpaired
// surrogate stops the program.
#include <locale.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <uchar.h>
#include <wchar.h>

static uint64_t mix(uint64_t x) {
    uint64_t z = x;
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ull;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebull;
    return z ^ (z >> 31);
}

static size_t put8(char *p, uint32_t c) {
    if (c < 0x80) {
        p[0] = (char)c;
        return 1;
    }
    if (c < 0x800) {
        p[0] = (char)(0xC0 | (c >> 6));
        p[1] = (char)(0x80 | (c & 0x3F));
        return 2;
    }
    if (c < 0x10000) {
        p[0] = (char)(0xE0 | (c >> 12));
        p[1] = (char)(0x80 | ((c >> 6) & 0x3F));
        p[2] = (char)(0x80 | (c & 0x3F));
        return 3;
    }
    p[0] = (char)(0xF0 | (c >> 18));
    p[1] = (char)(0x80 | ((c >> 12) & 0x3F));
    p[2] = (char)(0x80 | ((c >> 6) & 0x3F));
    p[3] = (char)(0x80 | (c & 0x3F));
    return 4;
}

// gen is README.md's text: scalars until it holds at least n bytes.
static char *gen(size_t n, uint64_t seed, size_t *len) {
    char *s = malloc(n + 4);
    if (!s) {
        fputs("out of memory\n", stderr);
        exit(1);
    }
    size_t k = 0;
    uint64_t st = seed;
    while (k < n) {
        st += 0x9e3779b97f4a7c15ull;
        uint64_t x = mix(st);
        uint64_t m = x % 16, y = x >> 8;
        uint32_t c;
        if (m < 10) {
            c = (x % 7 == 0 ? 0x41 : 0x61) + (uint32_t)(y % 26);
        } else if (m == 10) {
            c = 0x20;
        } else if (m == 11) {
            c = 0xC0 + (uint32_t)(y % 64);
        } else if (m == 12) {
            c = 0x430 + (uint32_t)(y % 32);
        } else if (m == 13) {
            c = 0x4E00 + (uint32_t)(y % 0x5000);
        } else if (m == 14) {
            c = 0x1F600 + (uint32_t)(y % 80);
        } else {
            c = 0x300 + (uint32_t)(y % 0x70);
        }
        k += put8(s + k, c);
    }
    *len = k;
    return s;
}

static void fail(const char *what) {
    fprintf(stderr, "text-utf16: %s\n", what);
    exit(1);
}

// encode is the UTF-16 of s[0..n] in a fresh buffer (at most n units).
static char16_t *encode(const char *s, size_t n, size_t *units) {
    char16_t *u = malloc(n * sizeof(char16_t) + 1);
    if (!u) {
        fail("out of memory");
    }
    mbstate_t st;
    memset(&st, 0, sizeof st);
    size_t i = 0, k = 0;
    while (i < n) {
        size_t r = mbrtoc16(&u[k], s + i, n - i, &st);
        if (r == (size_t)-3) { // the low surrogate of the last character
            k++;
            continue;
        }
        if (r == (size_t)-1 || r == (size_t)-2) {
            fail("mbrtoc16: invalid or incomplete UTF-8");
        }
        k++;
        i += r == 0 ? 1 : r;
    }
    *units = k;
    return u;
}

// decode is the UTF-8 of u[0..n] in a fresh buffer (at most 3 bytes a unit).
static char *decode(const char16_t *u, size_t n, size_t *bytes) {
    char *b = malloc(n * 3 + 1);
    if (!b) {
        fail("out of memory");
    }
    mbstate_t st;
    memset(&st, 0, sizeof st);
    size_t k = 0;
    for (size_t i = 0; i < n; i++) {
        size_t r = c16rtomb(b + k, u[i], &st);
        if (r == (size_t)-1) {
            fail("c16rtomb: unpaired surrogate");
        }
        k += r;
    }
    if (!mbsinit(&st)) {
        fail("c16rtomb: unpaired surrogate at the end");
    }
    *bytes = k;
    return b;
}

int main(int argc, char **argv) {
    if (argc != 5) {
        fail("usage: text-utf16 <encode|decode> <mib> <reps> <seed>");
    }
#ifndef _WIN32
    if (!setlocale(LC_ALL, "C.UTF-8") && !setlocale(LC_ALL, "en_US.UTF-8")) {
        fail("no UTF-8 locale");
    }
#endif
    const char *mode = argv[1];
    size_t mib = strtoull(argv[2], NULL, 10);
    size_t reps = strtoull(argv[3], NULL, 10);
    uint64_t seed = strtoull(argv[4], NULL, 10);
    size_t n;
    char *s = gen(mib * 1048576, seed, &n);
    uint64_t sum = 0;
    if (strcmp(mode, "encode") == 0) {
        for (size_t r = 0; r < reps; r++) {
            size_t units;
            char16_t *u = encode(s, n, &units);
            sum += (uint64_t)units + (uint64_t)u[r % units];
            free(u);
        }
    } else if (strcmp(mode, "decode") == 0) {
        size_t units;
        char16_t *u = encode(s, n, &units);
        for (size_t r = 0; r < reps; r++) {
            size_t bytes;
            char *b = decode(u, units, &bytes);
            sum += (uint64_t)bytes + (uint64_t)(unsigned char)b[r % bytes];
            free(b);
        }
        free(u);
    } else {
        fail("mode is encode or decode");
    }
    free(s);
    printf("%llu\n", (unsigned long long)sum);
    return 0;
}
