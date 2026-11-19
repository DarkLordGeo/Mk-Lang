/* compress-adler32, C, for tools/bench's reference-library table
 * (tools/bench builds it): zlib 1.3.1's adler32_z (Mk-evidence/vendor/zlib-1.3.1)
 * of a 50 MB log text, rounds times, round r over the text from byte r % 64
 * on (no cell writes to its buffer). The checksum adds the rounds' values. Not built by the
 * idiomatic table: no C std holds Adler-32.
 * Usage: compress-adler32 <file> <rounds>. */
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <zlib.h>

int main(int argc, char **argv) {
    if (argc != 3) return 2;
    FILE *f = fopen(argv[1], "rb");
    if (!f) return 1;
    fseek(f, 0, SEEK_END);
    long n = ftell(f);
    fseek(f, 0, SEEK_SET);
    unsigned char *text = malloc((size_t)n);
    if (!text || fread(text, 1, (size_t)n, f) != (size_t)n) return 1;
    fclose(f);
    long rounds = strtol(argv[2], 0, 10);
    uint64_t total = 0;
    for (long r = 0; r < rounds; r++) {
        total += adler32_z(1, text + r % 64, (z_size_t)(n - r % 64));
    }
    printf("%llu\n", (unsigned long long)total);
    free(text);
    return 0;
}
