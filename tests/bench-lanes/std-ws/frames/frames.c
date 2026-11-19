// ws-frames, C: std.websocket's receive path written once per language
// (README.md): decode a buffer of masked client frames into messages,
// fragments joined, each payload unmasked a byte at a time into one
// message buffer, each text message checked as UTF-8 by RFC 3629's table
// (a branch per lead byte), rounds passes. The buffer is batches copies of
// one batch of seven messages (binary 0, 125, 65,535 and 70,000 bytes; text
// 126 bytes, 30,000 bytes in three fragments split inside a character, and
// 1,000 bytes ending in a byte that is not UTF-8), each frame masked with
// the four low bytes of (f + 1) * 0x9E3779B97F4A7C15, f the frame's index
// in the buffer; binary payloads are splitmix64 bytes (seed 1), text the
// 10-byte unit "a", U+00E9, U+20AC, U+1F600 repeated. Per message the
// checksum adds its length, 7 when it is binary or valid text, and its
// byte at (r * 7919 + m) % length, m its index in the pass. Unsigned
// arithmetic wraps. Usage: ws-frames <batches> <rounds>.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    uint8_t op;
    int64_t n;
    int64_t frags;
} message;

static const message batch[7] = {{2, 0, 1}, {2, 125, 1}, {1, 126, 1}, {2, 65535, 1}, {2, 70000, 1}, {1, 30000, 3}, {1, 1000, 1}};

static const uint8_t unit[10] = {0x61, 0xC3, 0xA9, 0xE2, 0x82, 0xAC, 0xF0, 0x9F, 0x98, 0x80};

#define MAX_MSG 70000

// build writes the frames of batches copies of the batch to a new buffer
// and its length to len.
static uint8_t *build(int64_t batches, int64_t *len) {
    uint8_t *pool = malloc(MAX_MSG);
    uint8_t *text = malloc(MAX_MSG);
    uint8_t *payload = malloc(MAX_MSG);
    int64_t cap = batches * (166786 + 9 * 14) + 1;
    uint8_t *out = malloc((size_t)cap);
    if (!pool || !text || !payload || !out) exit(1);
    uint64_t st = 1;
    for (int64_t i = 0; i < MAX_MSG;) {
        st += 0x9E3779B97F4A7C15ULL;
        uint64_t z = st;
        z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9ULL;
        z = (z ^ (z >> 27)) * 0x94D049BB133111EBULL;
        z ^= z >> 31;
        for (int k = 0; k < 8 && i < MAX_MSG; k++, i++) pool[i] = (uint8_t)(z >> (8 * k));
    }
    for (int64_t i = 0; i < MAX_MSG; i++) text[i] = unit[i % 10];
    int64_t o = 0;
    uint64_t f = 0;
    for (int64_t b = 0; b < batches; b++) {
        for (int mi = 0; mi < 7; mi++) {
            message m = batch[mi];
            const uint8_t *src = m.op == 1 ? text : pool;
            for (int64_t i = 0; i < m.n; i++) payload[i] = src[i];
            if (m.n == 1000) payload[999] = 0xFF;
            // Fragments: the first n/3 - 1 bytes, the next n/3 + 1, the rest.
            int64_t at = 0;
            for (int64_t j = 0; j < m.frags; j++) {
                int64_t l = m.n - at;
                if (m.frags == 3 && j == 0) {
                    l = m.n / 3 - 1;
                } else if (m.frags == 3 && j == 1) {
                    l = m.n / 3 + 1;
                }
                uint8_t b0 = 0;
                if (j == 0) b0 = m.op;
                if (j == m.frags - 1) b0 |= 0x80;
                out[o++] = b0;
                if (l <= 125) {
                    out[o++] = (uint8_t)(0x80 | l);
                } else if (l <= 65535) {
                    out[o++] = 0x80 | 126;
                    out[o++] = (uint8_t)(l >> 8);
                    out[o++] = (uint8_t)l;
                } else {
                    out[o++] = 0x80 | 127;
                    out[o++] = 0;
                    out[o++] = 0;
                    out[o++] = 0;
                    out[o++] = 0;
                    out[o++] = (uint8_t)(l >> 24);
                    out[o++] = (uint8_t)(l >> 16);
                    out[o++] = (uint8_t)(l >> 8);
                    out[o++] = (uint8_t)l;
                }
                uint64_t kw = (f + 1) * 0x9E3779B97F4A7C15ULL;
                uint8_t key[4] = {(uint8_t)kw, (uint8_t)(kw >> 8), (uint8_t)(kw >> 16), (uint8_t)(kw >> 24)};
                for (int k = 0; k < 4; k++) out[o++] = key[k];
                for (int64_t i = 0; i < l; i++) out[o++] = payload[at + i] ^ key[i & 3];
                at += l;
                f++;
            }
        }
    }
    free(pool);
    free(text);
    free(payload);
    *len = o;
    return out;
}

