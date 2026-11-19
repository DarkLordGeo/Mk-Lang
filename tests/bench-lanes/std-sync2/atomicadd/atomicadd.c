/* atomicadd, C, standard C11: the workload of README.md with an
   <stdatomic.h> atomic_llong over t OS threads (<threads.h> thrd_t;
   labelled threads). Each thread does n atomic_fetch_add (seq_cst, as
   Mk's AtomicInt; on x86-64 every order of an RMW is the same
   lock-prefixed instruction) on one shared counter. Prints the counter
   (t * n). Usage: atomicadd <n> <t>. */
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <threads.h>

static atomic_llong c;
static long n;

static int worker(void *p) {
    (void)p;
    for (long i = 0; i < n; i++) {
        atomic_fetch_add(&c, 1);
    }
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: atomicadd <n> <t>\n");
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
    printf("%lld\n", (long long)atomic_load(&c));
    return 0;
}
