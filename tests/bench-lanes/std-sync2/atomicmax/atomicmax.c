/* atomicmax, C, standard C11: the workload of README.md with two
   <stdatomic.h> atomic_llong over t OS threads (<threads.h> thrd_t;
   labelled threads). Each thread takes a ticket n times with
   atomic_fetch_add_explicit(relaxed) and raises a shared maximum to it with
   an atomic_compare_exchange_weak_explicit(relaxed, relaxed) loop. Prints
   the counter and the maximum (t * n and t * n - 1).
   Usage: atomicmax <n> <t>. */
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <threads.h>

static atomic_llong c;
static atomic_llong m;
static long n;

static int worker(void *p) {
    (void)p;
    for (long i = 0; i < n; i++) {
        long long x = atomic_fetch_add_explicit(&c, 1, memory_order_relaxed);
        long long cur = atomic_load_explicit(&m, memory_order_relaxed);
        while (x > cur) {
            if (atomic_compare_exchange_weak_explicit(&m, &cur, x, memory_order_relaxed, memory_order_relaxed)) {
                break;
            }
        }
    }
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: atomicmax <n> <t>\n");
        return 2;
    }
    n = strtol(argv[1], NULL, 10);
    long t = strtol(argv[2], NULL, 10);
    if (t < 1 || t > 64) {
        return 2;
    }
    thrd_t th[64];
    for (long i = 0; i < t; i++) {
        if (thrd_create(&th[i], worker, NULL) != thrd_success) {
            return 1;
        }
    }
    for (long i = 0; i < t; i++) {
        thrd_join(th[i], NULL);
    }
    printf("%lld %lld\n", (long long)atomic_load(&c), (long long)atomic_load(&m));
    return 0;
}
