/* inmem-conn, C: a connection over an in-memory transport, as a protocol
 * layer sees one. The transport keeps one 32-byte message under its own
 * lock (SRWLOCK on Windows, pthread_mutex_t on Linux); the connection
 * holds the transport as a void pointer and a table of function pointers
 * and takes a read or a write lock of its own around each call (one
 * indirect call per operation). Each of n rounds writes 32 bytes (byte 0
 * the round's low byte) and reads them back. sum = 31 sum + byte 0 of
 * each read (wrapping). The transport is picked at run time from the
 * mode, as in every cell: the null table for null (every read is the end
 * of the stream), else the loop's; Mk's direct mode is the loop's here.
 * Prints n, sum and the failed calls. Usage: inmemconn <n> [mode]. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#ifdef _WIN32
#include <windows.h>
typedef SRWLOCK lock_t;
#define LOCK_INIT(l) InitializeSRWLock(l)
#define LOCK(l) AcquireSRWLockExclusive(l)
#define UNLOCK(l) ReleaseSRWLockExclusive(l)
#else
#include <pthread.h>
typedef pthread_mutex_t lock_t;
#define LOCK_INIT(l) pthread_mutex_init(l, NULL)
#define LOCK(l) pthread_mutex_lock(l)
#define UNLOCK(l) pthread_mutex_unlock(l)
#endif

struct loop {
    lock_t mu;
    unsigned char data[32];
    int n;
};

static int loop_read(void *self, unsigned char *buf, int len) {
    struct loop *l = self;
    LOCK(&l->mu);
    int n = l->n < len ? l->n : len;
    memcpy(buf, l->data, n);
    l->n = 0;
    UNLOCK(&l->mu);
    return n;
}

static int loop_write_all(void *self, const unsigned char *data, int len) {
    struct loop *l = self;
    LOCK(&l->mu);
    int n = len < 32 ? len : 32;
    memcpy(l->data, data, n);
    l->n = n;
    UNLOCK(&l->mu);
    return 0;
}

struct ops {
    int (*read)(void *, unsigned char *, int);
    int (*write_all)(void *, const unsigned char *, int);
};

struct conn {
    lock_t reads, writes;
    void *t;
    const struct ops *ops;
};

static int conn_read(struct conn *c, unsigned char *buf, int len) {
    LOCK(&c->reads);
    int n = c->ops->read(c->t, buf, len);
    UNLOCK(&c->reads);
    return n;
}

static int conn_write_all(struct conn *c, const unsigned char *data, int len) {
    LOCK(&c->writes);
    int r = c->ops->write_all(c->t, data, len);
    UNLOCK(&c->writes);
    return r;
}

static const struct ops loop_ops = {loop_read, loop_write_all};

/* null reads the end of the stream and drops what is written. */
static int null_read(void *self, unsigned char *buf, int len) {
    (void)self;
    (void)buf;
    (void)len;
    return 0;
}

static int null_write_all(void *self, const unsigned char *data, int len) {
    (void)self;
    (void)data;
    (void)len;
    return 0;
}

static const struct ops null_ops = {null_read, null_write_all};

int main(int argc, char **argv) {
    long n = strtol(argv[1], NULL, 10);
    struct loop *l = calloc(1, sizeof *l);
    LOCK_INIT(&l->mu);
    struct conn *c = calloc(1, sizeof *c);
    LOCK_INIT(&c->reads);
    LOCK_INIT(&c->writes);
    c->t = l;
    c->ops = argc > 2 && strcmp(argv[2], "null") == 0 ? &null_ops : &loop_ops;
    unsigned char msg[32], buf[32];
    memset(msg, 7, sizeof msg);
    memset(buf, 0, sizeof buf);
    uint64_t sum = 0;
    long fails = 0;
    for (long i = 0; i < n; i++) {
        msg[0] = (unsigned char)i;
        int r = conn_write_all(c, msg, 32);
        int k = conn_read(c, buf, 32);
        if (r != 0 || k != 32) fails++;
        sum = sum * 31 + buf[0];
    }
    printf("%ld %llu %ld\n", n, (unsigned long long)sum, fails);
    return 0;
}
