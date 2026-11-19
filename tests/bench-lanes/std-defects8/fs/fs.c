/* fs, C: the tree operations of tests/bench-lanes/std-fs2/README.md through
 * the system's own calls. The tree is made under the temporary directory
 * (GetTempPathW on Windows, TMPDIR or /tmp on Linux) in mkbench-sd8-c: 10
 * directories d<i>, each with 10 directories s<j> of 10 one-byte files
 * f<k>, 1,110 entries.
 * Usage: fs walk <reps> | fs mkrm <reps> | fs copy <file> <reps>.
 *   walk: makes the tree, lists it reps times by recursion (Windows:
 *         FindFirstFileExW with FindExInfoBasic and FIND_FIRST_EX_LARGE_FETCH;
 *         Linux: opendir, readdir and d_type), the root not counted, removes
 *         it; prints reps, directories, files and name bytes, summed.
 *   mkrm: reps times makes the tree (CreateDirectoryW or mkdir, then
 *         CreateFileW or open, one write, close) and removes it by recursion
 *         (DeleteFileW and RemoveDirectoryW; unlinkat relative to an open
 *         directory and AT_REMOVEDIR); prints reps, entries made and the
 *         removals that left the root behind.
 *   copy: copies file reps times onto one destination (CopyFileExW;
 *         copy_file_range until it returns 0); prints reps and the bytes
 *         copied. */
#ifndef _WIN32
#define _GNU_SOURCE
#endif
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <windows.h>
#else
#include <dirent.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>
#endif

static long long dirs, files, names;

static void die(const char *what) {
    fprintf(stderr, "fs: %s failed\n", what);
    exit(1);
}

#ifdef _WIN32
typedef wchar_t ch;
#define T(s) L##s
#define FMT swprintf
#define PS L"%ls"
#else
typedef char ch;
#define T(s) s
#define FMT snprintf
#define PS "%s"
#endif

