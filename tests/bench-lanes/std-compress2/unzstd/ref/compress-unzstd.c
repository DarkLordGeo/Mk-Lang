/* compress-unzstd, C, for tools/bench's reference-library table
 * (tools/bench builds it): libzstd 1.5.7 (Mk-evidence/vendor/zstd-1.5.7, lib/)
 * ZSTD_decompressStream reads a zstd frame (no content checksum) rounds
 * times. Each round opens the file and reads it with fread in 64 KiB
 * pieces, as the Mk and Zig columns read theirs; one ZSTD_DStream is made
 * once and reset per round. The output is taken in 64 KiB chunks; the
 * checksum adds every decoded byte and each round's length. Not built by
 * the idiomatic table: no C std holds zstd. Build: -O3
 * -march=x86-64-v2 -DDYNAMIC_BMI2=0 -I lib -I lib/common with
 * lib/common/*.c lib/decompress/*.c; DYNAMIC_BMI2=0 turns off libzstd's
 * run-time BMI2 dispatch and its BMI2 assembly, so the cell runs at the
 * row's one CPU target.
 * Usage: compress-unzstd <file> <rounds>. */
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <zstd.h>

static unsigned char rbuf[1 << 16], obuf[1 << 16];

int main(int argc, char **argv) {
    if (argc != 3) return 2;
    long rounds = strtol(argv[2], 0, 10);
    ZSTD_DStream *ds = ZSTD_createDStream();
    if (!ds) return 1;
    uint64_t total = 0;
    for (long i = 0; i < rounds; i++) {
        FILE *f = fopen(argv[1], "rb");
        if (!f) return 1;
        ZSTD_DCtx_reset(ds, ZSTD_reset_session_only);
        uint64_t sum = 0, n = 0;
        size_t got, ret = 1;
        while ((got = fread(rbuf, 1, sizeof rbuf, f)) > 0) {
            ZSTD_inBuffer in = {rbuf, got, 0};
            while (in.pos < in.size) {
                ZSTD_outBuffer out = {obuf, sizeof obuf, 0};
                ret = ZSTD_decompressStream(ds, &out, &in);
                if (ZSTD_isError(ret)) return 1;
                for (size_t k = 0; k < out.pos; k++) sum += obuf[k];
                n += out.pos;
            }
        }
        /* The output buffer may have filled as the input ran out: drain
         * until the frame is complete and flushed (ret 0). */
        while (ret != 0) {
            ZSTD_inBuffer in = {rbuf, 0, 0};
            ZSTD_outBuffer out = {obuf, sizeof obuf, 0};
            ret = ZSTD_decompressStream(ds, &out, &in);
            if (ZSTD_isError(ret)) return 1;
            for (size_t k = 0; k < out.pos; k++) sum += obuf[k];
            n += out.pos;
            if (out.pos == 0 && ret != 0) return 1; /* truncated */
        }
        fclose(f);
        total += sum + n;
    }
    ZSTD_freeDStream(ds);
    printf("%llu\n", (unsigned long long)total);
    return 0;
}
