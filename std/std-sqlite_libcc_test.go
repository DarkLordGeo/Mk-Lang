// Why: std.libc is called from C, so the C ABI of every function (size_t,
// int, const char *) must match the headers the driver generates from it.
// What: TestStdSqlite_LibcFromC, a C source that calls every std.libc
// function and the support pack's memory functions.
// How: the C file is written into a temp package at test time (no C text is
// kept as a file) and compiled with -fno-builtin, at dev and release.

package std

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// libcC returns, for each check, the number of the first failing check
// (0 when all pass). It is compiled with -fno-builtin, so clang calls
// std.libc instead of folding a call it knows; volatile sizes keep the
// huge requests out of constant folding too.
const libcC = `#include <stdlib.h>
#include <string.h>
#include <math.h>
#include "k.h"

static volatile size_t all_ones = (size_t)-1;
static volatile double neg_zero = -0.0;

static int off(const char *base, const void *p) { return p ? (int)((const char *)p - base) : -1; }

int k_check(void) {
    /* the allocator: churn with a content check */
    unsigned char *p = malloc(1);
    if (!p) return 1;
    p[0] = 7;
    size_t n = 1;
    while (n < 70000) {
        size_t m = n * 3 + 5;
        unsigned char *q = realloc(p, m);
        if (!q) return 2;
        for (size_t i = 0; i < n && i < 4096; i++)
            if (q[i] != (unsigned char)(i * 7 + 7)) return 3;
        for (size_t i = 0; i < m; i++) q[i] = (unsigned char)(i * 7 + 7);
        p = q;
        n = m;
    }
    p = realloc(p, 10);
    for (size_t i = 0; i < 10; i++)
        if (p[i] != (unsigned char)(i * 7 + 7)) return 4;
    if (((size_t)p & 15) != 0) return 5;
    free(p);
    free(NULL);
    unsigned char *z = calloc(1000, 8);
    if (!z) return 6;
    for (int i = 0; i < 8000; i++)
        if (z[i]) return 7;
    free(z);
    /* null results: overflowing products and sizes, and realloc keeps the block */
    size_t big = all_ones >> 31; /* 2^33 - 1: big * big overflows */
    if (calloc(big, big) != NULL) return 8;
    if (calloc(all_ones, 2) != NULL) return 9;
    if (malloc(all_ones) != NULL) return 10;
    if (malloc(all_ones - 15) != NULL) return 11;
    char *keep = malloc(4);
    if (!keep) return 12;
    memcpy(keep, "abc", 4);
    if (realloc(keep, all_ones) != NULL) return 13;
    if (strcmp(keep, "abc") != 0) return 14;
    free(keep);
    void *e1 = malloc(0), *e2 = malloc(0);
    if (!e1 || !e2 || e1 == e2) return 15;
    free(e1);
    free(e2);
    char *r = realloc(NULL, 5);
    if (!r) return 16;
    free(r);
    /* strings: int arguments use only their low byte; bytes compare unsigned */
    const char *s = "xAy\xff" "z";
    if (strlen(s) != 5 || strlen("") != 0) return 20;
    if (off(s, strchr(s, 0x141)) != 1) return 21;
    if (off(s, strchr(s, 0)) != 5) return 22;
    if (off(s, strchr(s, 'q')) != -1) return 23;
    if (off(s, memchr(s, -1, 5)) != 3) return 24;
    if (off(s, memchr(s, 'z', 4)) != -1) return 25;
    if (off(s, memchr(s, 'x', 0)) != -1) return 26;
    if (off("abcabc", strrchr("abcabc", 'b')) != 4) return 27;
    if (off("abc", strrchr("abc", 0)) != 3) return 28;
    if (strcmp("a\xff", "a\x01") <= 0) return 29;
    if (strcmp("ab", "abc") >= 0 || strcmp("", "") != 0) return 30;
    if (strncmp("abcx", "abcy", 3) != 0 || strncmp("abcx", "abcy", 4) >= 0) return 31;
    if (strncmp("a", "b", 0) != 0) return 32;
    if (strspn("aabbc", "ab") != 4 || strspn("", "ab") != 0 || strspn("abc", "") != 0) return 33;
    if (strcspn("select * from", " *") != 6 || strcspn("abc", "") != 3) return 34;
    if (strspn("\xff\xfe\x01", "\xfe\xff") != 2) return 35;
    /* the support pack's memory functions, overlapping moves included */
    char buf[16];
    memset(buf, 'a', sizeof buf);
    memcpy(buf, "0123456789", 10);
    memmove(buf + 2, buf, 8);
    if (memcmp(buf, "0101234567", 10) != 0) return 40;
    memmove(buf, buf + 2, 8);
    if (memcmp(buf, "0123456767", 10) != 0) return 41;
    if (memcmp("\xff", "\x01", 1) <= 0) return 42;
    /* fabs clears the sign bit */
    if (fabs(-2.5) != 2.5 || fabs(3.0) != 3.0) return 50;
    if (1.0 / fabs(neg_zero) < 0) return 51;
    return 0;
}
`

// TestStdSqlite_LibcFromC builds a package whose C source calls every
// std.libc function and the support pack's memory functions, and prints
// the first failing check (0 when all pass), at dev and release.
func TestStdSqlite_LibcFromC(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the program is built for x86_64-windows")
	}
	src := "import c.k\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n    var r: i32 = 0\n" +
		"    unsafe \"k_check is plain C\" {\n        r = k.k_check()\n    }\n    try Print(mut out, \"check {r}\\n\", (r: r))\n}\n"
	r := newSqliteRoot(t, src, false, `["."]`)
	man := "const Package = pkg.Package(\n    name: \"p\",\n    bins: [pkg.Bin(name: \"p\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n" +
		"    c_sources: [pkg.CSource(path: \"k.c\", flags: [\"-fno-builtin\"])],\n" +
		"    c_imports: [pkg.CImport(name: \"k\", headers: [\"k.h\"], defines: [], macros: [], vendor: false)],\n" +
		"    asm_sources: [],\n    targets: [],\n    unsafe_modules: [\".\"],\n    links: [],\n)\n"
	for name, text := range map[string]string{"pkg.mk": man, "k.c": libcC, "k.h": "int k_check(void);\n"} {
		if err := os.WriteFile(filepath.Join(r.pkg, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		if got := r.run(t, flags...); got != "check 0\n" {
			t.Errorf("libc from C %v: %s", flags, got)
		}
	}
}
