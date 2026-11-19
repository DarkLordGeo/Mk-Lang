// orderbook, C (C17), hand throughout: the task of README.md, the replay of
// Dogfoods/orderbook (`orderbook replay FEED [--quiet] [--snapshot N]`).
// C's library has no container, so the program writes its own, each named
// here. The feed is read by fgets into a 4 KiB line buffer over a 64 KiB
// setvbuf buffer, split on commas by hand (the feed has no quoted field; a
// line holding a quote, or longer than the buffer, is refused with status
// 3), fields trimmed of ASCII white space, integers parsed by hand (an
// optional sign and decimal digits, as the other twins' parsers accept).
// Orders live in a slot pool (an array of slots, each with a generation,
// and a stack of vacant slot indices used last in first out), named by
// (slot, generation) handles; a uint64_t bitset of live arrival numbers
// sized from the file (a third of its bytes, plus two). A hash map from
// client id (account and serial packed in 64 bits) to handle, and hash
// sets of the ids that are done and of the halted accounts, are one hand
// table: open addressing, linear probing, deletion by backward shift, a
// splitmix64 finaliser as the hash, doubled at three quarters full. Each
// side of the book is a sorted array of price levels (key: price times -1
// for bids, times 1 for asks) in descending key order, the best level
// last, so a new or emptied best level moves no other level; it is
// searched by binary search and kept sorted by memmove, as the Go twin's
// sorted slice. A level's FIFO of handles, the work queue and the 8-fill
// tape are growable ring buffers with a power-of-two capacity. Stops wait in
// two binary min-heaps ordered by (key, arrival number). Trades are an
// array of every fill, an array of the five largest, the tape, and a
// sorted array of volume by price. The checksum is FNV-1a over the bytes of
// 64-bit words in uint64_t; every other integer is int64_t, and no value of
// a feed overflows it. After the replay, the statistics run whether or not
// --quiet is given: a sorted copy (qsort) of the fill prices for
// percentiles, the VWAPs of all fills and of each aggressor side (a copy
// partitioned in place), bars of 1,000 fills (running totals, a short last
// bar merged), the fill-size histogram and the volume by account (the hand
// map, then qsort by volume and account, the order the dogfood's sort and
// stable sort give); the depth of 5 levels a side; the report's values;
// then the close, which counts the live orders left in each side's queues.
// Without --quiet the program prints the dogfood's text report but its
// "engine heap" line, which reads the dogfood's own counting allocator.
// Allocation failure ends the program with status 5.

#include <inttypes.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static void *grow(void *p, size_t *cap, size_t need, size_t size) {
    if (need <= *cap) return p;
    size_t n = *cap ? *cap : 4;
    while (n < need) n *= 2;
    void *q = realloc(p, n * size);
    if (!q) {
        fputs("error: out of memory\n", stderr);
        exit(5);
    }
    *cap = n;
    return q;
}

static void *zalloc(size_t n, size_t size) {
    void *p = calloc(n ? n : 1, size);
    if (!p) {
        fputs("error: out of memory\n", stderr);
        exit(5);
    }
    return p;
}

enum { BUY, SELL };
enum { LIMIT, MARKET, STOP, STOPLIMIT };
static const char *side_name[] = {"Buy", "Sell"};
static const char *kind_name[] = {"Limit", "Market", "Stop", "StopLimit"};

typedef struct {
    uint32_t account, serial;
} ClientId;

static uint64_t id_key(ClientId c) { return (uint64_t)c.account << 32 | c.serial; }

typedef struct {
    ClientId id;
    int64_t seq;
    int side, kind;
    int64_t price, stop, qty, left, ts;
} Order;

typedef struct {
    ClientId taker, maker;
    int64_t taker_seq, maker_seq;
    int side;
    int64_t price, qty, ts;
} Fill;

static bool fill_eq(const Fill *a, const Fill *b) {
    return a->taker.account == b->taker.account && a->taker.serial == b->taker.serial && a->maker.account == b->maker.account &&
           a->maker.serial == b->maker.serial && a->taker_seq == b->taker_seq && a->maker_seq == b->maker_seq && a->side == b->side &&
           a->price == b->price && a->qty == b->qty && a->ts == b->ts;
}

typedef struct {
    uint32_t idx, gen;
} Handle;

static bool handle_eq(Handle a, Handle b) { return a.idx == b.idx && a.gen == b.gen; }

enum { OP_ADD, OP_CANCEL, OP_AMEND, OP_HALT, OP_RESUME, OP_BUST, OP_TRIGGER };

typedef struct {
    int op, side, kind;
    ClientId id;
    uint32_t account;
    int64_t ts, qty, price, stop;
    Handle h;
} Command;

enum { R_DUPLICATE, R_HALTED, R_UNKNOWN, R_ALREADY_DONE, R_NO_LIQUIDITY, R_BAD_QUANTITY, R_NOTHING_TO_BUST, REJECT_KINDS };
static const char *reject_names[REJECT_KINDS] = {"duplicate id", "account halted", "unknown id", "already done",
                                                 "no liquidity", "bad quantity", "nothing to bust"};

// ---- the hash table: uint64_t keys to uint64_t values

typedef struct {
    uint64_t *keys, *vals;
    uint8_t *used;
    size_t cap, len;
} Map;

static uint64_t hash64(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9u;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebu;
    return z ^ (z >> 31);
}

static void map_init(Map *m, size_t cap) {
    m->cap = cap;
    m->len = 0;
    m->keys = zalloc(cap, sizeof(uint64_t));
    m->vals = zalloc(cap, sizeof(uint64_t));
    m->used = zalloc(cap, 1);
}

static size_t map_find(const Map *m, uint64_t k) {
    size_t i = hash64(k) & (m->cap - 1);
    while (m->used[i] && m->keys[i] != k) i = (i + 1) & (m->cap - 1);
    return i;
}

static uint64_t *map_get(const Map *m, uint64_t k) {
    size_t i = map_find(m, k);
    return m->used[i] ? &m->vals[i] : NULL;
}

static void map_put(Map *m, uint64_t k, uint64_t v);

static void map_grow(Map *m) {
    Map old = *m;
    map_init(m, old.cap * 2);
    for (size_t i = 0; i < old.cap; i++)
        if (old.used[i]) map_put(m, old.keys[i], old.vals[i]);
    free(old.keys);
    free(old.vals);
    free(old.used);
}

// map_put sets k to v; the old value, if any, is replaced.
static void map_put(Map *m, uint64_t k, uint64_t v) {
    size_t i = map_find(m, k);
    if (!m->used[i]) {
        if ((m->len + 1) * 4 > m->cap * 3) {
            map_grow(m);
            i = map_find(m, k);
        }
        m->used[i] = 1;
        m->keys[i] = k;
        m->len++;
    }
    m->vals[i] = v;
}

