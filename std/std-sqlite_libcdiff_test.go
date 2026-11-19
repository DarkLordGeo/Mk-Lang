// Why: std.libc's word-at-a-time strlen, memchr, strchr and strcspn read
// whole words, so alignment and bytes past the NUL are where they break.
// What: TestStdSqlite_LibcDifferential, std.libc's string functions against
// a Go model.
// How: a C source, compiled with -fno-builtin so every call reaches std.libc,
// rebuilds case k from splitmix64 seeded with 0x5EED0000 + k and returns
// one function's result; the Go model rebuilds the same case and computes
// the same results with the bytes package. The first 4,112 cases are every
// alignment 0 to 15 with every length 0 to 256; the rest have a random
// alignment and a length up to 4,096. Bytes are 1 to 255 (a third of the
// cases draw from "abcd", so searches hit), and 8 non-zero bytes follow
// each NUL, so a function that reads past it gives a wrong answer.

package std

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const libcDiffCases = 6000

// libcDiffC is the C side: d_result(k, f) is function f on case k.
const libcDiffC = `#include <stdlib.h>
#include <string.h>
#include "d.h"

static unsigned long long S;
static unsigned long long nx(void) {
    unsigned long long z = (S += 0x9E3779B97F4A7C15ULL);
    z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9ULL;
    z = (z ^ (z >> 27)) * 0x94D049BB133111EBULL;
    return z ^ (z >> 31);
}
static int byteof(int small) {
    if (small) return 'a' + (int)(nx() % 4);
    return 1 + (int)(nx() % 255);
}
static unsigned char A[4096 + 64], B[4096 + 64];
static long long off(const char *base, const void *p) { return p ? (long long)((const char *)p - base) : -1; }

long long d_result(int k, int f) {
    S = 0x5EED0000ULL + (unsigned long long)k;
    int align, len;
    if (k < 16 * 257) {
        align = k % 16;
        len = k / 16;
    } else {
        align = (int)(nx() % 16);
        len = (int)(nx() % 4097);
    }
    int small = (nx() % 3) == 0;
    char *s = (char *)A + align;
    for (int i = 0; i < len; i++) s[i] = (char)byteof(small);
    s[len] = 0;
    for (int i = 1; i <= 8; i++) s[len + i] = (char)byteof(small);
    int c = (int)(nx() % 256);
    if (len > 0 && (nx() % 2) == 1) {
        int at = (int)(nx() % (unsigned long long)len);
        c = (unsigned char)s[at];
    }
    if (nx() % 8 == 0) c += 256;
    char set[8];
    int sl = (int)(nx() % 5);
    for (int i = 0; i < sl; i++) set[i] = (char)byteof(small);
    set[sl] = 0;
    char *t = (char *)B + (int)(nx() % 16);
    for (int i = 0; i <= len; i++) t[i] = s[i];
    int mode = (int)(nx() % 4);
    if (mode == 1 && len > 0) {
        int at = (int)(nx() % (unsigned long long)len);
        t[at] = (char)byteof(small);
    } else if (mode == 2 && len > 0) {
        int at = (int)(nx() % (unsigned long long)len);
        t[at] = 0;
    } else if (mode == 3) {
        t[len] = (char)byteof(small);
        t[len + 1] = 0;
    }
    size_t n = (size_t)(nx() % (unsigned long long)(len + 3));
    size_t mn = (size_t)(nx() % (unsigned long long)(len + 9));
    switch (f) {
    case 0: return (long long)strlen(s);
    case 1: return off(s, strchr(s, c));
    case 2: return off(s, strrchr(s, c));
    case 3: return (long long)strspn(s, set);
    case 4: return (long long)strcspn(s, set);
    case 5: return strcmp(s, t);
    case 6: return strncmp(s, t, n);
    case 7: return off(s, memchr(s, c, mn));
    case 8: return off(s, strchr(s, 0));
    }
    return -2;
}
`

// libcDiffGen is the Go side of the generator.
type libcDiffGen struct{ s uint64 }

func (g *libcDiffGen) nx() uint64 {
	g.s += 0x9E3779B97F4A7C15
	z := g.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (g *libcDiffGen) byteof(small bool) byte {
	if small {
		return 'a' + byte(g.nx()%4)
	}
	return 1 + byte(g.nx()%255)
}

// cstr is the bytes of b before its first NUL.
func cstr(b []byte) []byte {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return b[:i]
	}
	return b
}

// cmpBytes is C's strcmp on a and b as written with their NULs, at most n
// bytes: the difference of the first unequal bytes as unsigned char.
func cmpBytes(a, b []byte, n int) int64 {
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return int64(a[i]) - int64(b[i])
		}
		if a[i] == 0 {
			return 0
		}
	}
	return 0
}

