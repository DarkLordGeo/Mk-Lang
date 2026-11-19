// xoshiro, C: xoshiro256** (Blackman and Vigna, xoshiro256starstar.c),
// seeded with four SplitMix64 outputs (splitmix64.c), and Lemire's nearly
// divisionless bounded draw (Lemire 2019) with unsigned __int128, the same
// steps as every other column of README.md. Unsigned arithmetic wraps.
// Built by tools/bench with clang 21: -O3 -march=x86-64-v2.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static uint64_t s[4];

static uint64_t splitmix(uint64_t *x) {
    uint64_t z = (*x += 0x9E3779B97F4A7C15ULL);
    z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9ULL;
    z = (z ^ (z >> 27)) * 0x94D049BB133111EBULL;
    return z ^ (z >> 31);
}

static inline uint64_t rotl(uint64_t x, int k) { return (x << k) | (x >> (64 - k)); }

static inline uint64_t next(void) {
    const uint64_t result = rotl(s[1] * 5, 7) * 9;
    const uint64_t t = s[1] << 17;
    s[2] ^= s[0];
    s[3] ^= s[1];
    s[1] ^= s[2];
    s[0] ^= s[3];
    s[2] ^= t;
    s[3] = rotl(s[3], 45);
    return result;
}

static inline uint64_t below(uint64_t n) {
    unsigned __int128 m = (unsigned __int128)next() * n;
    uint64_t lo = (uint64_t)m;
    if (lo < n) {
        uint64_t t = (0 - n) % n;
        while (lo < t) {
            m = (unsigned __int128)next() * n;
            lo = (uint64_t)m;
        }
    }
    return (uint64_t)(m >> 64);
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: xoshiro <n> <seed>\n");
        return 2;
    }
    long long n = strtoll(argv[1], NULL, 10);
    uint64_t x = (uint64_t)strtoull(argv[2], NULL, 10);
    for (int i = 0; i < 4; i++) {
        s[i] = splitmix(&x);
    }
    uint64_t total = 0;
    for (long long i = 0; i < n; i++) {
        total += next();
        total += below(1000);
    }
    printf("%llu\n", (unsigned long long)total);
    return 0;
}