static bool map_remove(Map *m, uint64_t k) {
    size_t i = map_find(m, k);
    if (!m->used[i]) return false;
    m->used[i] = 0;
    m->len--;
    // Backward shift: move later entries of the probe run into the hole.
    size_t j = i;
    for (;;) {
        j = (j + 1) & (m->cap - 1);
        if (!m->used[j]) break;
        size_t home = hash64(m->keys[j]) & (m->cap - 1);
        if (((j - home) & (m->cap - 1)) >= ((j - i) & (m->cap - 1))) {
            m->keys[i] = m->keys[j];
            m->vals[i] = m->vals[j];
            m->used[i] = 1;
            m->used[j] = 0;
            i = j;
        }
    }
    return true;
}

static uint64_t pack_handle(Handle h) { return (uint64_t)h.gen << 32 | h.idx; }
static Handle unpack_handle(uint64_t v) { return (Handle){(uint32_t)v, (uint32_t)(v >> 32)}; }

// ---- a growable ring buffer, as a macro over the element type. Its capacity
// is a power of two (4, then doubling), so an index wraps by a mask, as
// coll.Deque's does, not by a division.

#define RING(Name, T)                                                                   \
    typedef struct {                                                                    \
        T *buf;                                                                         \
        size_t cap, head, len;                                                          \
    } Name;                                                                             \
    static void Name##_reserve(Name *q) {                                               \
        if (q->len < q->cap) return;                                                    \
        size_t n = q->cap ? q->cap * 2 : 4;                                             \
        T *b = malloc(n * sizeof(T));                                                   \
        if (!b) {                                                                       \
            fputs("error: out of memory\n", stderr);                                    \
            exit(5);                                                                    \
        }                                                                               \
        for (size_t i = 0; i < q->len; i++) b[i] = q->buf[(q->head + i) & (q->cap - 1)];      \
        free(q->buf);                                                                   \
        q->buf = b;                                                                     \
        q->cap = n;                                                                     \
        q->head = 0;                                                                    \
    }                                                                                   \
    static void Name##_push(Name *q, T x) {                                             \
        Name##_reserve(q);                                                              \
        q->buf[(q->head + q->len) & (q->cap - 1)] = x;                                        \
        q->len++;                                                                       \
    }                                                                                   \
    static void Name##_push_front(Name *q, T x) {                                       \
        Name##_reserve(q);                                                              \
        q->head = (q->head + q->cap - 1) & (q->cap - 1);                                      \
        q->buf[q->head] = x;                                                            \
        q->len++;                                                                       \
    }                                                                                   \
    static bool Name##_pop_front(Name *q, T *x) {                                       \
        if (!q->len) return false;                                                      \
        *x = q->buf[q->head];                                                           \
        q->head = (q->head + 1) & (q->cap - 1);                                               \
        q->len--;                                                                       \
        return true;                                                                    \
    }                                                                                   \
    static bool Name##_pop(Name *q, T *x) {                                             \
        if (!q->len) return false;                                                      \
        *x = q->buf[(q->head + q->len - 1) & (q->cap - 1)];                                   \
        q->len--;                                                                       \
        return true;                                                                    \
    }                                                                                   \
    static T *Name##_at(const Name *q, size_t i) { return &q->buf[(q->head + i) & (q->cap - 1)]; }

RING(HandleQueue, Handle)
RING(CommandQueue, Command)
RING(FillQueue, Fill)

// ---- the slot pool and the order store

typedef struct {
    uint32_t gen;
    bool used;
    Order o;
} Slot;

typedef struct {
    Slot *slots;
    size_t nslots, cap;
    uint32_t *vacant;
    size_t nvacant, vcap;
    uint64_t *flags;
    int64_t nbits, seq;
    Map index, done;
} Orders;

static void orders_init(Orders *s, int64_t capacity) {
    memset(s, 0, sizeof *s);
    s->nbits = capacity;
    s->flags = zalloc((size_t)(capacity + 63) / 64, sizeof(uint64_t));
    map_init(&s->index, 1024);
    map_init(&s->done, 1024);
}

static Order *pool_get(const Orders *s, Handle h) {
    if (h.idx >= s->nslots) return NULL;
    Slot *sl = &s->slots[h.idx];
    return sl->used && sl->gen == h.gen ? &sl->o : NULL;
}

static Handle pool_add(Orders *s, Order o) {
    if (s->nvacant) {
        uint32_t i = s->vacant[--s->nvacant];
        s->slots[i].used = true;
        s->slots[i].o = o;
        return (Handle){i, s->slots[i].gen};
    }
    s->slots = grow(s->slots, &s->cap, s->nslots + 1, sizeof(Slot));
    s->slots[s->nslots] = (Slot){0, true, o};
    return (Handle){(uint32_t)s->nslots++, 0};
}

static void pool_release(Orders *s, uint32_t i) {
    s->slots[i].used = false;
    s->slots[i].gen++;
    s->vacant = grow(s->vacant, &s->vcap, s->nvacant + 1, sizeof(uint32_t));
    s->vacant[s->nvacant++] = i;
}

static bool has_flag(const Orders *s, int64_t q) { return s->flags[q >> 6] >> (q & 63) & 1; }

static void set_flag(Orders *s, int64_t q) {
    if (q >= s->nbits) {
        fputs("orders past the capacity\n", stderr);
        abort();
    }
    s->flags[q >> 6] |= (uint64_t)1 << (q & 63);
}

static void clear_flag(Orders *s, int64_t q) { s->flags[q >> 6] &= ~((uint64_t)1 << (q & 63)); }

static Order *live(const Orders *s, Handle h) {
    Order *o = pool_get(s, h);
    return o && has_flag(s, o->seq) ? o : NULL;
}

static Handle orders_add(Orders *s, Order o) {
    o.seq = s->seq;
    Handle h = pool_add(s, o);
    map_put(&s->index, id_key(o.id), pack_handle(h));
    set_flag(s, o.seq);
    s->seq++;
    map_remove(&s->done, id_key(o.id));
    return h;
}

static Handle orders_reissue(Orders *s, Order o) {
    o.seq = s->seq;
    Handle h = pool_add(s, o);
    uint64_t *old = map_get(&s->index, id_key(o.id));
    if (old) {
        Order *prev = pool_get(s, unpack_handle(*old));
        if (prev) clear_flag(s, prev->seq);
    }
    map_put(&s->index, id_key(o.id), pack_handle(h));
    set_flag(s, o.seq);
    s->seq++;
    return h;
}

static void orders_retire(Orders *s, Handle h) {
    Order *o = pool_get(s, h);
    if (!o) return;
    clear_flag(s, o->seq);
    map_remove(&s->index, id_key(o->id));
    map_put(&s->done, id_key(o->id), 1);
}

static int64_t live_count(const Orders *s) {
    int64_t c = 0;
    for (int64_t k = 0; k < (s->nbits + 63) / 64; k++) {
        uint64_t w = s->flags[k];
        while (w) {
            w &= w - 1;
            c++;
        }
    }
    if ((size_t)c != s->index.len) {
        fputs("live flags and index disagree\n", stderr);
        abort();
    }
    return c;
}

static void orders_sweep(Orders *s) {
    for (size_t i = 0; i < s->nslots; i++)
        if (s->slots[i].used && !has_flag(s, s->slots[i].o.seq)) pool_release(s, (uint32_t)i);
}