// libcDiffModel is the line the C side prints for case k.
func libcDiffModel(k int) string {
	g := &libcDiffGen{s: 0x5EED0000 + uint64(k)}
	var align, n int
	if k < 16*257 {
		align, n = k%16, k/16
	} else {
		align = int(g.nx() % 16)
		n = int(g.nx() % 4097)
	}
	_ = align
	small := g.nx()%3 == 0
	s := make([]byte, n+9)
	for i := 0; i < n; i++ {
		s[i] = g.byteof(small)
	}
	for i := 1; i <= 8; i++ {
		s[n+i] = g.byteof(small)
	}
	c := int(g.nx() % 256)
	if n > 0 && g.nx()%2 == 1 {
		c = int(s[int(g.nx()%uint64(n))])
	}
	if g.nx()%8 == 0 {
		c += 256
	}
	sl := int(g.nx() % 5)
	set := make([]byte, sl)
	for i := range set {
		set[i] = g.byteof(small)
	}
	g.nx() // t's alignment
	t := make([]byte, n+2)
	copy(t, s[:n+1])
	switch g.nx() % 4 {
	case 1:
		if n > 0 {
			at := int(g.nx() % uint64(n))
			t[at] = g.byteof(small)
		}
	case 2:
		if n > 0 {
			t[int(g.nx()%uint64(n))] = 0
		}
	case 3:
		t[n] = g.byteof(small)
		t[n+1] = 0
	}
	ln := int(g.nx() % uint64(n+3))
	mn := int(g.nx() % uint64(n+9))
	str := s[:n]
	b := byte(c)
	chr := int64(-1)
	rchr := int64(-1)
	if b == 0 {
		chr, rchr = int64(n), int64(n)
	} else {
		chr = int64(bytes.IndexByte(str, b))
		rchr = int64(bytes.LastIndexByte(str, b))
	}
	spn := 0
	for spn < n && bytes.IndexByte(set, str[spn]) >= 0 {
		spn++
	}
	cspn := 0
	for cspn < n && bytes.IndexByte(set, str[cspn]) < 0 {
		cspn++
	}
	// t's comparison length: its bytes up to and including its NUL.
	tl := len(cstr(t)) + 1
	ts := t[:tl]
	sz := s[:n+1]
	full := n + 1
	if tl > full {
		full = tl
	}
	pad := func(x []byte) []byte { return append(append([]byte{}, x...), make([]byte, full-len(x))...) }
	cmp := cmpBytes(pad(sz), pad(ts), full)
	ncmp := cmpBytes(pad(sz), pad(ts), min(ln, full))
	mc := int64(bytes.IndexByte(s[:mn], b))
	return fmt.Sprintf("%d %d %d %d %d %d %d %d %d", n, chr, rchr, spn, cspn, cmp, ncmp, mc, n)
}

// libcDiffMain prints d_result(k, 0..8) for every case, one line each.
func libcDiffMain() string {
	return fmt.Sprintf(`import c.d

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var k: i32 = 0
    for k < %d {
        var v: [9]i64 = [0; 9]
        unsafe "d_result is plain C" {
            var f: i32 = 0
            for f < 9 {
                v[int(f)] = d.d_result(k, f)
                f += 1
            }
        }
        try Print(mut out, "{a} {b} {c} {d} {e} {f} {g} {h} {i}\n", (a: v[0], b: v[1], c: v[2], d: v[3], e: v[4], f: v[5], g: v[6], h: v[7], i: v[8]))
        k += 1
    }
}
`, libcDiffCases)
}

// TestStdSqlite_LibcDifferential runs the C side against std.libc at dev
// and release and compares every line with the Go model.
func TestStdSqlite_LibcDifferential(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the program is built for x86_64-windows")
	}
	var want strings.Builder
	for k := 0; k < libcDiffCases; k++ {
		want.WriteString(libcDiffModel(k))
		want.WriteByte('\n')
	}
	r := newSqliteRoot(t, libcDiffMain(), false, `["."]`)
	man := "const Package = pkg.Package(\n    name: \"p\",\n    bins: [pkg.Bin(name: \"p\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n" +
		"    c_sources: [pkg.CSource(path: \"d.c\", flags: [\"-fno-builtin\"])],\n" +
		"    c_imports: [pkg.CImport(name: \"d\", headers: [\"d.h\"], defines: [], macros: [], vendor: false)],\n" +
		"    asm_sources: [],\n    targets: [],\n    unsafe_modules: [\".\"],\n    links: [],\n)\n"
	for name, text := range map[string]string{"pkg.mk": man, "d.c": libcDiffC, "d.h": "long long d_result(int k, int f);\n"} {
		if err := os.WriteFile(filepath.Join(r.pkg, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wl := strings.Split(want.String(), "\n")
	for _, flags := range [][]string{nil, {"--release"}} {
		got := r.run(t, flags...)
		gl := strings.Split(got, "\n")
		if len(gl) != len(wl) {
			t.Fatalf("libc differential %v: %d lines, want %d\n%s", flags, len(gl), len(wl), got[:min(len(got), 2000)])
		}
		bad := 0
		for k := range wl {
			if gl[k] != wl[k] {
				if bad < 10 {
					t.Errorf("libc differential %v case %d (fields: strlen strchr strrchr strspn strcspn strcmp strncmp memchr strchr0):\ngot  %s\nwant %s", flags, k, gl[k], wl[k])
				}
				bad++
			}
		}
		if bad > 0 {
			t.Errorf("libc differential %v: %d of %d cases differ", flags, bad, libcDiffCases)
		}
	}
}
