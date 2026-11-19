/* until, C: split a file into records through a FILE stream with a 64 KiB
 * buffer (setvbuf), once on '\n' and once on ' ', r times; prints the
 * records and the bytes taken (each record with its delimiter), with POSIX
 * getdelim. The Windows UCRT has no getdelim, so the row has no C cell there
 * (bench.json's skip): a hand-written getc loop would measure a
 * different algorithm from the other cells' library calls. Usage: until
 * <file> <r>. */
#define _POSIX_C_SOURCE 200809L
#ifdef _WIN32
#error "the Windows UCRT has no getdelim: this row has no C cell there"
#endif
#include <stdio.h>
#include <stdlib.h>

static void split(const char *p, int delim, long long *n, long long *bytes) {
    FILE *f = fopen(p, "rb");
    if (!f) {
        perror("open");
        exit(1);
    }
    setvbuf(f, NULL, _IOFBF, 65536);
    char *line = NULL;
    size_t cap = 0;
    ssize_t k;
    while ((k = getdelim(&line, &cap, delim, f)) > 0) {
        *n += 1;
        *bytes += (long long)k;
    }
    free(line);
    fclose(f);
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: until <file> <r>\n");
        return 2;
    }
    int reps = atoi(argv[2]);
    long long n = 0, bytes = 0;
    for (int i = 0; i < reps; i++) {
        split(argv[1], '\n', &n, &bytes);
        split(argv[1], ' ', &n, &bytes);
    }
    printf("%lld %lld\n", n, bytes);
    return 0;
}