// ---- trades and the checksum

#define TAPE_LENGTH 8
#define TOP_FILLS 5
#define FNV_PRIME 0x100000001b3u

static uint64_t mix(uint64_t h, uint64_t x) {
    for (int i = 0; i < 8; i++) {
        h = (h ^ (x & 0xff)) * FNV_PRIME;
        x >>= 8;
    }
    return h;
}

static uint64_t mix_int(uint64_t h, int64_t x) { return mix(h, (uint64_t)x); }

typedef struct {
    int64_t price, qty;
} PriceQty;

typedef struct {
    Fill *fills;
    size_t nfills, cap;
    Fill top[TOP_FILLS + 1];
    size_t ntop;
    FillQueue tape;
    PriceQty *volume; // sorted by price
    size_t nvolume, vcap;
    bool has_last;
    int64_t last, traded, notional, busted;
    uint64_t checksum;
} Trades;

static size_t volume_find(const Trades *t, int64_t price) {
    size_t lo = 0, hi = t->nvolume;
    while (lo < hi) {
        size_t mid = lo + (hi - lo) / 2;
        if (t->volume[mid].price < price) lo = mid + 1;
        else hi = mid;
    }
    return lo;
}

static void record(Trades *t, Fill f) {
    t->fills = grow(t->fills, &t->cap, t->nfills + 1, sizeof(Fill));
    t->fills[t->nfills++] = f;
    size_t at = 0;
    while (at < t->ntop && t->top[at].qty >= f.qty) at++;
    if (at < TOP_FILLS) {
        memmove(&t->top[at + 1], &t->top[at], (t->ntop - at) * sizeof(Fill));
        t->top[at] = f;
        if (++t->ntop > TOP_FILLS) t->ntop = TOP_FILLS;
    }
    FillQueue_push(&t->tape, f);
    Fill dropped;
    if (t->tape.len > TAPE_LENGTH) FillQueue_pop_front(&t->tape, &dropped);
    size_t i = volume_find(t, f.price);
    if (i < t->nvolume && t->volume[i].price == f.price) {
        t->volume[i].qty += f.qty;
    } else {
        t->volume = grow(t->volume, &t->vcap, t->nvolume + 1, sizeof(PriceQty));
        memmove(&t->volume[i + 1], &t->volume[i], (t->nvolume - i) * sizeof(PriceQty));
        t->volume[i] = (PriceQty){f.price, f.qty};
        t->nvolume++;
    }
    t->has_last = true;
    t->last = f.price;
    t->traded += f.qty;
    t->notional += f.qty * f.price;
    t->checksum = mix_int(mix_int(mix_int(mix(mix(t->checksum, (uint64_t)f.taker_seq), (uint64_t)f.maker_seq), f.price), f.qty), f.ts);
}

static bool bust(Trades *t) {
    if (!t->nfills) return false;
    Fill f = t->fills[--t->nfills], x;
    FillQueue_pop(&t->tape, &x);
    for (size_t i = 0; i < t->ntop; i++) {
        if (fill_eq(&t->top[i], &f)) {
            memmove(&t->top[i], &t->top[i + 1], (t->ntop - i - 1) * sizeof(Fill));
            t->ntop--;
            break;
        }
    }
    size_t i = volume_find(t, f.price);
    if (i < t->nvolume && t->volume[i].price == f.price) {
        t->volume[i].qty -= f.qty;
        if (t->volume[i].qty == 0) {
            memmove(&t->volume[i], &t->volume[i + 1], (t->nvolume - i - 1) * sizeof(PriceQty));
            t->nvolume--;
        }
    }
    t->traded -= f.qty;
    t->notional -= f.qty * f.price;
    t->busted++;
    t->checksum = mix(t->checksum, 0xb057);
    return true;
}

// ---- one side of the book

typedef struct {
    int64_t key, qty, live;
    HandleQueue orders;
} Level;

typedef struct {
    Level *levels; // descending key: best price last, so a new or emptied best level moves nothing
    size_t n, cap;
    int64_t sign;
} Ladder;

typedef struct {
    int64_t price, qty, orders;
} Depth;

static size_t ladder_find(const Ladder *l, int64_t key, bool *found) {
    size_t lo = 0, hi = l->n;
    while (lo < hi) {
        size_t mid = lo + (hi - lo) / 2;
        if (l->levels[mid].key > key) lo = mid + 1;
        else hi = mid;
    }
    *found = lo < l->n && l->levels[lo].key == key;
    return lo;
}

static void ladder_drop(Ladder *l, size_t i) {
    free(l->levels[i].orders.buf);
    memmove(&l->levels[i], &l->levels[i + 1], (l->n - i - 1) * sizeof(Level));
    l->n--;
}

static bool best(const Ladder *l, int64_t *p) {
    if (!l->n) return false;
    *p = l->levels[l->n - 1].key * l->sign;
    return true;
}

static void rest(Ladder *l, int64_t price, int64_t qty, Handle h) {
    bool found;
    size_t i = ladder_find(l, price * l->sign, &found);
    if (!found) {
        l->levels = grow(l->levels, &l->cap, l->n + 1, sizeof(Level));
        memmove(&l->levels[i + 1], &l->levels[i], (l->n - i) * sizeof(Level));
        l->levels[i] = (Level){.key = price * l->sign};
        l->n++;
    }
    Level *lvl = &l->levels[i];
    HandleQueue_push(&lvl->orders, h);
    lvl->qty += qty;
    lvl->live++;
}

static void withdraw(Ladder *l, int64_t price, int64_t qty, Handle h) {
    bool found;
    size_t i = ladder_find(l, price * l->sign, &found);
    if (!found) return;
    Level *lvl = &l->levels[i];
    lvl->qty -= qty;
    lvl->live--;
    if (lvl->orders.len && handle_eq(*HandleQueue_at(&lvl->orders, lvl->orders.len - 1), h)) {
        Handle x;
        HandleQueue_pop(&lvl->orders, &x);
    }
    if (lvl->qty == 0) ladder_drop(l, i);
}

#define COMPACT_SLACK 8

static void compact(Ladder *l, int64_t price, const Orders *book) {
    bool found;
    size_t i = ladder_find(l, price * l->sign, &found);
    if (!found) return;
    Level *old = &l->levels[i];
    if ((int64_t)old->orders.len <= 2 * old->live + COMPACT_SLACK) return;
    HandleQueue fresh = {0};
    for (size_t j = 0; j < old->orders.len; j++) {
        Handle h = *HandleQueue_at(&old->orders, j);
        if (live(book, h)) HandleQueue_push(&fresh, h);
    }
    free(old->orders.buf);
    old->orders = fresh;
}

typedef struct {
    int64_t orders, qty;
} Expired;

static Expired ladder_close(Ladder *l, const Orders *book) {
    Expired e = {0, 0};
    for (size_t i = 0; i < l->n; i++) {
        Level *lvl = &l->levels[i];
        e.qty += lvl->qty;
        for (size_t j = 0; j < lvl->orders.len; j++)
            if (live(book, *HandleQueue_at(&lvl->orders, j))) e.orders++;
        free(lvl->orders.buf);
    }
    l->n = 0;
    return e;
}

