/* compress-gzip-stream, C, for tools/bench's reference-library table
 * (tools/bench builds it): zlib 1.3.1's deflate (Mk-evidence/vendor/
 * zlib-1.3.1) in its gzip wrapper (windowBits 31) at Z_DEFAULT_COMPRESSION
 * (level 6) takes a 50 MB log text in 64 KiB pieces with Z_NO_FLUSH and
 * ends with Z_FINISH, rounds times; each round's output goes through a
 * 64 KiB output buffer and fwrite on a FILE with a 64 KiB buffer to the
 * null device (the path argument). One z_stream is made once and
 * deflateReset per round. The checksum adds every input byte and the input
 * length once per round. Not built by the idiomatic table: no C std holds
 * deflate.
 * Usage: compress-gzip-stream <file> <out> <rounds>. */
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <zlib.h>

static unsigned char obuf[1 << 16];

static int pump(z_stream *z, FILE *o, int flush) {
    do {
        z->next_out = obuf;
        z->avail_out = sizeof obuf;
        int ret = deflate(z, flush);
        if (ret == Z_STREAM_ERROR) return -1;
        size_t k = sizeof obuf - z->avail_out;
        if (k > 0 && fwrite(obuf, 1, k, o) != k) return -1;
        if (flush == Z_FINISH && ret == Z_STREAM_END) return 0;
    } while (z->avail_out == 0 || z->avail_in > 0 || flush == Z_FINISH);
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 4) return 2;
    FILE *f = fopen(argv[1], "rb");
    if (!f) return 1;
    fseek(f, 0, SEEK_END);
    long n = ftell(f);
    fseek(f, 0, SEEK_SET);
    unsigned char *text = malloc((size_t)n);
    if (!text || fread(text, 1, (size_t)n, f) != (size_t)n) return 1;
    fclose(f);
    long rounds = strtol(argv[3], 0, 10);
    uint64_t sum = 0;
    for (long i = 0; i < n; i++) sum += text[i];
    sum += (uint64_t)n;
    FILE *o = fopen(argv[2], "wb");
    if (!o) return 1;
    setvbuf(o, 0, _IOFBF, 1 << 16);
    z_stream z;
    memset(&z, 0, sizeof z);
    if (deflateInit2(&z, Z_DEFAULT_COMPRESSION, Z_DEFLATED, 31, 8, Z_DEFAULT_STRATEGY) != Z_OK) return 1;
    uint64_t total = 0;
    for (long r = 0; r < rounds; r++) {
        deflateReset(&z);
        for (long p = 0; p < n; p += 1 << 16) {
            long k = n - p < (1 << 16) ? n - p : (1 << 16);
            z.next_in = text + p;
            z.avail_in = (uInt)k;
            if (pump(&z, o, Z_NO_FLUSH) != 0) return 1;
        }
        if (pump(&z, o, Z_FINISH) != 0) return 1;
        total += sum;
    }
    deflateEnd(&z);
    if (fclose(o) != 0) return 1;
    printf("%llu\n", (unsigned long long)total);
    free(text);
    return 0;
}
