// log, C: the standard library's snprintf of n lines of two integer holes
// per rep, after the level word, appended to one buffer that is emptied
// before each rep, reps times (README.md). C has no logger; snprintf into a
// buffer is its formatted write without I/O.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main(int argc, char **argv) {
    if (argc < 3) {
        return 2;
    }
    long n = strtol(argv[1], NULL, 10);
    long reps = strtol(argv[2], NULL, 10);
    size_t cap = 1 << 20;
    char *buf = malloc(cap);
    if (!buf) {
        return 1;
    }
    uint64_t total = 0;
    for (long r = 0; r < reps; r++) {
        size_t len = 0;
        for (long k = 0; k < n; k++) {
            if (cap - len < 64) {
                cap *= 2;
                buf = realloc(buf, cap);
                if (!buf) {
                    return 1;
                }
            }
            memcpy(buf + len, "INFO ", 5);
            len += 5;
            len += (size_t)snprintf(buf + len, cap - len, "event %ld took %ld us\n", k, (k * 7) % 1000);
        }
        total += len;
    }
    printf("%llu\n", (unsigned long long)total);
    free(buf);
    return 0;
}