static void sweep_level(Ladder *l, int64_t key, Orders *book, Handle taker, int64_t now, Trades *out) {
    int64_t price = key * l->sign;
    bool found;
    size_t i = ladder_find(l, key, &found);
    if (!found) return;
    Level *lvl = &l->levels[i];
    while (lvl->qty > 0) {
        Handle maker;
        if (!HandleQueue_pop_front(&lvl->orders, &maker)) break;
        if (!live(book, maker) || handle_eq(maker, taker)) continue;
        Order *t = pool_get(book, taker), *m = pool_get(book, maker);
        int64_t q = t->left < m->left ? t->left : m->left;
        t->left -= q;
        m->left -= q;
        lvl->qty -= q;
        record(out, (Fill){t->id, m->id, t->seq, m->seq, t->side, price, q, now});
        if (m->left > 0) {
            HandleQueue_push_front(&lvl->orders, maker);
        } else {
            lvl->live--;
            orders_retire(book, maker);
        }
        if (t->left == 0) break;
    }
    if (lvl->qty == 0) ladder_drop(l, i);
}

static void match(Ladder *l, Orders *book, Handle taker, int64_t now, Trades *out) {
    for (;;) {
        Order *t = live(book, taker);
        if (!t || t->left == 0) return;
        bool limited = t->kind == LIMIT || t->kind == STOPLIMIT;
        int64_t limit = t->price;
        if (!l->n) return;
        int64_t key = l->levels[l->n - 1].key, p = key * l->sign;
        if (limited && !(l->sign > 0 ? p <= limit : p >= limit)) return;
        sweep_level(l, key, book, taker, now, out);
    }
}

static size_t top_levels(const Ladder *l, Depth *out, size_t n) {
    size_t k = 0;
    for (; k < n && k < l->n; k++) {
        const Level *lvl = &l->levels[l->n - 1 - k];
        out[k] = (Depth){lvl->key * l->sign, lvl->qty, lvl->live};
    }
    return k;
}

static int64_t quantity(const Ladder *l) {
    int64_t s = 0;
    for (size_t i = 0; i < l->n; i++) s += l->levels[i].qty;
    return s;
}

// ---- the stop queues: binary min-heaps on (key, seq)

typedef struct {
    int64_t key, seq;
    Handle h;
} StopEntry;

typedef struct {
    StopEntry *a;
    size_t n, cap;
} Heap;

static bool stop_less(const StopEntry *x, const StopEntry *y) { return x->key != y->key ? x->key < y->key : x->seq < y->seq; }

static void heap_push(Heap *hp, StopEntry e) {
    hp->a = grow(hp->a, &hp->cap, hp->n + 1, sizeof(StopEntry));
    size_t i = hp->n++;
    while (i > 0) {
        size_t p = (i - 1) / 2;
        if (!stop_less(&e, &hp->a[p])) break;
        hp->a[i] = hp->a[p];
        i = p;
    }
    hp->a[i] = e;
}

static StopEntry heap_pop(Heap *hp) {
    StopEntry top = hp->a[0], last = hp->a[--hp->n];
    size_t i = 0;
    for (;;) {
        size_t c = 2 * i + 1;
        if (c >= hp->n) break;
        if (c + 1 < hp->n && stop_less(&hp->a[c + 1], &hp->a[c])) c++;
        if (!stop_less(&hp->a[c], &last)) break;
        hp->a[i] = hp->a[c];
        i = c;
    }
    if (hp->n) hp->a[i] = last;
    return top;
}

// ---- the engine

#define SWEEP_EVERY 4096

typedef struct {
    Ladder bids, asks;
    Orders orders;
    Trades log;
    Heap buy_stops, sell_stops;
    CommandQueue work;
    Handle *fired;
    size_t nfired, fcap;
    Map halted;
    int64_t rejects[REJECT_KINDS];
    int64_t snap_event, snap_live, snap_fills;
    int64_t every, now, events, cancels;
} Engine;

static void refuse(Engine *e, int code) {
    e->rejects[code]++;
    e->log.checksum = mix(e->log.checksum, 0x7e00 + (uint64_t)code);
}

static void execute(Engine *e, Handle h);

static void fire(Engine *e, Handle h) {
    if (!live(&e->orders, h)) return;
    Order *o = pool_get(&e->orders, h);
    o->kind = o->kind == STOPLIMIT ? LIMIT : MARKET;
    execute(e, h);
}

static void arm(Engine *e, Handle h) {
    Order *p = live(&e->orders, h);
    if (!p) return;
    Order o = *p;
    if (e->log.has_last) {
        int64_t last = e->log.last;
        if (o.side == BUY ? last >= o.stop : last <= o.stop) {
            fire(e, h);
            return;
        }
    }
    if (o.side == BUY) heap_push(&e->buy_stops, (StopEntry){o.stop, o.seq, h});
    else heap_push(&e->sell_stops, (StopEntry){-o.stop, o.seq, h});
}

static void triggers(Engine *e) {
    if (!e->log.has_last) return;
    int64_t last = e->log.last;
    while (e->buy_stops.n && e->buy_stops.a[0].key <= last) {
        StopEntry x = heap_pop(&e->buy_stops);
        if (live(&e->orders, x.h)) {
            e->fired = grow(e->fired, &e->fcap, e->nfired + 1, sizeof(Handle));
            e->fired[e->nfired++] = x.h;
        }
    }
    while (e->sell_stops.n && e->sell_stops.a[0].key <= -last) {
        StopEntry x = heap_pop(&e->sell_stops);
        if (live(&e->orders, x.h)) {
            e->fired = grow(e->fired, &e->fcap, e->nfired + 1, sizeof(Handle));
            e->fired[e->nfired++] = x.h;
        }
    }
    for (size_t i = e->nfired; i-- > 0;) CommandQueue_push_front(&e->work, (Command){.op = OP_TRIGGER, .h = e->fired[i]});
    e->nfired = 0;
}

static void execute(Engine *e, Handle h) {
    size_t before = e->log.nfills;
    Order *p = live(&e->orders, h);
    if (!p) return;
    int side = p->side;
    match(side == BUY ? &e->asks : &e->bids, &e->orders, h, e->now, &e->log);
    p = live(&e->orders, h);
    if (!p) return;
    Order o = *p;
    if (o.left == 0) {
        orders_retire(&e->orders, h);
    } else if (o.kind == LIMIT) {
        rest(side == BUY ? &e->bids : &e->asks, o.price, o.left, h);
    } else {
        // The rest of a market order is cancelled.
        orders_retire(&e->orders, h);
        if (e->log.nfills == before) {
            if (pool_get(&e->orders, h)) pool_release(&e->orders, h.idx);
            refuse(e, R_NO_LIQUIDITY);
        }
    }
    if (e->log.nfills > before) triggers(e);
}

