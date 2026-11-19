/* cancelpoint, C: n uncontended lock and unlock pairs of a mutex (SRWLOCK on
   Windows, pthread_mutex_t on Linux), each followed by a load of this
   loop's cancel flag (a C program's form of a cancellation point: C has no
   scopes), the guarded value updated as x = x * 31 + i (wrapping). With
   expired 1 another flag, an unrelated one, is set before the loop; this
   loop never reads it. Prints n, x and the failed calls. Usage: cancelpoint
   <n> <expired>. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#ifdef _WIN32
#include <windows.h>
static SRWLOCK mu = SRWLOCK_INIT;
#define LOCK() AcquireSRWLockExclusive(&mu)
#define UNLOCK() ReleaseSRWLockExclusive(&mu)
#else
#include <pthread.h>
static pthread_mutex_t mu = PTHREAD_MUTEX_INITIALIZER;
#define LOCK() pthread_mutex_lock(&mu)
#define UNLOCK() pthread_mutex_unlock(&mu)
#endif

static volatile int ours;
static volatile int other;
static uint64_t x;

int main(int argc, char **argv) {
    if (argc < 3) {
        return 2;
    }
    long long n = atoll(argv[1]);
    int expired = atoi(argv[2]);
    if (expired == 1) {
        __atomic_store_n(&other, 1, __ATOMIC_SEQ_CST);
    }
    long fails = 0;
    for (long long i = 0; i < n; i++) {
        LOCK();
        if (__atomic_load_n(&ours, __ATOMIC_SEQ_CST) != 0) {
            UNLOCK();
            fails++;
            break;
        }
        x = x * 31 + (uint64_t)i;
        UNLOCK();
    }
    printf("%lld %llu %ld\n", n, (unsigned long long)x, fails);
    return 0;
}
