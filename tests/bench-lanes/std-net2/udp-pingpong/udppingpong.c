/* udp-pingpong, C, sendto and recvfrom (Winsock on Windows, BSD sockets on
 * Linux): two UDP sockets on 127.0.0.1; each of n rounds sends 32 bytes
 * (byte 0 the round's low byte) from a to b, receives them on b, sends
 * them back and receives them on a. sum = 31 sum + byte 0 of each answer
 * (wrapping). Prints n, sum and the failed calls. Usage: udppingpong <n>. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#ifdef _WIN32
#include <winsock2.h>
#pragma comment(lib, "ws2_32")
typedef int socklen_t;
typedef SOCKET sock_t;
#else
#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>
typedef int sock_t;
#endif

static sock_t bound(struct sockaddr_in *at) {
    sock_t s = socket(AF_INET, SOCK_DGRAM, 0);
    struct sockaddr_in a;
    memset(&a, 0, sizeof a);
    a.sin_family = AF_INET;
    a.sin_addr.s_addr = htonl(0x7F000001);
    bind(s, (struct sockaddr *)&a, sizeof a);
    socklen_t l = sizeof *at;
    getsockname(s, (struct sockaddr *)at, &l);
    return s;
}

int main(int argc, char **argv) {
#ifdef _WIN32
    WSADATA d;
    WSAStartup(0x202, &d);
#endif
    long n = strtol(argv[1], NULL, 10);
    struct sockaddr_in aa, ba, from;
    sock_t a = bound(&aa), b = bound(&ba);
    char msg[32], buf[64];
    memset(msg, 7, sizeof msg);
    uint64_t sum = 0;
    long fails = 0;
    for (long i = 0; i < n; i++) {
        msg[0] = (char)i;
        socklen_t fl = sizeof from;
        int s1 = sendto(a, msg, 32, 0, (struct sockaddr *)&ba, sizeof ba);
        int k1 = recvfrom(b, buf, 64, 0, (struct sockaddr *)&from, &fl);
        int s2 = sendto(b, buf, 32, 0, (struct sockaddr *)&from, fl);
        fl = sizeof from;
        int k2 = recvfrom(a, buf, 64, 0, (struct sockaddr *)&from, &fl);
        if (s1 != 32 || s2 != 32 || k1 != 32 || k2 != 32) fails++;
        sum = sum * 31 + (uint8_t)buf[0];
    }
    printf("%ld %llu %ld\n", n, (unsigned long long)sum, fails);
    return 0;
}