static void add(Engine *e, Order o) {
    if (o.qty <= 0 || (o.kind != MARKET && o.kind != STOP && o.price <= 0)) {
        refuse(e, R_BAD_QUANTITY);
        return;
    }
    if (map_get(&e->halted, o.id.account)) {
        refuse(e, R_HALTED);
        return;
    }
    if (map_get(&e->orders.index, id_key(o.id))) {
        refuse(e, R_DUPLICATE);
        return;
    }
    Handle h = orders_add(&e->orders, o);
    if (o.kind == STOP || o.kind == STOPLIMIT) arm(e, h);
    else execute(e, h);
}

static void done_or_unknown(Engine *e, ClientId id) { refuse(e, map_get(&e->orders.done, id_key(id)) ? R_ALREADY_DONE : R_UNKNOWN); }

static void cancel(Engine *e, ClientId id) {
    uint64_t *hv = map_get(&e->orders.index, id_key(id));
    if (!hv) {
        done_or_unknown(e, id);
        return;
    }
    Handle h = unpack_handle(*hv);
    Order *p = live(&e->orders, h);
    if (p) {
        Order o = *p;
        orders_retire(&e->orders, h);
        if (o.kind == LIMIT) {
            Ladder *l = o.side == BUY ? &e->bids : &e->asks;
            withdraw(l, o.price, o.left, h);
            compact(l, o.price, &e->orders);
        }
    }
    e->cancels++;
}

static void amend(Engine *e, int64_t ts, ClientId id, int64_t qty, int64_t price) {
    uint64_t *hv = map_get(&e->orders.index, id_key(id));
    if (!hv) {
        done_or_unknown(e, id);
        return;
    }
    Handle h = unpack_handle(*hv);
    if (qty <= 0 || price <= 0) {
        refuse(e, R_BAD_QUANTITY);
        return;
    }
    Order *p = live(&e->orders, h);
    if (!p) return;
    Order o = *p;
    if (o.kind == LIMIT) withdraw(o.side == BUY ? &e->bids : &e->asks, o.price, o.left, h);
    // An amended order is a new arrival.
    o.qty = o.left = qty;
    if (o.kind == STOP) o.stop = price;
    else o.price = price;
    o.ts = ts;
    Handle h2 = orders_reissue(&e->orders, o);
    if (o.kind == STOP || o.kind == STOPLIMIT) arm(e, h2);
    else execute(e, h2);
}

static void apply(Engine *e, Command c) {
    switch (c.op) {
    case OP_ADD:
        e->now = c.ts;
        add(e, (Order){c.id, 0, c.side, c.kind, c.price, c.stop, c.qty, c.qty, c.ts});
        break;
    case OP_CANCEL:
        e->now = c.ts;
        cancel(e, c.id);
        break;
    case OP_AMEND:
        e->now = c.ts;
        amend(e, c.ts, c.id, c.qty, c.price);
        break;
    case OP_HALT:
        e->now = c.ts;
        map_put(&e->halted, c.account, 1);
        break;
    case OP_RESUME:
        e->now = c.ts;
        map_remove(&e->halted, c.account);
        break;
    case OP_BUST:
        e->now = c.ts;
        if (!bust(&e->log)) refuse(e, R_NOTHING_TO_BUST);
        break;
    case OP_TRIGGER:
        fire(e, c.h);
        break;
    }
}

static void submit(Engine *e, Command c) {
    CommandQueue_push(&e->work, c);
    Command x;
    while (CommandQueue_pop_front(&e->work, &x)) apply(e, x);
    e->events++;
    if (e->events % SWEEP_EVERY == 0) orders_sweep(&e->orders);
    if (e->every > 0 && e->events % e->every == 0) {
        e->snap_event = e->events;
        e->snap_live = live_count(&e->orders);
        e->snap_fills = (int64_t)e->log.nfills;
    }
}

static uint64_t checksum(const Engine *e) {
    uint64_t h = e->log.checksum;
    int64_t b = 0, a = 0;
    best(&e->bids, &b);
    best(&e->asks, &a);
    h = mix_int(h, b);
    h = mix_int(h, a);
    h = mix_int(h, quantity(&e->bids));
    h = mix_int(h, quantity(&e->asks));
    return mix(h, (uint64_t)live_count(&e->orders));
}

// ---- the feed

// The magnitude is read in 64 unsigned bits with a limit per sign, 2^63
// for '-' and 2^63 - 1 otherwise, so the i64 minimum parses as in
// str.ParseInt.
static bool parse_int(const char *s, int64_t *out) {
    bool neg = false;
    if (*s == '+' || *s == '-') neg = *s++ == '-';
    if (!*s) return false;
    const uint64_t limit = neg ? (uint64_t)INT64_MAX + 1 : (uint64_t)INT64_MAX;
    uint64_t n = 0;
    for (; *s; s++) {
        if (*s < '0' || *s > '9') return false;
        uint64_t d = (uint64_t)(*s - '0');
        if (n > (limit - d) / 10) return false;
        n = n * 10 + d;
    }
    *out = neg ? (int64_t)(0 - n) : (int64_t)n;
    return true;
}

static bool digits(const char *s, size_t len, int64_t *out) {
    if (len == 0 || len > 15) return false;
    int64_t n = 0;
    for (size_t i = 0; i < len; i++) {
        if (s[i] < '0' || s[i] > '9') return false;
        n = n * 10 + (s[i] - '0');
    }
    *out = n;
    return true;
}

static bool parse_price(const char *s, int64_t *out) {
    const char *dot = strchr(s, '.');
    size_t wl = dot ? (size_t)(dot - s) : strlen(s);
    int64_t w, c = 0;
    if (!digits(s, wl, &w)) return false;
    if (dot) {
        size_t fl = strlen(dot + 1);
        if (fl == 0 || fl > 2 || !digits(dot + 1, fl, &c)) return false;
        if (fl == 1) c *= 10;
    }
    if (w > 9999999999999) return false;
    *out = w * 100 + c;
    return true;
}

static bool parse_account(const char *s, uint32_t *out) {
    int64_t n;
    if (!parse_int(s, &n) || n < 0 || n > 0xffffffff) return false;
    *out = (uint32_t)n;
    return true;
}

static bool parse_id(char *s, ClientId *out) {
    char *slash = strchr(s, '/');
    if (!slash) return false;
    *slash = 0;
    return parse_account(s, &out->account) && parse_account(slash + 1, &out->serial);
}