#ifdef _WIN32
static void mkdir1(const ch *p) {
    if (!CreateDirectoryW(p, NULL)) die("CreateDirectoryW");
}
static void write1(const ch *p) {
    HANDLE h = CreateFileW(p, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
    DWORD w;
    if (h == INVALID_HANDLE_VALUE) die("CreateFileW");
    if (!WriteFile(h, "x", 1, &w, NULL) || w != 1) die("WriteFile");
    CloseHandle(h);
}
static int exists(const ch *p) {
    return GetFileAttributesW(p) != INVALID_FILE_ATTRIBUTES;
}
static void walk(const ch *dir) {
    ch pat[1024];
    WIN32_FIND_DATAW fd;
    HANDLE h;
    FMT(pat, 1024, L"%ls\\*", dir);
    h = FindFirstFileExW(pat, FindExInfoBasic, &fd, FindExSearchNameMatch, NULL, FIND_FIRST_EX_LARGE_FETCH);
    if (h == INVALID_HANDLE_VALUE) die("FindFirstFileExW");
    do {
        const ch *n = fd.cFileName;
        if (n[0] == '.' && (n[1] == 0 || (n[1] == '.' && n[2] == 0))) continue;
        names += (long long)wcslen(n);
        if ((fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) && !(fd.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT)) {
            ch sub[1024];
            dirs++;
            FMT(sub, 1024, L"%ls\\%ls", dir, n);
            walk(sub);
        } else {
            files++;
        }
    } while (FindNextFileW(h, &fd));
    FindClose(h);
}
static void removeall(const ch *dir) {
    ch pat[1024];
    WIN32_FIND_DATAW fd;
    HANDLE h;
    FMT(pat, 1024, L"%ls\\*", dir);
    h = FindFirstFileExW(pat, FindExInfoBasic, &fd, FindExSearchNameMatch, NULL, FIND_FIRST_EX_LARGE_FETCH);
    if (h != INVALID_HANDLE_VALUE) {
        do {
            const ch *n = fd.cFileName;
            ch sub[1024];
            if (n[0] == '.' && (n[1] == 0 || (n[1] == '.' && n[2] == 0))) continue;
            FMT(sub, 1024, L"%ls\\%ls", dir, n);
            if ((fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) && !(fd.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT)) {
                removeall(sub);
            } else if (!DeleteFileW(sub)) {
                die("DeleteFileW");
            }
        } while (FindNextFileW(h, &fd));
        FindClose(h);
    }
    if (!RemoveDirectoryW(dir)) die("RemoveDirectoryW");
}
static long long copy1(const ch *src, const ch *dst) {
    WIN32_FILE_ATTRIBUTE_DATA d;
    if (!CopyFileExW(src, dst, NULL, NULL, NULL, 0)) die("CopyFileExW");
    if (!GetFileAttributesExW(dst, GetFileExInfoStandard, &d)) die("GetFileAttributesExW");
    return ((long long)d.nFileSizeHigh << 32) | d.nFileSizeLow;
}
static void temp(ch *out, const char *suffix) {
    ch t[MAX_PATH + 1];
    DWORD n = GetTempPathW(MAX_PATH + 1, t);
    if (n == 0) die("GetTempPathW");
    if (t[n - 1] == '\\') t[n - 1] = 0;
    FMT(out, 1024, L"%ls\\mkbench-sd8-c%hs", t, suffix);
}
#else
static void mkdir1(const ch *p) {
    if (mkdir(p, 0777) != 0) die("mkdir");
}
static void write1(const ch *p) {
    int fd = open(p, O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0666);
    if (fd < 0) die("open");
    if (write(fd, "x", 1) != 1) die("write");
    close(fd);
}
static int exists(const ch *p) {
    struct stat st;
    return lstat(p, &st) == 0;
}
static void walk(const ch *dir) {
    DIR *d = opendir(dir);
    struct dirent *e;
    if (!d) die("opendir");
    while ((e = readdir(d)) != NULL) {
        const ch *n = e->d_name;
        int isdir;
        if (n[0] == '.' && (n[1] == 0 || (n[1] == '.' && n[2] == 0))) continue;
        names += (long long)strlen(n);
        isdir = e->d_type == DT_DIR;
        if (e->d_type == DT_UNKNOWN) {
            struct stat st;
            if (fstatat(dirfd(d), n, &st, AT_SYMLINK_NOFOLLOW) != 0) die("fstatat");
            isdir = S_ISDIR(st.st_mode);
        }
        if (isdir) {
            ch sub[4096];
            dirs++;
            FMT(sub, sizeof sub, "%s/%s", dir, n);
            walk(sub);
        } else {
            files++;
        }
    }
    closedir(d);
}
/* removes the entries of the open directory fd, then fd itself is closed */
static void removein(int fd) {
    DIR *d = fdopendir(fd);
    struct dirent *e;
    if (!d) die("fdopendir");
    while ((e = readdir(d)) != NULL) {
        const ch *n = e->d_name;
        if (n[0] == '.' && (n[1] == 0 || (n[1] == '.' && n[2] == 0))) continue;
        if (unlinkat(dirfd(d), n, 0) == 0) continue;
        {
            int sub = openat(dirfd(d), n, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
            if (sub < 0) die("openat");
            removein(sub);
            if (unlinkat(dirfd(d), n, AT_REMOVEDIR) != 0) die("unlinkat");
        }
    }
    closedir(d);
}
static void removeall(const ch *dir) {
    int fd = open(dir, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (fd < 0) die("open");
    removein(fd);
    if (rmdir(dir) != 0) die("rmdir");
}
static long long copy1(const ch *src, const ch *dst) {
    int in = open(src, O_RDONLY | O_CLOEXEC);
    int out;
    long long total = 0;
    ssize_t n;
    if (in < 0) die("open src");
    out = open(dst, O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0666);
    if (out < 0) die("open dst");
    while ((n = copy_file_range(in, NULL, out, NULL, 1 << 30, 0)) > 0) total += n;
    if (n < 0) die("copy_file_range");
    close(in);
    close(out);
    return total;
}
static void temp(ch *out, const char *suffix) {
    const char *t = getenv("TMPDIR");
    if (!t || !*t) t = "/tmp";
    FMT(out, 1024, "%s/mkbench-sd8-c%s", t, suffix);
}
#endif

static long long make_tree(const ch *root) {
    long long n = 0;
    int i, j, k;
    ch p[1024];
    mkdir1(root);
    for (i = 0; i < 10; i++) {
        FMT(p, 1024, PS T("/d%d"), root, i);
        mkdir1(p);
        for (j = 0; j < 10; j++) {
            ch d[1024];
            FMT(d, 1024, PS T("/d%d/s%d"), root, i, j);
            mkdir1(d);
            for (k = 0; k < 10; k++) {
                FMT(p, 1024, PS T("/f%d"), d, k);
                write1(p);
            }
            n += 11;
        }
        n++;
    }
    return n;
}

int main(int argc, char **argv) {
    ch root[1024];
    long long n, i;
    if (argc < 3) return 2;
    if (strcmp(argv[1], "copy") == 0) {
        ch src[1024], dst[1024];
        long long bytes = 0;
        if (argc < 4) return 2;
        n = atoll(argv[3]);
#ifdef _WIN32
        if (MultiByteToWideChar(CP_UTF8, 0, argv[2], -1, src, 1024) == 0) return 2;
#else
        FMT(src, 1024, "%s", argv[2]);
#endif
        temp(dst, "-copy");
        for (i = 0; i < n; i++) bytes += copy1(src, dst);
#ifdef _WIN32
        if (!DeleteFileW(dst)) die("DeleteFileW");
#else
        if (unlink(dst) != 0) die("unlink");
#endif
        printf("%lld %lld\n", n, bytes);
        return 0;
    }
    n = atoll(argv[2]);
    temp(root, "");
    if (exists(root)) removeall(root);
    if (strcmp(argv[1], "walk") == 0) {
        make_tree(root);
        for (i = 0; i < n; i++) walk(root);
        removeall(root);
        printf("%lld %lld %lld %lld\n", n, dirs, files, names);
        return 0;
    } else {
        long long made = 0, fails = 0;
        for (i = 0; i < n; i++) {
            made += make_tree(root);
            removeall(root);
            if (exists(root)) fails++;
        }
        printf("%lld %lld %lld\n", n, made, fails);
    }
    return 0;
}
