// csv-read, C: read a log file (tools/bench gen kind log) as CSV records
// with the delimiter space, r times; prints the records, the fields and
// the field bytes. Usage: csv-read <file> <r>.
//
// C has no CSV reader in its standard library, so this is a hand-written
// one, labelled as such in bench.json: RFC 4180 with encoding/csv's
// reading of line breaks (CR LF or LF end a record, CR LF inside quotes
// reads as LF, blank lines skipped, a final CR dropped), the same grammar
// std.csv reads. It reads the file with fread in 64 KiB blocks and runs a
// byte-at-a-time state machine whose unquoted run is a loop over a
// 256-entry class table, the shape of csv-core's DFA. Fields are counted,
// not copied: each field's decoded length is what it adds to the bytes.
// A CR LF inside quotes split across two fread blocks counts its CR (the
// log data holds no quote).

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

enum { START, FIELD, BARE, QUOTED, CLOSED };

typedef struct {
    long long records, fields, bytes;
    int state;
    long long flen;  // decoded bytes of the open field
    int pending_cr;  // a CR seen in BARE or after a closing quote, not yet known to be data
    int nfields;     // fields of the open record
} reader;

static unsigned char cls[256];  // 1 for the delimiter, the quote, CR and LF

static void end_field(reader *r) {
    r->fields++;
    r->bytes += r->flen;
    r->nfields++;
    r->flen = 0;
}

static void end_record(reader *r) {
    r->records++;
    r->nfields = 0;
    r->state = START;
}

static int feed(reader *r, const unsigned char *p, size_t n, unsigned char delim) {
    size_t i = 0;
    while (i < n) {
        unsigned char c = p[i];
        switch (r->state) {
        case START:
            if (r->pending_cr) {
                r->pending_cr = 0;
                if (c == '\n') {  // a blank CR LF line
                    i++;
                    continue;
                }
                r->state = BARE;  // the CR was data: a field starting with CR
                r->flen = 1;
                continue;
            }
            if (c == '\n') {
                i++;
                continue;
            }
            if (c == '\r') {
                r->pending_cr = 1;
                i++;
                continue;
            }
            r->state = FIELD;
            continue;
        case FIELD:
            if (c == '"') {
                r->state = QUOTED;
                i++;
                continue;
            }
            r->state = BARE;
            continue;
        case BARE:
            if (r->pending_cr) {
                r->pending_cr = 0;
                if (c == '\n') {
                    end_field(r);
                    end_record(r);
                    i++;
                    continue;
                }
                r->flen++;  // the CR was data
                continue;
            }
            while (i < n && !cls[p[i]]) {
                r->flen++;
                i++;
            }
            if (i == n) {
                continue;
            }
            c = p[i++];
            if (c == delim) {
                end_field(r);
                r->state = FIELD;
            } else if (c == '\n') {
                end_field(r);
                end_record(r);
            } else if (c == '\r') {
                r->pending_cr = 1;
            } else {
                return -1;  // a bare quote
            }
            continue;
        case QUOTED: {
            const unsigned char *q = memchr(p + i, '"', n - i);
            size_t stop = q ? (size_t)(q - p) : n;
            for (size_t k = i; k < stop; k++) {
                // CR LF reads as LF: the CR is not counted.
                if (!(p[k] == '\r' && k + 1 < n && p[k + 1] == '\n')) {
                    r->flen++;
                }
            }
            i = stop;
            if (q) {
                r->state = CLOSED;
                i++;
            }
            continue;
        }
        case CLOSED:
            if (r->pending_cr) {
                r->pending_cr = 0;
                if (c != '\n') {
                    return -1;
                }
                end_field(r);
                end_record(r);
                i++;
                continue;
            }
            i++;
            if (c == '"') {
                r->flen++;
                r->state = QUOTED;
            } else if (c == delim) {
                end_field(r);
                r->state = FIELD;
            } else if (c == '\n') {
                end_field(r);
                end_record(r);
            } else if (c == '\r') {
                r->pending_cr = 1;
            } else {
                return -1;
            }
            continue;
        }
    }
    return 0;
}

static int finish(reader *r) {
    switch (r->state) {
    case START:
        return 0;
    case QUOTED:
        return -1;
    default:
        end_field(r);  // FIELD after a delimiter, BARE (a pending CR dropped), CLOSED
        end_record(r);
        return 0;
    }
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: csv-read <file> <r>\n");
        return 2;
    }
    int reps = atoi(argv[2]);
    unsigned char delim = ' ';
    cls[delim] = cls['"'] = cls['\r'] = cls['\n'] = 1;
    static unsigned char buf[1 << 16];
    long long records = 0, fields = 0, bytes = 0;
    for (int k = 0; k < reps; k++) {
        FILE *f = fopen(argv[1], "rb");
        if (!f) {
            perror(argv[1]);
            return 1;
        }
        reader r = {0};
        size_t n;
        while ((n = fread(buf, 1, sizeof buf, f)) > 0) {
            if (feed(&r, buf, n, delim) != 0) {
                fprintf(stderr, "quote error\n");
                return 1;
            }
        }
        fclose(f);
        if (finish(&r) != 0) {
            fprintf(stderr, "quote error\n");
            return 1;
        }
        records += r.records;
        fields += r.fields;
        bytes += r.bytes;
    }
    printf("%lld %lld %lld\n", records, fields, bytes);
    return 0;
}
