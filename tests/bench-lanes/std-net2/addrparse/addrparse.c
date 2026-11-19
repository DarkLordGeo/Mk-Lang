/* addrparse, C: inet_ntop and inet_pton round trips of splitmix64
 * addresses (README.md); C has no ip:port text facility, so the brackets,
 * the split at the last colon and the port are hand-written loops.
 * Usage: addrparse <n>. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
#pragma comment(lib, "ws2_32")
#else
#include <arpa/inet.h>
#endif

static uint64_t s = 1;

static uint64_t next(void) {
    s += 0x9E3779B97F4A7C15ull;
    uint64_t z = s;
    z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9ull;
    z = (z ^ (z >> 27)) * 0x94D049BB133111EBull;
    return z ^ (z >> 31);
}

/* fmt writes "a.b.c.d:port" or "[v6]:port" into t; its length. */
static int fmt(char *t, int v6, const unsigned char *ip, unsigned port) {
    int n = 0;
    if (v6) {
        t[n++] = '[';
        inet_ntop(AF_INET6, ip, t + n, 64);
        n += (int)strlen(t + n);
        t[n++] = ']';
    } else {
        inet_ntop(AF_INET, ip, t, 64);
        n = (int)strlen(t);
    }
    t[n++] = ':';
    char d[6];
    int k = 0;
    do {
        d[k++] = (char)('0' + port % 10);
        port /= 10;
    } while (port);
    while (k) t[n++] = d[--k];
    t[n] = 0;
    return n;
}

/* parse reads t back; 0 on success. */
static int parse(char *t, int len, int *v6, unsigned char *ip, unsigned *port) {
    int c = len - 1;
    while (c >= 0 && t[c] != ':') c--;
    if (c <= 0 || c == len - 1) return 1;
    unsigned p = 0;
    for (int i = c + 1; i < len; i++) {
        if (t[i] < '0' || t[i] > '9') return 1;
        p = p * 10 + (unsigned)(t[i] - '0');
        if (p > 65535) return 1;
    }
    *port = p;
    char host[64];
    if (t[0] == '[') {
        if (t[c - 1] != ']' || c - 2 >= 64) return 1;
        memcpy(host, t + 1, (size_t)(c - 2));
        host[c - 2] = 0;
        *v6 = 1;
        return inet_pton(AF_INET6, host, ip) != 1;
    }
    if (c >= 64) return 1;
    memcpy(host, t, (size_t)c);
    host[c] = 0;
    *v6 = 0;
    return inet_pton(AF_INET, host, ip) != 1;
}

int main(int argc, char **argv) {
    long n = strtol(argv[1], NULL, 10);
    uint64_t sum = 0;
    char t[80];
    for (long i = 0; i < n; i++) {
        uint64_t x = next();
        unsigned char ip[16] = {0};
        int v6 = (int)(x & 1);
        if (!v6) {
            ip[0] = (unsigned char)(x >> 8);
            ip[1] = (unsigned char)(x >> 16);
            ip[2] = (unsigned char)(x >> 24);
            ip[3] = (unsigned char)(x >> 32);
        } else {
            uint64_t y = next(), z = next();
            uint64_t zero = (x >> 8) & (x >> 16);
            for (int k = 0; k < 8; k++) {
                uint64_t w = k >= 4 ? z : y;
                uint64_t g = (w >> (16 * (k % 4))) & 0xFFFF;
                if ((zero >> k) & 1) g = 0;
                if (k == 0) g |= 0x2000;
                ip[2 * k] = (unsigned char)(g >> 8);
                ip[2 * k + 1] = (unsigned char)g;
            }
        }
        int len = fmt(t, v6, ip, (unsigned)((x >> 40) & 0xFFFF));
        int pv6 = 0;
        unsigned char pip[16] = {0};
        unsigned port = 0;
        if (parse(t, len, &pv6, pip, &port)) {
            printf("parse failed: %s\n", t);
            return 1;
        }
        sum = sum * 31 + port;
        for (int k = 0; k < (pv6 ? 16 : 4); k++) sum = sum * 31 + pip[k];
    }
    printf("%ld %llu\n", n, (unsigned long long)sum);
    return 0;
}
