/* stat, C: the facts of one existing file, n times, through the system's
 * own call: GetFileAttributesExW on Windows (what Go's and Rust's
 * standard libraries call first), stat(2) on Linux; sum adds each answer's
 * size. Prints n, sum and the failed calls. Usage: stat <path> <n>. */
#include <stdio.h>
#include <stdlib.h>
#ifdef _WIN32
#include <windows.h>
#else
#include <sys/stat.h>
#endif

int main(int argc, char **argv) {
    long n, i, fails = 0;
    long long sum = 0;
    if (argc < 3) return 2;
    n = atol(argv[2]);
#ifdef _WIN32
    wchar_t w[4096];
    if (MultiByteToWideChar(CP_UTF8, 0, argv[1], -1, w, 4096) == 0) return 2;
    for (i = 0; i < n; i++) {
        WIN32_FILE_ATTRIBUTE_DATA d;
        if (!GetFileAttributesExW(w, GetFileExInfoStandard, &d)) { fails++; continue; }
        sum += ((long long)d.nFileSizeHigh << 32) | d.nFileSizeLow;
    }
#else
    for (i = 0; i < n; i++) {
        struct stat st;
        if (stat(argv[1], &st) != 0) { fails++; continue; }
        sum += st.st_size;
    }
#endif
    printf("%ld %lld %ld\n", n, sum, fails);
    return 0;
}
