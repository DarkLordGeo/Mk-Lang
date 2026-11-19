/* libc-search's kernel, shared by both columns (README.md): n bytes from
   splitmix64 (seed 1), each 1 to 254; pass r puts a NUL and a 255 near the
   end (at n - 1 - r % 64 and n - 2 - r % 64) and adds strlen and the offset
   memchr finds for 255, so each call scans about n bytes. The Mk column
   compiles this file as its CSource and links std.libc's strlen and memchr;
   the C column includes it and links the C runtime's. */
#include <stdlib.h>
#include <string.h>

static unsigned long long k_mix(unsigned long long *s) {
    unsigned long long z = (*s += 0x9E3779B97F4A7C15ULL);
    z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9ULL;
    z = (z ^ (z >> 27)) * 0x94D049BB133111EBULL;
    return z ^ (z >> 31);
}

long long k_run(long long n, long long reps) {
    unsigned char *b = malloc((size_t)n + 64);
    if (!b) return -1;
    unsigned long long s = 1;
    for (long long i = 0; i < n; i++) b[i] = (unsigned char)(1 + k_mix(&s) % 254);
    b[n] = 0;
    long long sum = 0;
    for (long long r = 0; r < reps; r++) {
        long long z = n - 1 - r % 64, f = n - 2 - r % 64;
        unsigned char kz = b[z], kf = b[f];
        b[z] = 0;
        b[f] = 255;
        sum += (long long)strlen((const char *)b);
        const unsigned char *p = memchr(b, 255, (size_t)n);
        sum += p ? (long long)(p - b) : -1;
        b[z] = kz;
        b[f] = kf;
    }
    free(b);
    return sum;
}