static bool is_space(char c) { return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\v' || c == '\f'; }

// parse_line turns one line into a command: 1 for a command, 0 for a
// comment or a blank line, -1 for a record that is not a command.
static int parse_line(char *line, Command *c) {
    char *cols[7] = {0};
    int count = 0;
    for (char *p = line;;) {
        char *end = strchr(p, ',');
        if (end) *end = 0;
        while (is_space(*p)) p++;
        char *q = p + strlen(p);
        while (q > p && is_space(q[-1])) *--q = 0;
        if (count == 0 && *p == '#') return 0;
        if (count < 7) cols[count] = p;
        count++;
        if (!end) break;
        p = end + 1;
    }
    if (count == 1 && !*cols[0]) return 0;
    const char *k = cols[0];
    int want = !strcmp(k, "X") ? 2 : !strcmp(k, "C") || !strcmp(k, "H") || !strcmp(k, "R") ? 3 : !strcmp(k, "M") || !strcmp(k, "A") ? 5
             : !strcmp(k, "N") || !strcmp(k, "S")                                            ? 6 : !strcmp(k, "T") ? 7 : 0;
    if (!want || count != want) return -1;
    memset(c, 0, sizeof *c);
    if (!parse_int(cols[1], &c->ts)) return -1;
    if (*k == 'X') {
        c->op = OP_BUST;
        return 1;
    }
    if (*k == 'H' || *k == 'R') {
        c->op = *k == 'H' ? OP_HALT : OP_RESUME;
        return parse_account(cols[2], &c->account) ? 1 : -1;
    }
    if (!parse_id(cols[2], &c->id)) return -1;
    if (*k == 'C') {
        c->op = OP_CANCEL;
        return 1;
    }
    if (*k == 'A') {
        c->op = OP_AMEND;
        return parse_int(cols[3], &c->qty) && parse_price(cols[4], &c->price) ? 1 : -1;
    }
    if (!strcmp(cols[3], "B")) c->side = BUY;
    else if (!strcmp(cols[3], "S")) c->side = SELL;
    else return -1;
    if (!parse_int(cols[4], &c->qty)) return -1;
    c->op = OP_ADD;
    switch (*k) {
    case 'N':
        c->kind = LIMIT;
        return parse_price(cols[5], &c->price) ? 1 : -1;
    case 'M':
        c->kind = MARKET;
        return 1;
    case 'S':
        c->kind = STOP;
        return parse_price(cols[5], &c->stop) ? 1 : -1;
    default:
        c->kind = STOPLIMIT;
        return parse_price(cols[5], &c->stop) && parse_price(cols[6], &c->price) ? 1 : -1;
    }
}

// ---- the statistics

typedef struct {
    int64_t open, high, low, close, volume, trades;
} Bar;

typedef struct {
    uint32_t account;
    int64_t bought, sold, fills;
} AccountRow;

typedef struct {
    bool ok;
    int64_t v;
} OptPrice;

typedef struct {
    OptPrice p50, p90, p99, vwap, buy_vwap, sell_vwap;
    int64_t at_or_below;
    Bar *bars;
    size_t nbars;
    int64_t sizes[7];
    AccountRow *accounts;
    size_t naccounts;
} Summary;

static int cmp_i64(const void *a, const void *b) {
    int64_t x = *(const int64_t *)a, y = *(const int64_t *)b;
    return (x > y) - (x < y);
}

static int cmp_account(const void *a, const void *b) {
    const AccountRow *x = a, *y = b;
    int64_t vx = x->bought + x->sold, vy = y->bought + y->sold;
    if (vx != vy) return vx > vy ? -1 : 1;
    return (x->account > y->account) - (x->account < y->account);
}

static OptPrice percentile(const int64_t *sorted, size_t n, size_t p) {
    if (!n) return (OptPrice){false, 0};
    size_t rank = (n * p + 99) / 100;
    return (OptPrice){true, sorted[rank ? rank - 1 : 0]};
}

static OptPrice vwap(const Fill *f, size_t n) {
    int64_t q = 0, x = 0;
    for (size_t i = 0; i < n; i++) {
        q += f[i].qty;
        x += f[i].qty * f[i].price;
    }
    return q ? (OptPrice){true, x / q} : (OptPrice){false, 0};
}

static const int64_t size_limits[6] = {10, 50, 100, 500, 1000, 5000};

static Summary summarize(const Fill *fills, size_t n, size_t per) {
    Summary s;
    memset(&s, 0, sizeof s);
    int64_t *prices = zalloc(n, sizeof(int64_t)), *sorted = zalloc(n, sizeof(int64_t));
    for (size_t i = 0; i < n; i++) prices[i] = fills[i].price;
    memcpy(sorted, prices, n * sizeof(int64_t));
    qsort(sorted, n, sizeof(int64_t), cmp_i64);
    s.vwap = vwap(fills, n);
    if (s.vwap.ok) {
        size_t lo = 0, hi = n;
        while (lo < hi) {
            size_t mid = lo + (hi - lo) / 2;
            if (sorted[mid] <= s.vwap.v) lo = mid + 1;
            else hi = mid;
        }
        s.at_or_below = (int64_t)lo;
    }
    Fill *xs = zalloc(n, sizeof(Fill));
    memcpy(xs, fills, n * sizeof(Fill));
    size_t cut = 0;
    for (size_t i = 0; i < n; i++) {
        if (xs[i].side == BUY) {
            Fill t = xs[i];
            xs[i] = xs[cut];
            xs[cut++] = t;
        }
    }
    s.buy_vwap = vwap(xs, cut);
    s.sell_vwap = vwap(xs + cut, n - cut);
    s.p50 = percentile(sorted, n, 50);
    s.p90 = percentile(sorted, n, 90);
    s.p99 = percentile(sorted, n, 99);
    // Bars: running totals of each bar's sizes, then each bar.
    if (n && per >= 1) {
        int64_t *volumes = zalloc(n, sizeof(int64_t));
        for (size_t i = 0; i < n; i++) volumes[i] = fills[i].qty;
        for (size_t lo = 0; lo < n; lo += per) {
            int64_t sum = 0;
            for (size_t i = lo; i < n && i < lo + per; i++) volumes[i] = sum += volumes[i];
        }
        s.bars = zalloc(n / per + 1, sizeof(Bar));
        for (size_t lo = 0; lo < n; lo += per) {
            size_t hi = lo + per < n ? lo + per : n;
            Bar b = {fills[lo].price, fills[lo].price, fills[lo].price, fills[hi - 1].price, volumes[hi - 1], (int64_t)(hi - lo)};
            for (size_t i = lo; i < hi; i++) {
                if (fills[i].price > b.high) b.high = fills[i].price;
                if (fills[i].price < b.low) b.low = fills[i].price;
            }
            s.bars[s.nbars++] = b;
        }
        if (s.nbars >= 2 && (size_t)s.bars[s.nbars - 1].trades * 2 < per) {
            Bar *prev = &s.bars[s.nbars - 2], last = s.bars[s.nbars - 1];
            if (last.high > prev->high) prev->high = last.high;
            if (last.low < prev->low) prev->low = last.low;
            prev->close = last.close;
            prev->volume += last.volume;
            prev->trades += last.trades;
            s.nbars--;
        }
        free(volumes);
    }
    for (size_t i = 0; i < n; i++) {
        int c = 0;
        for (int j = 0; j < 6 && fills[i].qty > size_limits[j]; j++) c++;
        s.sizes[c]++;
    }
    // Accounts: a map from account to its row's index.
    Map by;
    map_init(&by, 128);
    size_t cap = 0;
    for (size_t i = 0; i < n; i++) {
        const Fill *f = &fills[i];
        uint32_t buyer = f->side == BUY ? f->taker.account : f->maker.account;
        uint32_t seller = f->side == BUY ? f->maker.account : f->taker.account;
        uint32_t who[2] = {buyer, seller};
        for (int k = 0; k < 2; k++) {
            uint64_t *at = map_get(&by, who[k]);
            if (!at) {
                s.accounts = grow(s.accounts, &cap, s.naccounts + 1, sizeof(AccountRow));
                s.accounts[s.naccounts] = (AccountRow){who[k], 0, 0, 0};
                map_put(&by, who[k], s.naccounts++);
                at = map_get(&by, who[k]);
            }
            AccountRow *r = &s.accounts[*at];
            if (k == 0) r->bought += f->qty;
            else r->sold += f->qty;
            r->fills++;
        }
    }
    qsort(s.accounts, s.naccounts, sizeof(AccountRow), cmp_account);
    free(by.keys);
    free(by.vals);
    free(by.used);
    free(prices);
    free(sorted);
    free(xs);
    return s;
}

// ---- the report

static char *price_text(char *buf, int64_t t) {
    const char *sign = t < 0 ? "-" : "";
    if (t < 0) t = -t;
    sprintf(buf, "%s%" PRId64 ".%02" PRId64, sign, t / 100, t % 100);
    return buf;
}

static char *opt_text(char *buf, OptPrice p) {
    if (!p.ok) return strcpy(buf, "-");
    return price_text(buf, p.v);
}

static char *id_text(char *buf, ClientId c) {
    sprintf(buf, "%" PRIu32 "/%" PRIu32, c.account, c.serial);
    return buf;
}

// What the dogfood's JSON report holds, built whether or not it is
// written, as the dogfood builds it: its prices as text.
typedef struct {
    char (*text)[64];
    size_t n;
} ReportValues;

static ReportValues build_report(const Engine *e, const Summary *s, const Depth *bids, size_t nb, const Depth *asks, size_t na) {
    ReportValues r;
    r.text = zalloc(16 + nb + na + 3 * e->log.ntop + 4 * s->nbars, 64);
    r.n = 0;
    OptPrice ps[8] = {s->vwap, s->buy_vwap, s->sell_vwap, s->p50, s->p99, {e->log.has_last, e->log.last}, {0, 0}, {0, 0}};
    ps[6].ok = best(&e->bids, &ps[6].v);
    ps[7].ok = best(&e->asks, &ps[7].v);
    for (int i = 0; i < 8; i++)
        if (ps[i].ok) price_text(r.text[r.n++], ps[i].v);
    for (size_t i = 0; i < nb; i++) price_text(r.text[r.n++], bids[i].price);
    for (size_t i = 0; i < na; i++) price_text(r.text[r.n++], asks[i].price);
    for (size_t i = 0; i < e->log.ntop; i++) {
        id_text(r.text[r.n++], e->log.top[i].taker);
        id_text(r.text[r.n++], e->log.top[i].maker);
        price_text(r.text[r.n++], e->log.top[i].price);
    }
    for (size_t i = 0; i < s->nbars; i++) {
        price_text(r.text[r.n++], s->bars[i].open);
        price_text(r.text[r.n++], s->bars[i].high);
        price_text(r.text[r.n++], s->bars[i].low);
        price_text(r.text[r.n++], s->bars[i].close);
    }
    return r;
}

static void line_i(FILE *w, const char *label, int64_t v) { fprintf(w, "%-18s%12" PRId64 "\n", label, v); }
static void line_s(FILE *w, const char *label, const char *v) { fprintf(w, "%-18s%12s\n", label, v); }

static void fills_table(FILE *w, const char *title, const Fill *const *fills, size_t n) {
    char p[64], t[32], m[32];
    fprintf(w, "%s\n", title);
    if (!n) {
        fputs("  (none)\n", w);
        return;
    }
    for (size_t i = 0; i < n; i++) {
        const Fill *f = fills[i];
        fprintf(w, "  %10" PRId64 "  %-4s %7" PRId64 " @ %10s  taker %-9s maker %s\n", f->ts, side_name[f->side], f->qty, price_text(p, f->price),
                id_text(t, f->taker), id_text(m, f->maker));
    }
}

static int cmp_busiest(const void *a, const void *b) {
    const PriceQty *x = a, *y = b;
    if (x->qty != y->qty) return x->qty > y->qty ? -1 : 1;
    return (x->price > y->price) - (x->price < y->price);
}

static int cmp_seq(const void *a, const void *b) {
    const Order *x = a, *y = b;
    return (x->seq > y->seq) - (x->seq < y->seq);
}

static void text_report(FILE *w, Engine *e, const Summary *s, const Depth *bids, size_t nb, const Depth *asks, size_t na, const char *sum) {
    char b1[64], b2[64], b3[64], b4[64];
    Trades *t = &e->log;
    line_i(w, "commands", e->events);
    line_i(w, "orders", e->orders.seq);
    line_i(w, "fills", (int64_t)t->nfills);
    line_i(w, "cancels", e->cancels);
    line_i(w, "busts", t->busted);
    line_i(w, "live orders", live_count(&e->orders));
    line_i(w, "stop queue entries", (int64_t)(e->buy_stops.n + e->sell_stops.n));
    for (int i = 0; i < REJECT_KINDS; i++)
        if (e->rejects[i]) fprintf(w, "rejected: %-14s%12" PRId64 "\n", reject_names[i], e->rejects[i]);
    line_i(w, "volume", t->traded);
    line_s(w, "vwap", opt_text(b1, s->vwap));
    line_s(w, "vwap, buyers", opt_text(b1, s->buy_vwap));
    line_s(w, "vwap, sellers", opt_text(b1, s->sell_vwap));
    line_s(w, "median fill", opt_text(b1, s->p50));
    line_s(w, "90th percentile", opt_text(b1, s->p90));
    line_s(w, "99th percentile", opt_text(b1, s->p99));
    line_i(w, "fills <= vwap", s->at_or_below);
    line_s(w, "last", opt_text(b1, (OptPrice){t->has_last, t->last}));
    line_i(w, "traded prices", (int64_t)t->nvolume);
    fputs("\n", w);
    fprintf(w, "%10s %6s %12s | %-12s %10s %6s\n", "bid qty", "orders", "bid", "ask", "ask qty", "orders");
    size_t rows = nb > na ? nb : na;
    if (!rows) fprintf(w, "%20s%s%21s\n", "", "(the book is empty)", "");
    for (size_t i = 0; i < rows; i++) {
        if (i < nb) fprintf(w, "%10" PRId64 " %6" PRId64 " %12s", bids[i].qty, bids[i].orders, price_text(b1, bids[i].price));
        else fprintf(w, "%30s", "");
        fputs(" | ", w);
        if (i < na) fprintf(w, "%-12s %10" PRId64 " %6" PRId64, price_text(b1, asks[i].price), asks[i].qty, asks[i].orders);
        fputs("\n", w);
    }
    fputs("\n", w);
    const Fill *list[TAPE_LENGTH + TOP_FILLS];
    for (size_t i = 0; i < t->ntop; i++) list[i] = &t->top[i];
    fills_table(w, "largest fills", list, t->ntop);
    for (size_t i = 0; i < t->tape.len; i++) list[i] = FillQueue_at(&t->tape, t->tape.len - 1 - i);
    fills_table(w, "recent fills, newest first", list, t->tape.len);
    PriceQty *busiest = zalloc(t->nvolume, sizeof(PriceQty));
    memcpy(busiest, t->volume, t->nvolume * sizeof(PriceQty));
    qsort(busiest, t->nvolume, sizeof(PriceQty), cmp_busiest);
    fputs("most traded prices\n", w);
    for (size_t i = 0; i < 3 && i < t->nvolume; i++) fprintf(w, "  %12s %10" PRId64 "\n", price_text(b1, busiest[i].price), busiest[i].qty);
    free(busiest);
    Order *resting = zalloc(e->orders.nslots, sizeof(Order));
    size_t nr = 0;
    for (size_t i = 0; i < e->orders.nslots; i++)
        if (e->orders.slots[i].used && has_flag(&e->orders, e->orders.slots[i].o.seq)) resting[nr++] = e->orders.slots[i].o;
    qsort(resting, nr, sizeof(Order), cmp_seq);
    fprintf(w, "oldest resting orders (%zu live)\n", nr);
    for (size_t i = 0; i < 3 && i < nr; i++) {
        const Order *r = &resting[i];
        fprintf(w, "  %-9s %-4s %-9s %7" PRId64 " @ %10s\n", id_text(b2, r->id), side_name[r->side], kind_name[r->kind], r->left,
                price_text(b1, r->kind == STOP ? r->stop : r->price));
    }
    free(resting);
    if (s->nbars) {
        fprintf(w, "bars, newest first\n%5s %10s %10s %10s %10s %10s\n", "#", "open", "high", "low", "close", "volume");
        for (size_t i = 0; i < 10 && i < s->nbars; i++) {
            const Bar *b = &s->bars[s->nbars - 1 - i];
            fprintf(w, "%5zu %10s %10s %10s %10s %10" PRId64 "\n", s->nbars - i, price_text(b1, b->open), price_text(b2, b->high), price_text(b3, b->low),
                    price_text(b4, b->close), b->volume);
        }
    }
    static const char *labels[7] = {"<=10", "<=50", "<=100", "<=500", "<=1000", "<=5000", ">5000"};
    fputs("fill sizes\n", w);
    for (int i = 0; i < 7; i++) fprintf(w, "  %-7s%9" PRId64 "\n", labels[i], s->sizes[i]);
    fprintf(w, "accounts by volume\n%4s %8s %10s %10s %7s\n", "rank", "account", "bought", "sold", "fills");
    for (size_t i = 0; i < 10 && i < s->naccounts; i++) {
        const AccountRow *r = &s->accounts[i];
        fprintf(w, "%4zu %8" PRIu32 " %10" PRId64 " %10" PRId64 " %7" PRId64 "\n", i + 1, r->account, r->bought, r->sold, r->fills);
    }
    fprintf(w, "checksum %s\n", sum);
}

// ---- main

static int usage(const char *msg) {
    fprintf(stderr, "error: %s\nusage: orderbook replay FEED [--quiet] [--snapshot N]\n", msg);
    return 2;
}

int main(int argc, char **argv) {
    bool quiet = false;
    int64_t every = 10000;
    const char *path = NULL;
    if (argc < 2 || strcmp(argv[1], "replay")) return usage("the only command is replay");
    for (int i = 2; i < argc; i++) {
        if (!strcmp(argv[i], "--quiet")) {
            quiet = true;
        } else if (!strcmp(argv[i], "--snapshot") && i + 1 < argc) {
            if (!parse_int(argv[i + 1], &every) || every < 0 || every > 1000000000000) return usage("--snapshot does not take that value");
            i++;
        } else if (strncmp(argv[i], "--", 2) && !path) {
            path = argv[i];
        } else {
            return usage("unexpected argument");
        }
    }
    if (!path) return usage("replay needs a feed file");
    static char rbuf[64 * 1024];
    FILE *f = fopen(path, "rb");
    if (f) setvbuf(f, rbuf, _IOFBF, sizeof rbuf);
    if (!f || fseek(f, 0, SEEK_END)) {
        fprintf(stderr, "error: cannot read %s\n", path);
        return 4;
    }
    long size = ftell(f);
    if (size < 0 || fseek(f, 0, SEEK_SET)) {
        fprintf(stderr, "error: cannot read %s\n", path);
        return 4;
    }
    Engine *e = zalloc(1, sizeof(Engine));
    e->bids.sign = -1;
    e->asks.sign = 1;
    e->every = every;
    e->log.checksum = 0xcbf29ce484222325u;
    orders_init(&e->orders, size / 3 + 2);
    map_init(&e->halted, 64);
    char line[4096];
    int64_t n = 0;
    while (fgets(line, sizeof line, f)) {
        n++;
        size_t len = strlen(line);
        if (len == sizeof line - 1 && line[len - 1] != '\n' && !feof(f)) {
            fprintf(stderr, "%s:%" PRId64 ": not a valid record\n", path, n);
            return 3;
        }
        if (len && line[len - 1] == '\n') line[--len] = 0;
        if (len && line[len - 1] == '\r') line[--len] = 0;
        Command c;
        int r = strchr(line, '"') ? -1 : parse_line(line, &c);
        if (r < 0) {
            fprintf(stderr, "%s:%" PRId64 ": not a valid record\n", path, n);
            return 3;
        }
        if (r > 0) submit(e, c);
    }
    if (ferror(f)) {
        fprintf(stderr, "error: cannot read %s\n", path);
        return 4;
    }
    fclose(f);
    static char wbuf[64 * 1024];
    setvbuf(stdout, wbuf, _IOFBF, sizeof wbuf);
    Summary s = summarize(e->log.fills, e->log.nfills, 1000);
    Depth bids[5], asks[5];
    size_t nb = top_levels(&e->bids, bids, 5), na = top_levels(&e->asks, asks, 5);
    char sum[32];
    sprintf(sum, "%016" PRIx64, checksum(e));
    if (quiet) printf("commands %" PRId64 " fills %zu checksum %s\n", e->events, e->log.nfills, sum);
    else text_report(stdout, e, &s, bids, nb, asks, na, sum);
    // The report's values are built, as the dogfood builds its JSON report;
    // only its --json would write them.
    ReportValues rv = build_report(e, &s, bids, nb, asks, na);
    Expired eb = ladder_close(&e->bids, &e->orders), ea = ladder_close(&e->asks, &e->orders);
    if (!quiet) {
        printf("last snapshot at command %" PRId64 ": %" PRId64 " live orders, %" PRId64 " fills\n", e->snap_event, e->snap_live, e->snap_fills);
        printf("expired at close: %" PRId64 " bids (%" PRId64 "), %" PRId64 " asks (%" PRId64 ")\n", eb.orders, eb.qty, ea.orders, ea.qty);
    }
    free(rv.text);
    if (fflush(stdout)) return 4;
    return 0;
}
