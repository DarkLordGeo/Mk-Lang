// grep, C, hand: the task of README.md for the lit variant only (C's std has
// neither a regex nor a case-folding search, so re and ci have no C cell).
// ISO C has no directory listing, so the walk is written by hand on the
// platform's API, labelled "platform API, not ISO C": Win32
// FindFirstFileExW and FindNextFileW (names converted to UTF-8 with
// WideCharToMultiByte), or POSIX opendir and readdir on Linux. The Win32
// walk passes FindExInfoBasic and FIND_FIRST_EX_LARGE_FETCH, a performance
// flag for larger directory reads, which Rust std's read_dir leaves out;
// Mk's std.fs reads directory blocks with
// GetFileInformationByHandleEx(FileFullDirectoryInfo), as Go does. Each
// directory's entries are collected, names starting with "." dropped, the
// rest sorted bytewise (qsort with strcmp), then visited depth first, files
// and directories interleaved, as mgrep walks. A file is read whole with
// fread into one buffer reused for every file (on Windows, standard output is
// set to binary mode with _setmode, so a newline is LF, as in the other
// cells). A file with a NUL byte in its
// first 1,024 bytes is skipped as binary; every other file prints
// "path:count", the number of its lines that hold the literal. A file that
// is valid UTF-8 (a hand validator, 30 lines) is searched whole: the next
// occurrence from the current line's start is found with memchr on its first
// byte and memcmp, its line counted, and the search goes on at the next line.
// A file that is not has its lines judged one by one, and a line that is not
// valid UTF-8 is skipped.
// Usage: grep -c -j 1 --path-separator / -F PATTERN DIR.
#ifndef _WIN32
#define _DEFAULT_SOURCE
#endif
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#include <windows.h>
#else
#include <dirent.h>
#include <sys/stat.h>
#endif

#define SNIFF 1024

// The bytes that make a pattern a regex (mgrep's MetaByte).
static const char meta[] = "\\.+*?()|[]{}^$";

static const unsigned char *needle;
static size_t needle_len;
static unsigned char *buf;
static size_t buf_cap;
static int matched, failed;

typedef struct {
    char *name;
    int dir;
} item;

static void usage(void) {
    fprintf(stderr, "usage: grep -c -j 1 --path-separator / -F PATTERN DIR\n");
    exit(2);
}

static void *xmalloc(size_t n) {
    void *p = malloc(n ? n : 1);
    if (!p) {
        fprintf(stderr, "grep: out of memory\n");
        exit(2);
    }
    return p;
}

static void *xrealloc(void *p, size_t n) {
    p = realloc(p, n);
    if (!p) {
        fprintf(stderr, "grep: out of memory\n");
        exit(2);
    }
    return p;
}

static char *concat3(const char *a, const char *b, const char *c) {
    size_t la = strlen(a), lb = strlen(b), lc = strlen(c);
    char *s = xmalloc(la + lb + lc + 1);
    memcpy(s, a, la);
    memcpy(s + la, b, lb);
    memcpy(s + la + lb, c, lc + 1);
    return s;
}

// The printed name of leaf inside the directory printed as name.
static char *joined(const char *name, const char *leaf) {
    size_t n = strlen(name);
    int ends = n > 0 && (name[n - 1] == '/' || name[n - 1] == '\\');
    return concat3(name, ends ? "" : "/", leaf);
}

static int by_name(const void *a, const void *b) {
    return strcmp(((const item *)a)->name, ((const item *)b)->name);
}

// Whether s[0..n) is valid UTF-8: no overlong form, no surrogate, nothing
// past U+10FFFF.
static int valid_utf8(const unsigned char *s, size_t n) {
    size_t i = 0;
    while (i < n) {
        unsigned char c = s[i];
        if (c < 0x80) {
            i++;
            continue;
        }
        size_t k;
        unsigned char lo = 0x80, hi = 0xBF;
        if (c >= 0xC2 && c <= 0xDF) {
            k = 1;
        } else if (c >= 0xE0 && c <= 0xEF) {
            k = 2;
            if (c == 0xE0) lo = 0xA0;
            if (c == 0xED) hi = 0x9F;
        } else if (c >= 0xF0 && c <= 0xF4) {
            k = 3;
            if (c == 0xF0) lo = 0x90;
            if (c == 0xF4) hi = 0x8F;
        } else {
            return 0;
        }
        if (n - i - 1 < k) return 0;
        if (s[i + 1] < lo || s[i + 1] > hi) return 0;
        for (size_t j = 2; j <= k; j++) {
            if (s[i + j] < 0x80 || s[i + j] > 0xBF) return 0;
        }
        i += k + 1;
    }
    return 1;
}

