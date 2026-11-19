/* spin, C, standard C11: the workload of README.md with a spin lock on a
   C11 atomic_flag (test-and-set: a flag cannot be read without setting it,
   so this is the lock C11's <stdatomic.h> gives), and t OS threads
   (CreateThread on Windows, pthreads on Linux: what a C program without a
   task runtime has; labelled threads). Each thread takes the lock n times
   and adds one to the shared counter. Prints the counter.
   Usage: spin <n> <t>. */
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#ifdef _WIN32
#include <windows.h>
#else
#include <pthread.h>
#endif

static atomic_flag lk = ATOMIC_FLAG_INIT;
static uint64_t counter;
static long n;

static void work(void) {
    for (long i = 0; i < n; i++) {
        while (atomic_flag_test_and_set_explicit(&lk, memory_order_acquire)) {
        }
        counter++;
        atomic_flag_clear_explicit(&lk, memory_order_release);
    }
}

#ifdef _WIN32
static DWORD WINAPI run(LPVOID p) { (void)p; work(); return 0; }
#else
static void *run(void *p) { (void)p; work(); return NULL; }
#endif

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: spin <n> <t>\n");
        return 2;
    }
    n = strtol(argv[1], NULL, 10);
    long t = strtol(argv[2], NULL, 10);
    if (t < 1 || t > 64) return 2;
#ifdef _WIN32
    HANDLE th[64];
    for (long i = 0; i < t; i++) th[i] = CreateThread(NULL, 0, run, NULL, 0, NULL);
    WaitForMultipleObjects((DWORD)t, th, TRUE, INFINITE);
#else
    pthread_t th[64];
    for (long i = 0; i < t; i++) pthread_create(&th[i], NULL, run, NULL);
    for (long i = 0; i < t; i++) pthread_join(th[i], NULL);
#endif
    printf("%llu\n", (unsigned long long)counter);
    return 0;
}
