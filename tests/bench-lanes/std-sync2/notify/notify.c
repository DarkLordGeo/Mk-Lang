/* notify, C, standard C11: the workload of README.md with a <threads.h>
   mtx_t holding whose turn it is and one cnd_t per side, over two OS
   threads (thrd_t; labelled threads). C11's condition variable is the
   baseline Mk's Notify replaces, as Rust's Condvar and Go's
   sync.Cond in the other columns. Each side, n times: lock, wait while it
   is not its turn, give the turn to the other, unlock, cnd_signal the
   other's condition. Prints the total rounds (2 * n).
   Usage: notify <n>. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <threads.h>

static mtx_t mu;
static cnd_t cv[2];
static int turn;
static long n;
static int64_t rounds[2];

static int side(void *p) {
    int me = (int)(intptr_t)p;
    int64_t k = 0;
    for (long i = 0; i < n; i++) {
        mtx_lock(&mu);
        while (turn != me) {
            cnd_wait(&cv[me], &mu);
        }
        turn = 1 - me;
        mtx_unlock(&mu);
        cnd_signal(&cv[1 - me]);
        k++;
    }
    rounds[me] = k;
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 2) {
        fprintf(stderr, "usage: notify <n>\n");
        return 2;
    }
    n = strtol(argv[1], NULL, 10);
    if (mtx_init(&mu, mtx_plain) != thrd_success || cnd_init(&cv[0]) != thrd_success ||
        cnd_init(&cv[1]) != thrd_success) {
        return 1;
    }
    thrd_t th[2];
    for (int i = 0; i < 2; i++) {
        if (thrd_create(&th[i], side, (void *)(intptr_t)i) != thrd_success) {
            return 1;
        }
    }
    for (int i = 0; i < 2; i++) {
        thrd_join(th[i], NULL);
    }
    printf("%lld\n", (long long)(rounds[0] + rounds[1]));
    return 0;
}
