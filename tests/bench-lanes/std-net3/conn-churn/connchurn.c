/* conn-churn, C, blocking calls (Winsock on Windows, BSD sockets on Linux):
 * n loopback TCP connections, one after another, of one thread. Each round
 * connects to the listener, accepts the connection with the peer's
 * address from accept itself, writes two bytes (the round's low byte
 * twice) in one send at the accepted end, reads one at the connected end,
 * and closes the connected end, then the accepted one. The connected end
 * closes with a byte unread, so its TCP resets the connection (RFC 1122
 * 4.2.2.13) and no TIME_WAIT entry is left. sum = 31 sum + each byte read
 * (wrapping); a round whose peer is not 127.0.0.1 with a port, or whose
 * call failed, counts as failed. Prints n, sum and the failed rounds.
 * Usage: connchurn <n>. */
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
#define BAD_SOCK INVALID_SOCKET
#define CLOSE_SOCK closesocket
#else
#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>
typedef int sock_t;
#define BAD_SOCK (-1)
#define CLOSE_SOCK close
#endif

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
    listen(l, 128);
    socklen_t al = sizeof a;
    getsockname(l, (struct sockaddr *)&a, &al);
    uint64_t sum = 0;
    long fails = 0;
    for (long i = 0; i < n; i++) {
        sock_t d = socket(AF_INET, SOCK_STREAM, 0);
        if (d == BAD_SOCK || connect(d, (struct sockaddr *)&a, sizeof a) != 0) {
            if (d != BAD_SOCK) CLOSE_SOCK(d);
            fails++;
            continue;
        }
        struct sockaddr_in p;
        socklen_t pl = sizeof p;
        sock_t s = accept(l, (struct sockaddr *)&p, &pl);
        if (s == BAD_SOCK) {
            CLOSE_SOCK(d);
            fails++;
            continue;
        }
        if (p.sin_family != AF_INET || ntohl(p.sin_addr.s_addr) != 0x7F000001 || p.sin_port == 0) fails++;
        char two[2] = {(char)i, (char)i}, buf = 0;
        if (send(s, two, 2, 0) != 2) fails++;
        if (recv(d, &buf, 1, 0) != 1) fails++;
        sum = sum * 31 + (uint8_t)buf;
        CLOSE_SOCK(d);
        CLOSE_SOCK(s);
    }
    printf("%ld %llu %ld\n", n, (unsigned long long)sum, fails);
    return 0;
}
