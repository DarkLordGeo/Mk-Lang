/* line-echo, C: a buffered line echo through a hand-written buffered
 * reader and writer (4096-byte buffers at each end; the reader finds a
 * line with memchr), each over a table of two function pointers, as
 * stdio's FILE is over its read and write. Each of n rounds writes 16
 * lines through the client's writer and flushes, reads each line at the
 * server into a line buffer and writes it back through the server's
 * writer, flushes, and reads the 16 lines back at the client. A line is
 * 32 bytes: byte 0 is 32 + k mod 64 for the k-th line sent, then 30 'x'
 * and a newline. sum = 31 sum + byte 0 + 256 * length of each line read
 * back at the client (wrapping). Mode tcp runs over one loopback TCP
 * connection (blocking send and recv, Winsock on Windows, BSD sockets on
 * Linux, TCP_NODELAY at both ends); mode inmem over two in-memory pipes
 * of 4096 bytes, each under its own lock (SRWLOCK on Windows,
 * pthread_mutex_t on Linux), one connection per end that takes a read or
 * a write lock of its own and calls its transport through a function
 * pointer table. The in-memory transport is picked at run time
 * from the mode, as in every cell: the null table for null (every read is
 * the end of the stream), else the pipe end's. Prints n, sum and the
 * failed calls. Usage: lineecho <n> tcp|inmem|null. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#pragma comment(lib, "ws2_32")
typedef int socklen_t;
typedef SOCKET sock_t;
typedef SRWLOCK lock_t;
#define LOCK_INIT(l) InitializeSRWLock(l)
#define LOCK(l) AcquireSRWLockExclusive(l)
#define UNLOCK(l) ReleaseSRWLockExclusive(l)
#else
#include <arpa/inet.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <pthread.h>
#include <sys/socket.h>
#include <unistd.h>
typedef int sock_t;
typedef pthread_mutex_t lock_t;
#define LOCK_INIT(l) pthread_mutex_init(l, NULL)
#define LOCK(l) pthread_mutex_lock(l)
#define UNLOCK(l) pthread_mutex_unlock(l)
#endif

#define LINES 16
#define BUF 4096

/* ops reads at most len bytes (0 at the end, -1 on an error) or writes
 * all of len bytes (0, or -1 on an error). */
struct ops {
    int (*read)(void *, unsigned char *, int);
    int (*write_all)(void *, const unsigned char *, int);
};

/* ---- the transports ---- */

struct sock {
    sock_t s;
};

static int sock_read(void *self, unsigned char *b, int n) {
    int k = recv(((struct sock *)self)->s, (char *)b, n, 0);
    return k < 0 ? -1 : k;
}

static int sock_write_all(void *self, const unsigned char *b, int n) {
    sock_t s = ((struct sock *)self)->s;
    while (n > 0) {
        int k = send(s, (const char *)b, n, 0);
        if (k <= 0) return -1;
        b += k;
        n -= k;
    }
    return 0;
}

static const struct ops sock_ops = {sock_read, sock_write_all};

/* pipe holds the bytes written to one direction and not yet read. */
struct pipe {
    lock_t mu;
    unsigned char data[BUF];
    int start, end;
};

/* end is one end of the in-memory duplex: it reads rx and writes tx. */
struct end {
    struct pipe *rx, *tx;
};

static int end_read(void *self, unsigned char *b, int len) {
    struct pipe *p = ((struct end *)self)->rx;
    LOCK(&p->mu);
    int n = p->end - p->start;
    if (n > len) n = len;
    memcpy(b, p->data + p->start, n);
    p->start += n;
    if (p->start == p->end) p->start = p->end = 0;
    UNLOCK(&p->mu);
    return n;
}

static int end_write_all(void *self, const unsigned char *b, int len) {
    struct pipe *p = ((struct end *)self)->tx;
    LOCK(&p->mu);
    if (len > BUF - p->end) {
        fprintf(stderr, "lineecho: a pipe overflowed\n");
        abort();
    }
    memcpy(p->data + p->end, b, len);
    p->end += len;
    UNLOCK(&p->mu);
    return 0;
}

static const struct ops end_ops = {end_read, end_write_all};

/* null reads the end of the stream and drops what is written. */
static int null_read(void *self, unsigned char *b, int len) {
    (void)self;
    (void)b;
    (void)len;
    return 0;
}

static int null_write_all(void *self, const unsigned char *b, int len) {
    (void)self;
    (void)b;
    (void)len;
    return 0;
}

static const struct ops null_ops = {null_read, null_write_all};

/* conn takes a read or a write lock of its own around each call of its
 * transport. */
struct conn {
    lock_t reads, writes;
    void *t;
    const struct ops *ops;
};

static int conn_read(void *self, unsigned char *b, int len) {
    struct conn *c = self;
    LOCK(&c->reads);
    int n = c->ops->read(c->t, b, len);
    UNLOCK(&c->reads);
    return n;
}

static int conn_write_all(void *self, const unsigned char *b, int len) {
    struct conn *c = self;
    LOCK(&c->writes);
    int r = c->ops->write_all(c->t, b, len);
    UNLOCK(&c->writes);
    return r;
}

static const struct ops conn_ops = {conn_read, conn_write_all};

