// bufwrite, C, idiomatic std: the task of README.md with stdio: fopen("wb")
// in the temporary directory (getenv TMP, then TEMP, else "."), setvbuf with
// a 64 KiB full buffer, fprintf of each line ("%llu %llx %llu\n"), fclose;
// then fopen("rb") and one fread of the whole file, FNV-1a over its bytes,
// and remove. Integers uint64_t (the hex value wraps). Usage: bufwrite <n>.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

int main(int argc, char **argv) {
    if (argc != 2) {
        fprintf(stderr, "usage: bufwrite <n>\n");
        return 2;
    }
    long long n = strtoll(argv[1], NULL, 10);
    const char *tmp = getenv("TMP");
    if (tmp == NULL) tmp = getenv("TEMP");
    if (tmp == NULL) tmp = ".";
    char p[4096];
    snprintf(p, sizeof p, "%s/mkbench-bufwrite-c.txt", tmp);
    FILE *f = fopen(p, "wb");
    if (f == NULL) {
        perror(p);
        return 1;
    }
    if (setvbuf(f, NULL, _IOFBF, 65536) != 0) return 1;
    for (long long i = 0; i < n; i++) {
        uint64_t u = (uint64_t)i;
        uint64_t h = u * 0x9e3779b97f4a7c15ULL;
        if (fprintf(f, "%llu %llx %llu\n", (unsigned long long)u, (unsigned long long)h, (unsigned long long)(u * 7)) < 0) return 1;
    }
    if (fclose(f) != 0) return 1;
    f = fopen(p, "rb");
    if (f == NULL) return 1;
    fseek(f, 0, SEEK_END);
    long size = ftell(f);
    fseek(f, 0, SEEK_SET);
    unsigned char *b = malloc((size_t)size + 1);
    if (b == NULL || fread(b, 1, (size_t)size, f) != (size_t)size) return 1;
    fclose(f);
    uint64_t h = 0xcbf29ce484222325ULL;
    for (long i = 0; i < size; i++) h = (h ^ b[i]) * 0x100000001b3ULL;
    uint64_t total = (uint64_t)size + h;
    free(b);
    remove(p);
    printf("%llu\n", (unsigned long long)total);
    return 0;
}