// The lines of s[0..n) that hold the literal: the next occurrence from the
// start of the current line, then on from the line after it. The literal
// holds no line break, so an occurrence never spans two lines.
static size_t lines(const unsigned char *s, size_t n) {
    size_t count = 0, pos = 0;
    while (pos + needle_len <= n) {
        const unsigned char *p = memchr(s + pos, needle[0], n - pos - needle_len + 1);
        if (!p) break;
        size_t at = (size_t)(p - s);
        if (memcmp(p + 1, needle + 1, needle_len - 1) != 0) {
            pos = at + 1;
            continue;
        }
        count++;
        const unsigned char *nl = memchr(p, '\n', n - at);
        if (!nl) break;
        pos = (size_t)(nl - s) + 1;
    }
    return count;
}

// The number of lines of s[0..n) that hold the literal. A buffer that is not
// valid UTF-8 is judged line by line, and its invalid lines are skipped.
static size_t count_lines(const unsigned char *s, size_t n) {
    if (valid_utf8(s, n)) return lines(s, n);
    size_t count = 0, pos = 0;
    while (pos < n) {
        const unsigned char *nl = memchr(s + pos, '\n', n - pos);
        size_t end = nl ? (size_t)(nl - s) : n;
        size_t len = end - pos;
        if (len > 0 && s[end - 1] == '\r') len--;
        if (valid_utf8(s + pos, len) && lines(s + pos, len) > 0) count++;
        pos = end + 1;
    }
    return count;
}

// Reads the file at path whole into buf; the length, or -1 on an error.
static long long read_whole(const char *path) {
#ifdef _WIN32
    int wn = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, NULL, 0);
    if (wn <= 0) return -1;
    wchar_t *wpath = xmalloc((size_t)wn * sizeof(wchar_t));
    MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, wpath, wn);
    FILE *f = _wfopen(wpath, L"rb");
    free(wpath);
#else
    FILE *f = fopen(path, "rb");
#endif
    if (!f) return -1;
    size_t len = 0;
    for (;;) {
        if (len == buf_cap) {
            buf_cap = buf_cap ? buf_cap * 2 : 1 << 16;
            buf = xrealloc(buf, buf_cap);
        }
        size_t got = fread(buf + len, 1, buf_cap - len, f);
        len += got;
        if (got == 0) break;
    }
    int bad = ferror(f);
    fclose(f);
    return bad ? -1 : (long long)len;
}

// The entries of the directory at path, names starting with "." dropped,
// in an array of *n items; NULL on an error.
static item *list(const char *path, size_t *n) {
    size_t cap = 16;
    item *items = xmalloc(cap * sizeof(item));
    *n = 0;
#ifdef _WIN32
    char *pattern = concat3(path, "\\", "*");
    int wn = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, pattern, -1, NULL, 0);
    if (wn <= 0) {
        free(pattern);
        free(items);
        return NULL;
    }
    wchar_t *wpattern = xmalloc((size_t)wn * sizeof(wchar_t));
    MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, pattern, -1, wpattern, wn);
    free(pattern);
    WIN32_FIND_DATAW fd;
    HANDLE h = FindFirstFileExW(wpattern, FindExInfoBasic, &fd, FindExSearchNameMatch, NULL, FIND_FIRST_EX_LARGE_FETCH);
    free(wpattern);
    if (h == INVALID_HANDLE_VALUE) {
        free(items);
        return NULL;
    }
    do {
        if (fd.cFileName[0] == L'.') continue;
        int un = WideCharToMultiByte(CP_UTF8, 0, fd.cFileName, -1, NULL, 0, NULL, NULL);
        if (un <= 0) continue;
        char *name = xmalloc((size_t)un);
        WideCharToMultiByte(CP_UTF8, 0, fd.cFileName, -1, name, un, NULL, NULL);
        if (*n == cap) {
            cap *= 2;
            items = xrealloc(items, cap * sizeof(item));
        }
        items[*n].name = name;
        items[*n].dir = (fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) != 0;
        (*n)++;
    } while (FindNextFileW(h, &fd));
    int done = GetLastError() == ERROR_NO_MORE_FILES;
    FindClose(h);
    if (!done) {
        fprintf(stderr, "grep: %s: listing stopped early\n", path);
        failed = 1;
    }
