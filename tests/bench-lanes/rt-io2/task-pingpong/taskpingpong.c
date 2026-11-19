/* task-pingpong, C: two threads pass a value back and forth through two
   one-slot mailboxes, each a word the receiver waits on (WaitOnAddress on
   Windows, the futex on Linux: what a C program without a task runtime
   has); each of n rounds puts i in ping, the partner answers i*3+1 in
   pong; sum = 31 sum + the answer (wrapping). Prints n, sum and 0.
   Usage: taskpingpong <n>. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#ifdef _WIN32
#include <windows.h>
#pragma comment(lib, "synchronization.lib")
#else
#include <pthread.h>
#include <linux/futex.h>
#include <sys/syscall.h>
#include <unistd.h>
#endif

typedef struct { volatile long full; uint64_t v; } box;
static box ping, pong;
static long n;

static void wait_full(box *b, long want) {
    while (__atomic_load_n(&b->full, __ATOMIC_ACQUIRE) != want) {
        long cur = !want;
#ifdef _WIN32
        WaitOnAddress((volatile void *)&b->full, &cur, sizeof cur, INFINITE);
#else
        syscall(SYS_futex, (int *)&b->full, FUTEX_WAIT_PRIVATE, (int)cur, NULL, NULL, 0);
#endif
    }
}

static void set_full(box *b, long v) {
    __atomic_store_n(&b->full, v, __ATOMIC_RELEASE);
#ifdef _WIN32
    WakeByAddressSingle((void *)&b->full);
#else
    syscall(SYS_futex, (int *)&b->full, FUTEX_WAKE_PRIVATE, 1, NULL, NULL, 0);
#endif
}

/* put: wait until the box is empty, fill it; take: wait until it is full,
   empty it. */
static void put(box *b, uint64_t v) { wait_full(b, 0); b->v = v; set_full(b, 1); }
static uint64_t take(box *b) { wait_full(b, 1); uint64_t v = b->v; set_full(b, 0); return v; }

#ifdef _WIN32
static DWORD WINAPI partner(LPVOID p) {
#else
static void *partner(void *p) {
#endif
    (void)p;
    for (long i = 0; i < n; i++) put(&pong, take(&ping) * 3 + 1);
    return 0;
}

int main(int argc, char **argv) {
    if (argc < 2) return 2;
    n = atol(argv[1]);
#ifdef _WIN32
    HANDLE t = CreateThread(NULL, 0, partner, NULL, 0, NULL);
#else
    pthread_t t;
    pthread_create(&t, NULL, partner, NULL);
#endif
    uint64_t sum = 0;
    for (long i = 0; i < n; i++) {
        put(&ping, (uint64_t)i);
        sum = sum * 31 + take(&pong);
    }
#ifdef _WIN32
    WaitForSingleObject(t, INFINITE);
#else
    pthread_join(t, NULL);
#endif
    printf("%ld %llu 0\n", n, (unsigned long long)sum);
    return 0;
}
