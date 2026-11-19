// zlib-c, C, eco: the task of README.md with zlib 1.3.1 (built by
// tools/bench from Mk-evidence/vendor), deflateInit at level 6, which
// writes the zlib framing (RFC 1950). The input file is read with fread in
// 64 KiB chunks, each chunk is deflated with Z_NO_FLUSH into a 64 KiB
// buffer, the last with Z_FINISH (the final block and the Adler-32
// trailer), and every full buffer is written with fwrite. The program
// prints the count of input bytes.
// Usage: zlibc <in> <out>.
#include <stdio.h>
#include <stdlib.h>
#include "zlib.h"

#define CHUNK 65536

static unsigned char in[CHUNK];
static unsigned char out[CHUNK];

static void die(const char *what) {
    fprintf(stderr, "zlibc: %s\n", what);
    exit(1);
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: zlibc <in> <out>\n");
        return 2;
    }
    FILE *src = fopen(argv[1], "rb");
    if (src == NULL) die("cannot open the input");
    FILE *dst = fopen(argv[2], "wb");
    if (dst == NULL) die("cannot create the output");
    z_stream s = {0};
    if (deflateInit(&s, 6) != Z_OK) die("deflateInit");
    unsigned long long total = 0;
    int flush;
    do {
        size_t n = fread(in, 1, CHUNK, src);
        if (ferror(src)) die("read");
        total += n;
        flush = feof(src) ? Z_FINISH : Z_NO_FLUSH;
        s.next_in = in;
        s.avail_in = (uInt)n;
        do {
            s.next_out = out;
            s.avail_out = CHUNK;
            if (deflate(&s, flush) == Z_STREAM_ERROR) die("deflate");
            size_t have = CHUNK - s.avail_out;
            if (fwrite(out, 1, have, dst) != have) die("write");
        } while (s.avail_out == 0);
    } while (flush != Z_FINISH);
    deflateEnd(&s);
    if (fclose(dst) != 0) die("close the output");
    fclose(src);
    printf("%llu\n", total);
    return 0;
}
