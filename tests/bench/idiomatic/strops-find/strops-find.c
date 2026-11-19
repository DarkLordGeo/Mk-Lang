// strops-find, C, idiomatic std: the task of README.md with the C standard
// library's substring search, strstr, which needs NUL-terminated strings:
// the file is read once (fopen, fread) into a buffer, and each '\n' of it is
// overwritten with NUL once before the rounds (labelled in bench.json), so
// every line is a C string and strstr(line + p, needle) searches it. The
// offsets are size_t. Usage: strops-find <file> <rounds>.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const char *needles[8] = {"the", "ing", "qzx", "e a", "tion", "s an d", "abcdefgh", "xylophone quartz"};

static uint64_t round_(const char *text, size_t size) {
    uint64_t total = 0;
    size_t lens[8];
    for (int k = 0; k < 8; k++) lens[k] = strlen(needles[k]);
    const char *end = text + size;
    for (const char *line = text; line < end; line += strlen(line) + 1) {
        for (int k = 0; k < 8; k++) {
            const char *s = line;
            const char *q;
            while ((q = strstr(s, needles[k])) != NULL) {
                total += (uint64_t)(q - line) + 1;
                s = q + lens[k];
            }
        }
    }
    return total;
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: strops-find <file> <rounds>\n");
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
    char *b = malloc((size_t)size + 1);
    if (b == NULL || fread(b, 1, (size_t)size, f) != (size_t)size) {
        return 1;
    }
    fclose(f);
    b[size] = 0;
    for (long i = 0; i < size; i++) {
        if (b[i] == '\n') b[i] = 0;
    }
    long long rounds = strtoll(argv[2], NULL, 10);
    uint64_t total = 0;
    for (long long r = 0; r < rounds; r++) total += round_(b, (size_t)size);
    printf("%llu\n", (unsigned long long)total);
    free(b);
    return 0;
}
