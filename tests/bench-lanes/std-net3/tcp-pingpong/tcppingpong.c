/* tcp-pingpong, C, blocking send and recv (Winsock on Windows, BSD sockets
 * on Linux): one loopback TCP connection of one thread, both ends with
 * TCP_NODELAY; each of n rounds writes 32 bytes (byte 0 the round's low
 * byte) at the dialled end, reads them whole at the accepted end, writes
 * them back and reads them whole at the dialled end. sum = 31 sum + byte 0
 * of each answer (wrapping). Prints n, sum and the failed calls. The mode
 * argument is Mk's and ignored. Usage: tcppingpong <n> [mode]. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
#pragma comment(lib, "ws2_32")
typedef int socklen_t;
typedef SOCKET sock_t;
#else
#include <arpa/inet.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <sys/socket.h>
#include <unistd.h>
typedef int sock_t;
#endif

/* sendall writes every byte of b; 0 on success. */
static int sendall(sock_t s, const char *b, int n) {
    while (n > 0) {
        int k = send(s, b, n, 0);
        if (k <= 0) return -1;
        b += k;
        n -= k;
    }
    return 0;
}

/* full reads exactly n bytes; 0 on success. */
static int full(sock_t s, char *b, int n) {
    while (n > 0) {
        int k = recv(s, b, n, 0);
        if (k <= 0) return -1;
        b += k;
        n -= k;
    }
    return 0;
}

int main(int argc, char **argv) {
#ifdef _WIN32
    WSADATA wd;
    WSAStartup(0x202, &wd);
#endif
    long n = strtol(argv[1], NULL, 10);
    struct sockaddr_in a;
    memset(&a, 0, sizeof a);
    a.sin_family = AF_INET;
    a.sin_addr.s_addr = htonl(0x7F000001);
    sock_t l = socket(AF_INET, SOCK_STREAM, 0);
    bind(l, (struct sockaddr *)&a, sizeof a);
    listen(l, 16);
    socklen_t al = sizeof a;
    getsockname(l, (struct sockaddr *)&a, &al);
    sock_t d = socket(AF_INET, SOCK_STREAM, 0);
    if (connect(d, (struct sockaddr *)&a, sizeof a) != 0) {
        printf("no connection\n");
        return 1;
    }
    sock_t s = accept(l, NULL, NULL);
    int one = 1;
    setsockopt(d, IPPROTO_TCP, TCP_NODELAY, (const char *)&one, sizeof one);
    setsockopt(s, IPPROTO_TCP, TCP_NODELAY, (const char *)&one, sizeof one);
    char msg[32], buf[32];
    memset(msg, 7, sizeof msg);
    uint64_t sum = 0;
    long fails = 0;
    for (long i = 0; i < n; i++) {
        msg[0] = (char)i;
        int bad = sendall(d, msg, 32);
        bad |= full(s, buf, 32);
        bad |= sendall(s, buf, 32);
        bad |= full(d, buf, 32);
        if (bad) fails++;
        sum = sum * 31 + (uint8_t)buf[0];
    }
    printf("%ld %llu %ld\n", n, (unsigned long long)sum, fails);
    return 0;
}
