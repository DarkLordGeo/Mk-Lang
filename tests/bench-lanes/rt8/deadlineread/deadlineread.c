/* deadlineread, C: n round trips of one byte over a loopback TCP
   connection; an echo thread reads each byte and writes it back, and the
   main thread writes i % 251, then reads the echo. With deadline 1 the
   socket's receive timeout is set once to one second before the loop
   (setsockopt SO_RCVTIMEO: a DWORD of milliseconds on Windows, a timeval
   on Linux), the form of a one-operation timeout that bounds every later
   recv with no system call per read; it never fires. With 0 the read is
   bare. sum = 31 sum + the byte read (wrapping). Prints n, sum and
   the failed calls. Usage: deadlineread <n> <deadline>. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#pragma comment(lib, "ws2_32.lib")
typedef SOCKET sock;
#define CLOSE closesocket
#else
#include <arpa/inet.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <pthread.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <unistd.h>
typedef int sock;
#define CLOSE close
#endif

static long long n;
static sock lsock;

static int full(sock s, char *b, int want, int recving) {
    int got = 0;
    while (got < want) {
        int k = recving ? recv(s, b + got, want - got, 0) : send(s, b + got, want - got, 0);
        if (k <= 0) {
            return -1;
        }
        got += k;
    }
    return 0;
}

#ifdef _WIN32
static DWORD WINAPI echo(LPVOID arg)
#else
static void *echo(void *arg)
#endif
{
    (void)arg;
    sock s = accept(lsock, NULL, NULL);
    char b = 0;
    for (long long i = 0; i < n; i++) {
        if (full(s, &b, 1, 1) != 0 || full(s, &b, 1, 0) != 0) {
            break;
        }
    }
    CLOSE(s);
    return 0;
}

int main(int argc, char **argv) {
    if (argc < 3) {
        return 2;
    }
    n = atoll(argv[1]);
    int timed = atoi(argv[2]) == 1;
#ifdef _WIN32
    WSADATA wsa;
    WSAStartup(MAKEWORD(2, 2), &wsa);
#endif
    struct sockaddr_in a;
    memset(&a, 0, sizeof a);
    a.sin_family = AF_INET;
    a.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    lsock = socket(AF_INET, SOCK_STREAM, 0);
    if (bind(lsock, (struct sockaddr *)&a, sizeof a) != 0 || listen(lsock, 1) != 0) {
        return 1;
    }
    socklen_t al = sizeof a;
    getsockname(lsock, (struct sockaddr *)&a, &al);
#ifdef _WIN32
    HANDLE th = CreateThread(NULL, 0, echo, NULL, 0, NULL);
#else
    pthread_t th;
    pthread_create(&th, NULL, echo, NULL);
#endif
    sock c = socket(AF_INET, SOCK_STREAM, 0);
    if (connect(c, (struct sockaddr *)&a, sizeof a) != 0) {
        return 1;
    }
    if (timed) {
#ifdef _WIN32
        DWORD ms = 1000;
        if (setsockopt(c, SOL_SOCKET, SO_RCVTIMEO, (const char *)&ms, sizeof ms) != 0) {
            return 1;
        }
#else
        struct timeval tv;
        tv.tv_sec = 1;
        tv.tv_usec = 0;
        if (setsockopt(c, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof tv) != 0) {
            return 1;
        }
#endif
    }
    uint64_t sum = 0;
    long fails = 0;
    for (long long i = 0; i < n; i++) {
        char w = (char)(i % 251);
        if (full(c, &w, 1, 0) != 0) {
            fails++;
            break;
        }
        unsigned char r = 0;
        if (recv(c, (char *)&r, 1, 0) != 1) {
            fails++;
            break;
        }
        sum = sum * 31 + r;
    }
    CLOSE(c);
#ifdef _WIN32
    WaitForSingleObject(th, INFINITE);
#else
    pthread_join(th, NULL);
#endif
    CLOSE(lsock);
    printf("%lld %llu %ld\n", n, (unsigned long long)sum, fails);
    return 0;
}
