// dispatch, C, same algorithm: ../README.md's loop of n dynamic calls
// through a two-element array of {void *self; uint64_t (*step)(void *,
// uint64_t)} over an Add and a Xor, the target picked by a splitmix64 bit:
// one load of the function pointer, one indirect call. uint64_t, wrapping.
// Usage: dispatch <n> <seed>. Prints the checksum.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

typedef struct { uint64_t acc; } Add;
typedef struct { uint64_t acc; } Xor;
typedef struct { void *self; uint64_t (*step)(void *, uint64_t); } Step;

static uint64_t add_step(void *p, uint64_t x) {
    Add *a = p;
    a->acc += x;
    return a->acc;
}

static uint64_t xor_step(void *p, uint64_t x) {
    Xor *o = p;
    o->acc = (o->acc ^ x) * 0x100000001b3ULL;
    return o->acc;
}

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

static uint64_t run(long long n, uint64_t seed) {
    Add a = {1};
    Xor x = {0xcbf29ce484222325ULL};
    Step objs[2] = {{&a, add_step}, {&x, xor_step}};
    uint64_t st = seed, sum = 0;
    for (long long i = 0; i < n; i++) {
        st += 0x9e3779b97f4a7c15ULL;
        uint64_t z = mix(st);
        Step *s = &objs[z & 1];
        sum += s->step(s->self, z >> 1);
    }
    return sum;
}

int main(int argc, char **argv) {
    if (argc < 3) return 2;
    long long n = strtoll(argv[1], 0, 10);
    uint64_t seed = strtoull(argv[2], 0, 10);
    printf("%llu\n", (unsigned long long)run(n, seed));
    return 0;
}
