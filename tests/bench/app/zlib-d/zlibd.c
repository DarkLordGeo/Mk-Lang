// zlib-d, C, eco: the task of README.md with zlib 1.3.1 (built by
// tools/bench from Mk-evidence/vendor), inflateInit, which reads the
// zlib framing (RFC 1950) and checks the trailer's Adler-32 (a mismatch is
// Z_DATA_ERROR). The compressed file is read with fread in 64 KiB chunks,
// inflated into a 64 KiB buffer, and every buffer is written with fwrite.
// A stream that ends before Z_STREAM_END is an error. The program prints
// the count of decompressed bytes.
// Usage: zlibd <in> <out>.
#include <stdio.h>
#include <stdlib.h>
#include "zlib.h"

#define CHUNK 65536

static unsigned char in[CHUNK];
static unsigned char out[CHUNK];

static void die(const char *what) {
    fprintf(stderr, "zlibd: %s\n", what);
    exit(1);
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: zlibd <in> <out>\n");
        return 2;
    }
    FILE *src = fopen(argv[1], "rb");
    if (src == NULL) die("cannot open the input");
    FILE *dst = fopen(argv[2], "wb");
    if (dst == NULL) die("cannot create the output");
    z_stream s = {0};
    if (inflateInit(&s) != Z_OK) die("inflateInit");
    unsigned long long total = 0;
    int ret = Z_OK;
    do {
        size_t n = fread(in, 1, CHUNK, src);
        if (ferror(src)) die("read");
        if (n == 0) die("the stream is truncated");
        s.next_in = in;
        s.avail_in = (uInt)n;
        do {
            s.next_out = out;
            s.avail_out = CHUNK;
            ret = inflate(&s, Z_NO_FLUSH);
            if (ret == Z_NEED_DICT || ret == Z_DATA_ERROR || ret == Z_MEM_ERROR || ret == Z_STREAM_ERROR) {
                die(s.msg != NULL ? s.msg : "inflate");
            }
            size_t have = CHUNK - s.avail_out;
            if (fwrite(out, 1, have, dst) != have) die("write");
            total += have;
        } while (s.avail_out == 0 && ret != Z_STREAM_END);
    } while (ret != Z_STREAM_END);
    inflateEnd(&s);
    if (fclose(dst) != 0) die("close the output");
    fclose(src);
    printf("%llu\n", total);
    return 0;
}
