// mutex, C, standard C11: the task of README.md with a <threads.h> mtx_t
// (mtx_plain) over t OS threads (thrd_t; labelled threads). Each thread
// does n times: mtx_lock, add 1 to the shared count, mtx_unlock. Prints
// the count (t * n). Usage: mutex <n> <t>.
#include <stdio.h>
#include <stdlib.h>
#include <threads.h>

static mtx_t m;
static long long count;
static long n;

static int worker(void *p) {
    (void)p;
    for (long i = 0; i < n; i++) {
        if (mtx_lock(&m) != thrd_success) return 1;
        count += 1;
        mtx_unlock(&m);
    }
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: mutex <n> <t>\n");
        return 2;
    }
    n = strtol(argv[1], NULL, 10);
    long t = strtol(argv[2], NULL, 10);
    if (t < 1 || t > 64) {
        return 2;
    }
    if (mtx_init(&m, mtx_plain) != thrd_success) return 1;
    thrd_t th[64];
    for (long i = 0; i < t; i++) {
        if (thrd_create(&th[i], worker, NULL) != thrd_success) {
            return 1;
        }
    }
    for (long i = 0; i < t; i++) {
        thrd_join(th[i], NULL);
    }
    mtx_destroy(&m);
    printf("%lld\n", count);
    return 0;
}
