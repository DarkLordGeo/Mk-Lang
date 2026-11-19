/* read, C: read a file through a FILE stream with a 64 KiB buffer
 * (setvbuf) by fread calls of want bytes each, r times; prints the bytes
 * read and the wrapping sum of each call's first byte. fread fills its
 * request unless the stream ends, so each call gives want bytes, as the
 * other cells' calls do on a regular file. Usage: read <file> <want> <r>. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static void pass(const char *p, unsigned char *d, size_t want, long long *bytes, uint64_t *sum) {
    FILE *f = fopen(p, "rb");
    if (!f) {
        perror("open");
        exit(1);
    }
    setvbuf(f, NULL, _IOFBF, 65536);
    size_t k;
    while ((k = fread(d, 1, want, f)) > 0) {
        *bytes += (long long)k;
        *sum += d[0];
    }
    if (ferror(f)) {
        perror("read");
        exit(1);
    }
    fclose(f);
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: read <file> <want> <r>\n");
        return 2;
    }
    size_t want = (size_t)strtoull(argv[2], NULL, 10);
    long reps = strtol(argv[3], NULL, 10);
    unsigned char *d = malloc(want);
    if (!d) {
        return 1;
    }
    long long bytes = 0;
    uint64_t sum = 0;
    for (long i = 0; i < reps; i++) {
        pass(argv[1], d, want, &bytes, &sum);
    }
    printf("%lld %llu\n", bytes, (unsigned long long)sum);
    free(d);
    return 0;
}