// valid_utf8 reports whether s is UTF-8 by RFC 3629's table of well-formed
// sequences: a branch per lead byte, the second byte's range by the lead,
// the rest continuation bytes.
static int valid_utf8(const uint8_t *s, int64_t n) {
    int64_t i = 0;
    while (i < n) {
        uint8_t c = s[i];
        if (c < 0x80) {
            i++;
            continue;
        }
        int64_t need = 0;
        uint8_t lo = 0x80, hi = 0xBF;
        if (c < 0xC2) {
            return 0;
        } else if (c < 0xE0) {
            need = 1;
        } else if (c < 0xF0) {
            need = 2;
            if (c == 0xE0) {
                lo = 0xA0;
            } else if (c == 0xED) {
                hi = 0x9F;
            }
        } else if (c < 0xF5) {
            need = 3;
            if (c == 0xF0) {
                lo = 0x90;
            } else if (c == 0xF4) {
                hi = 0x8F;
            }
        } else {
            return 0;
        }
        if (i + need >= n) return 0;
        uint8_t b = s[i + 1];
        if (b < lo || b > hi) return 0;
        for (int64_t k = 2; k <= need; k++) {
            if ((s[i + k] & 0xC0) != 0x80) return 0;
        }
        i += need + 1;
    }
    return 1;
}

// decode is one pass over buf: the checksum it adds for round r.
static uint64_t decode(const uint8_t *buf, int64_t len, uint8_t *msg, uint64_t r) {
    uint64_t sum = 0;
    int64_t pos = 0;
    int64_t mlen = 0;
    uint8_t mop = 0;
    uint64_t count = 0;
    while (pos + 2 <= len) {
        uint8_t b0 = buf[pos];
        uint8_t b1 = buf[pos + 1];
        int64_t l = b1 & 0x7F;
        int64_t hl = 2;
        if (l == 126) {
            if (pos + 4 > len) return sum + 1000003;
            l = (int64_t)buf[pos + 2] << 8 | buf[pos + 3];
            hl = 4;
        } else if (l == 127) {
            if (pos + 10 > len) return sum + 1000003;
            uint64_t x = 0;
            for (int64_t k = 2; k < 10; k++) x = x << 8 | buf[pos + k];
            if (x > MAX_MSG) return sum + 1000003;
            l = (int64_t)x;
            hl = 10;
        }
        uint8_t op = b0 & 0x0F;
        if ((b0 & 0x70) != 0 || (b1 & 0x80) == 0 || l > len - pos - hl - 4) return sum + 1000003;
        if (op != 0) {
            mop = op;
            mlen = 0;
        }
        if (l > MAX_MSG - mlen) return sum + 1000003;
        int64_t k = pos + hl;
        uint8_t key[4] = {buf[k], buf[k + 1], buf[k + 2], buf[k + 3]};
        pos = k + 4;
        for (int64_t i = 0; i < l; i++) msg[mlen + i] = buf[pos + i] ^ key[i & 3];
        mlen += l;
        pos += l;
        if ((b0 & 0x80) != 0) {
            sum += (uint64_t)mlen;
            if (mop != 1 || valid_utf8(msg, mlen)) sum += 7;
            if (mlen > 0) sum += msg[(r * 7919 + count) % (uint64_t)mlen];
            count++;
        }
    }
    return sum;
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: ws-frames <batches> <rounds>\n");
        return 2;
    }
    int64_t batches = strtoll(argv[1], 0, 10);
    int64_t rounds = strtoll(argv[2], 0, 10);
    int64_t len = 0;
    uint8_t *buf = build(batches, &len);
    uint8_t *msg = malloc(MAX_MSG);
    if (!msg) return 1;
    uint64_t total = 0;
    for (int64_t r = 0; r < rounds; r++) total += decode(buf, len, msg, (uint64_t)r);
    printf("%llu\n", (unsigned long long)total);
    free(msg);
    free(buf);
    return 0;
}