#else
    DIR *d = opendir(path);
    if (!d) {
        free(items);
        return NULL;
    }
    struct dirent *e;
    while ((e = readdir(d)) != NULL) {
        if (e->d_name[0] == '.') continue;
        int dir;
        if (e->d_type == DT_UNKNOWN) {
            char *full = concat3(path, "/", e->d_name);
            struct stat st;
            dir = stat(full, &st) == 0 && S_ISDIR(st.st_mode);
            free(full);
        } else {
            dir = e->d_type == DT_DIR;
        }
        if (*n == cap) {
            cap *= 2;
            items = xrealloc(items, cap * sizeof(item));
        }
        size_t len = strlen(e->d_name);
        items[*n].name = xmalloc(len + 1);
        memcpy(items[*n].name, e->d_name, len + 1);
        items[*n].dir = dir;
        (*n)++;
    }
    closedir(d);
#endif
    qsort(items, *n, sizeof(item), by_name);
    return items;
}

// Searches every file under the directory at path, printed as name.
static void walk(const char *path, const char *name) {
    size_t n;
    item *items = list(path, &n);
    if (!items) {
        fprintf(stderr, "grep: %s: cannot list the directory\n", name);
        failed = 1;
        return;
    }
#ifdef _WIN32
    const char *sep = "\\";
#else
    const char *sep = "/";
#endif
    for (size_t i = 0; i < n; i++) {
        char *child = concat3(path, sep, items[i].name);
        char *shown = joined(name, items[i].name);
        if (items[i].dir) {
            walk(child, shown);
        } else {
            long long len = read_whole(child);
            if (len < 0) {
                fprintf(stderr, "grep: %s: cannot read the file\n", shown);
                failed = 1;
            } else if (!memchr(buf, 0, (size_t)len < SNIFF ? (size_t)len : SNIFF)) {
                size_t c = count_lines(buf, (size_t)len);
                if (c > 0) matched = 1;
                fputs(shown, stdout);
                printf(":%zu\n", c);
            }
        }
        free(child);
        free(shown);
        free(items[i].name);
    }
    free(items);
}

int main(int argc, char **argv) {
    int counts = 0, fixed = 0;
    const char *rest[2];
    int nrest = 0;
    for (int i = 1; i < argc; i++) {
        const char *a = argv[i];
        if (strcmp(a, "-c") == 0) {
            counts = 1;
        } else if (strcmp(a, "-F") == 0) {
            fixed = 1;
        } else if (strcmp(a, "-j") == 0) {
            if (++i == argc || strcmp(argv[i], "1") != 0) usage();
        } else if (strcmp(a, "--path-separator") == 0) {
            if (++i == argc || strcmp(argv[i], "/") != 0) usage();
        } else if (a[0] == '-') {
            fprintf(stderr, "grep: %s: C's cell searches a literal only (the lit variant)\n", a);
            exit(2);
        } else {
            if (nrest == 2) usage();
            rest[nrest++] = a;
        }
    }
    if (!counts || nrest != 2) usage();
    if (rest[0][0] == '\0' || (!fixed && strpbrk(rest[0], meta))) {
        fprintf(stderr, "grep: a regex, which C's std cannot match (the re variant has no C cell)\n");
        exit(2);
    }
    needle = (const unsigned char *)rest[0];
    needle_len = strlen(rest[0]);
    static char out[1 << 16];
#ifdef _WIN32
    // a newline is written as LF alone, as every other cell writes it
    _setmode(_fileno(stdout), _O_BINARY);
#endif
    setvbuf(stdout, out, _IOFBF, sizeof out);
    size_t rn = strlen(rest[1]);
    char *shown = xmalloc(rn + 1);
    for (size_t i = 0; i <= rn; i++) shown[i] = rest[1][i] == '\\' ? '/' : rest[1][i];
    walk(rest[1], shown);
    free(shown);
    if (fflush(stdout) != 0) {
        fprintf(stderr, "grep: cannot write the output\n");
        exit(2);
    }
    if (failed) return 2;
    return matched ? 0 : 1;
}