/* ---- the buffered reader and writer ---- */

struct reader {
    unsigned char buf[BUF];
    int start, end;
    void *src;
    const struct ops *ops;
};

/* until copies bytes up to and including the first newline into dst (at
 * most cap bytes) and returns the count; 0 at the end, -1 on an error. */
static int until(struct reader *r, unsigned char *dst, int cap) {
    int got = 0;
    while (got < cap) {
        if (r->start == r->end) {
            int k = r->ops->read(r->src, r->buf, BUF);
            if (k <= 0) return k < 0 ? -1 : got;
            r->start = 0;
            r->end = k;
        }
        int avail = r->end - r->start;
        if (avail > cap - got) avail = cap - got;
        unsigned char *nl = memchr(r->buf + r->start, '\n', avail);
        int take = nl ? (int)(nl - (r->buf + r->start)) + 1 : avail;
        memcpy(dst + got, r->buf + r->start, take);
        r->start += take;
        got += take;
        if (nl) break;
    }
    return got;
}

struct writer {
    unsigned char buf[BUF];
    int n;
    void *dst;
    const struct ops *ops;
};

static int flush(struct writer *w) {
    if (w->n == 0) return 0;
    int r = w->ops->write_all(w->dst, w->buf, w->n);
    w->n = 0;
    return r;
}

static int put(struct writer *w, const unsigned char *b, int len) {
    if (len > BUF - w->n && flush(w) != 0) return -1;
    if (len > BUF) return w->ops->write_all(w->dst, b, len);
    memcpy(w->buf + w->n, b, len);
    w->n += len;
    return 0;
}

static struct reader cr, sr;
static struct writer cw, sw;

static void echo(long n, void *c, void *s, const struct ops *ops, uint64_t *sum, long *fails) {
    cr.src = cw.dst = c;
    sr.src = sw.dst = s;
    cr.ops = cw.ops = sr.ops = sw.ops = ops;
    unsigned char line[32], got[64];
    memset(line, 'x', sizeof line);
    line[31] = '\n';
    memset(got, 0, sizeof got);
    long k = 0;
    for (long i = 0; i < n; i++) {
        for (int j = 0; j < LINES; j++) {
            line[0] = (unsigned char)(32 + k % 64);
            k++;
            if (put(&cw, line, 32) != 0) (*fails)++;
        }
        if (flush(&cw) != 0) (*fails)++;
        for (int j = 0; j < LINES; j++) {
            int m = until(&sr, got, sizeof got);
            if (m < 0) {
                (*fails)++;
                m = 0;
            }
            if (put(&sw, got, m) != 0) (*fails)++;
        }
        if (flush(&sw) != 0) (*fails)++;
        for (int j = 0; j < LINES; j++) {
            int m = until(&cr, got, sizeof got);
            if (m < 0) m = 0;
            if (m != 32) (*fails)++;
            *sum = *sum * 31 + got[0] + 256 * (uint64_t)m;
        }
    }
}

int main(int argc, char **argv) {
    long n = strtol(argv[1], NULL, 10);
    uint64_t sum = 0;
    long fails = 0;
    int null = argc > 2 && strcmp(argv[2], "null") == 0;
    if (null || (argc > 2 && strcmp(argv[2], "inmem") == 0)) {
        struct pipe *up = calloc(1, sizeof *up), *down = calloc(1, sizeof *down);
        LOCK_INIT(&up->mu);
        LOCK_INIT(&down->mu);
        struct end ce = {down, up}, se = {up, down};
        struct conn *c = calloc(1, sizeof *c), *s = calloc(1, sizeof *s);
        LOCK_INIT(&c->reads);
        LOCK_INIT(&c->writes);
        LOCK_INIT(&s->reads);
        LOCK_INIT(&s->writes);
        c->t = &ce;
        s->t = &se;
        c->ops = s->ops = null ? &null_ops : &end_ops;
        echo(n, c, s, &conn_ops, &sum, &fails);
    } else {
#ifdef _WIN32
        WSADATA wd;
        WSAStartup(0x202, &wd);
#endif
        struct sockaddr_in a;
        memset(&a, 0, sizeof a);
        a.sin_family = AF_INET;
        a.sin_addr.s_addr = htonl(0x7F000001);
        sock_t l = socket(AF_INET, SOCK_STREAM, 0);
        bind(l, (struct sockaddr *)&a, sizeof a);
        listen(l, 16);
        socklen_t al = sizeof a;
        getsockname(l, (struct sockaddr *)&a, &al);
        struct sock d, s;
        d.s = socket(AF_INET, SOCK_STREAM, 0);
        if (connect(d.s, (struct sockaddr *)&a, sizeof a) != 0) {
            printf("no connection\n");
            return 1;
        }
        s.s = accept(l, NULL, NULL);
        int one = 1;
        setsockopt(d.s, IPPROTO_TCP, TCP_NODELAY, (const char *)&one, sizeof one);
        setsockopt(s.s, IPPROTO_TCP, TCP_NODELAY, (const char *)&one, sizeof one);
        echo(n, &d, &s, &sock_ops, &sum, &fails);
    }
    printf("%ld %llu %ld\n", n, (unsigned long long)sum, fails);
    return 0;
}
