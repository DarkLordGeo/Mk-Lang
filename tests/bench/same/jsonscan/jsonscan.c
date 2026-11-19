// jsonscan, C: the reference algorithm of README.md.
// The file is read once into one malloc'd buffer (fopen and fread, binary
// mode), then scanned rounds times. Indices and lengths are int64_t; the
// token hash is uint64_t and wraps, as the overflow class "wraps" says. No
// bounds checks (C has none); the i < n tests are the algorithm's own and
// the same in every column. The if chain is in the README's order; no
// switch. Built by tools/bench with clang 21:
// -O3 -march=x86-64-v2 -ffp-contract=off -fms-runtime-lib=static -fuse-ld=lld.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static uint64_t scan(const unsigned char *b, int64_t n, uint64_t h0) {
    uint64_t h = h0;
    int64_t i = 0;
    while (i < n) {
        unsigned char c = b[i];
        if (c == ' ' || c == '\n' || c == '\r' || c == '\t') {
            i++;
        } else if (c == '"') {
            i++;
            int64_t l = 0;
            while (i < n && b[i] != '"') {
                if (b[i] == '\\') {
                    i++;
                }
                i++;
                l++;
            }
            i++;
            h = h * 31 + (uint64_t)l;
        } else if (c == '-' || (c >= '0' && c <= '9')) {
            uint64_t neg = 0;
            if (c == '-') {
                neg = 1;
                i++;
            }
            uint64_t v = 0;
            while (i < n && ((b[i] >= '0' && b[i] <= '9') || b[i] == '.')) {
                if (b[i] != '.') {
                    v = v * 10 + (uint64_t)(b[i] - '0');
                }
                i++;
            }
            h = h * 31 + (v * 2 + neg);
        } else if (c == 't') {
            h = h * 31 + 1;
            i += 4;
        } else if (c == 'f') {
            h = h * 31 + 2;
            i += 5;
        } else if (c == 'n') {
            h = h * 31 + 3;
            i += 4;
        } else {
            h = h * 31 + c;
            i++;
        }
    }
    return h;
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: jsonscan <file> <rounds>\n");
        return 2;
    }
    FILE *f = fopen(argv[1], "rb");
    if (f == NULL) {
        perror(argv[1]);
        return 1;
    }
    fseek(f, 0, SEEK_END);
    long size = ftell(f);
    fseek(f, 0, SEEK_SET);
    unsigned char *b = malloc((size_t)size + 1);
    if (b == NULL || fread(b, 1, (size_t)size, f) != (size_t)size) {
        return 1;
    }
    fclose(f);
    int64_t rounds = strtoll(argv[2], NULL, 10);
    uint64_t total = 0;
    for (int64_t r = 0; r < rounds; r++) {
        total = scan(b, (int64_t)size, total);
    }
    printf("%llu\n", (unsigned long long)total);
    free(b);
    return 0;
}
