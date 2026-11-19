/* libc-search, C: the kernel of mk/kernel.c, linked against the C runtime's
   strlen and memchr. Usage: libc-search <n> <reps>. */
#include <stdio.h>
#include <stdlib.h>
#include "mk/kernel.c"

int main(int argc, char **argv) {
    if (argc < 3) return 2;
    long long n = atoll(argv[1]), reps = atoll(argv[2]);
    printf("%lld\n", k_run(n, reps));
    return 0;
}
