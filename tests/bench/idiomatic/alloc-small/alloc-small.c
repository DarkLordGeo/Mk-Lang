// alloc-small, C, idiomatic std: the task of README.md with malloc and free
// (the static UCRT's heap, -fms-runtime-lib=static, over the process heap):
// each object is malloc(l) filled by memcpy from the source buffer, kept in
// a ring of 4,096 pointers and lengths; each step reads the oldest, frees
// it, and puts a new one in its slot. At the end every live object is read
// and freed. Usage: alloc-small <steps> <seed>.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define LIVE 4096

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

static unsigned char *make(const unsigned char *src, uint64_t *st, size_t *len) {
    *st += 0x9e3779b97f4a7c15ULL;
    uint64_t v = mix(*st);
    size_t l = (size_t)(v % 249) + 8;
    size_t off = (size_t)((v >> 16) % 3840);
    unsigned char *o = malloc(l);
    if (o == NULL) exit(1);
    memcpy(o, src + off, l);
    *len = l;
    return o;
}

static uint64_t readobj(const unsigned char *o, size_t n) {
    return (uint64_t)n + o[0] + o[n - 1];
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: alloc-small <steps> <seed>\n");
        return 2;
    }
    long long steps = strtoll(argv[1], NULL, 10);
    uint64_t st = (uint64_t)strtoll(argv[2], NULL, 10);
    unsigned char buf[4096];
    uint64_t gs = st ^ 0x5555555555555555ULL;
    for (int i = 0; i < 4096; i++) {
        gs += 0x9e3779b97f4a7c15ULL;
        buf[i] = (unsigned char)(mix(gs) % 26) + 'a';
    }
    static unsigned char *ring[LIVE];
    static size_t lens[LIVE];
    for (int i = 0; i < LIVE; i++) ring[i] = make(buf, &st, &lens[i]);
    uint64_t total = 0;
    for (long long j = 0; j < steps; j++) {
        int slot = (int)(j % LIVE);
        total += readobj(ring[slot], lens[slot]);
        unsigned char *o = make(buf, &st, &lens[slot]);
        free(ring[slot]);
        ring[slot] = o;
    }
    for (int i = 0; i < LIVE; i++) {
        total += readobj(ring[i], lens[i]);
        free(ring[i]);
    }
    printf("%llu\n", (unsigned long long)total);
    return 0;
}
