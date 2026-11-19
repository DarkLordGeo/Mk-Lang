// Why: std.http's head parsing uses vector scans, which must agree with a
// plain byte loop on every input, and a failed read must surface as such.
// What: FieldEnd (parse.mk), HeadScan (serve.mk) and ReadHead over
// Reader.Until against scalar references kept here; ReadError from a Reader
// that fails in the head, a Content-Length body and a chunked body.
// How: seeded random and adversarial inputs (control bytes, bare CR and LF,
// every offset and limit, reads of 1 to 17 bytes through 1 to 64 byte buffers).

package std

import (
	"fmt"
	"strings"
	"testing"
)

// svHttpFixture is http/zz_vectest.mk in the test's std copy.
const svHttpFixture = `// Test fixture (std/std-vector_http_test.go): FieldEnd, HeadScan and
// ReadHead against the byte loops they replaced.
import std.core

// SvFieldEndScalar is parse.mk's value loop before std-vector.
SvFieldEndScalar(b: []u8, i0: int): int {
    var i := i0
    for i < b.Len() {
        c := b[i]
        if c != HTAB && (c < 0x20 || c == 0x7F) {
            return i
        }
        i += 1
    }
    return b.Len()
}

// SvHeadScanScalar is serve.mk's HeadScan before std-vector.
SvHeadScanScalar(b: []u8, var p: HeadPos, e: int): HeadPos {
    for p.scan < e {
        ch := b[p.scan]
        if p.scan == p.s && ch == CR {
            if p.scan + 1 >= e {
                break
            }
            if b[p.scan + 1] != LF {
                p.bad = p.skipped + 1
                break
            }
            p.s += 2
            p.skipped += 2
            p.scan = p.s
            continue
        }
        if ch == LF {
            if p.scan == p.s || b[p.scan - 1] != CR {
                p.bad = p.skipped + p.scan - p.s
                break
            }
            if p.scan - p.s >= 3 && b[p.scan - 2] == LF {
                p.hend = p.scan + 1
                break
            }
        }
        p.scan += 1
    }
    return p
}

// SvReadHeadScalar is parse.mk's ReadHead before std-vector: one byte per
// Reader.Read.
SvReadHeadScalar(a: &Arena, io: Io, r: mut Reader, limit: int, base: int): Error!Raw from a {
    var buf := try a.Slice[u8](512)
    var n := 0
    var skipped := 0
    var one: [1]u8 = [0; 1]
    for {
        got := r.Read(mut one[..]) else {
            fail ReadError
        }
        if got == 0 {
            if n == 0 && skipped == 0 {
                fail Closed
            }
            fail BadRequest(offset: base + skipped + n)
        }
        if skipped + n >= limit {
            fail TooLarge
        }
        c := one[0]
        if n == 0 && c == CR {
            got2 := r.Read(mut one[..]) else {
                fail ReadError
            }
            if got2 == 0 || one[0] != LF {
                fail BadRequest(offset: base + skipped + 1)
            }
            skipped += 2
            continue
        }
        if n == buf.Len() {
            buf = try GrowBytes(a, buf[0..n], n + 1)
        }
        buf[n] = c
        n += 1
        if c == LF {
            if n < 2 || buf[n - 2] != CR {
                fail BadRequest(offset: base + skipped + n - 1)
            }
            if n >= 4 && buf[n - 3] == LF {
                break
            }
        }
    }
    head := buf[0..n]
    text := str.From(head) else {
        fail BadRequest(offset: base + skipped + Utf8Bad(head))
    }
    return Raw(text: text, skipped: skipped)
}

struct SvRng {
    x: u64
}

// SvR is a number in 0..k.
SvR(r: mut SvRng, k: int): int {
    return Cast[int](SvNext(r) % Cast[u64](k))
}

SvNext(r: mut SvRng): u64 {
    r.x = r.x ^ (r.x << 13)
    r.x = r.x ^ (r.x >> 7)
    r.x = r.x ^ (r.x << 17)
    return r.x
}

const SvAlpha: [16]u8 = [0x61, 0x62, 0x20, 0x09, 0x0D, 0x0A, 0x00, 0x1F, 0x7F, 0x80, 0xFF, 0x3A, 0x7E, 0x21, 0x41, 0x0D]

// SvFill writes b: mode 0 dense special bytes, mode 1 printable text with
// a special byte about every 50, mode 2 head-like lines (SvHead).
SvFill(r: mut SvRng, b: mut []u8, mode: int) {
    if mode == 2 {
        SvHead(r, b)
        return
    }
    for i in 0..b.Len() {
        x := SvNext(r)
        if mode == 0 || x % 50 == 0 {
            b[i] = SvAlpha[Cast[int]((x >> 8) % 16)]
        } else {
            b[i] = Trunc[u8](0x20 + (x >> 16) % 95)
        }
    }
}

// SvHead writes lines of 0 to 39 printable bytes ending in CRLF, with a
// bare LF or a lone CR one line in twenty and 0 to 2 empty lines first; a
// line of 0 bytes after another ends a head.
SvHead(r: mut SvRng, b: mut []u8) {
    n := b.Len()
    var i := 0
    lead := SvR(r, 3)
    for _ in 0..lead {
        if i + 2 <= n {
            b[i] = 0x0D
            b[i + 1] = 0x0A
            i += 2
        }
    }
    for i < n {
        var k := SvR(r, 40)
        if SvR(r, 6) == 0 {
            k = 0
        }
        var j := 0
        for j < k && i < n {
            b[i] = Trunc[u8](0x20 + SvNext(r) % 95)
            i += 1
            j += 1
        }
        y := SvNext(r) % 20
        if i < n {
            if y == 0 {
                b[i] = 0x0A
                i += 1
            } else if y == 1 {
                b[i] = 0x0D
                i += 1
            } else {
                b[i] = 0x0D
                i += 1
                if i < n {
                    b[i] = 0x0A
                    i += 1
                }
            }
        }
    }
}

SvSame(p: HeadPos, q: HeadPos): bool {
    return p.s == q.s && p.scan == q.scan && p.skipped == q.skipped && p.hend == q.hend && p.bad == q.bad
}

// SvCode is a ReadHead outcome as one number: the head's length and the
// bytes skipped, or an error kind and its offset.
SvCode(a: &Arena, io: Io, r: mut Reader, limit: int, scalar: bool): i64 {
    var res := Raw(text: "", skipped: 0)
    if scalar {
        res = SvReadHeadScalar(a, io, r, limit, 7) else e {
            return SvErr(e)
        }
    } else {
        res = ReadHead(a, io, r, limit, 7) else e {
            return SvErr(e)
        }
    }
    return i64(res.text.Len()) * 1000 + i64(res.skipped)
}

SvErr(e: Error): i64 {
    match e {
        Closed {
            return -1
        }
        TooLarge {
            return -2
        }
        ReadError {
            return -3
        }
        BadRequest(offset) {
            return -1000 - i64(offset)
        }
        _ {
            return -4
        }
    }
}

// SvRest is the count of bytes r still gives.
SvRest(r: mut Reader): int {
    var b: [64]u8 = [0; 64]
    var n := 0
    for {
        got := r.Read(mut b[..]) else {
            return -1
        }
        if got == 0 {
            return n
        }
        n += got
    }
}

export SvHttpRun(sys: &System, seed: u64): Error|ReadError!void {
    var out := sys.Stdout()
    var rng := SvRng(x: seed)
    var buf := try sys.arena.Slice[u8](4096)
    var fe_cases := 0
    var fe_bad := 0
    var hs_cases := 0
    var hs_bad := 0
    var rh_cases := 0
    var rh_bad := 0
    var rh_heads := 0
    for c in 0..3000 {
        var n := SvR(mut rng, 300)
        if c % 100 == 0 {
            n = 2000 + SvR(mut rng, 2000)
        }
        mode := c % 3
        SvFill(mut rng, mut buf[0..n], mode)
        b := buf[0..n]
        // FieldEnd from every start.
        for i0 in 0..n + 1 {
            fe_cases += 1
            if FieldEnd(b, i0) != SvFieldEndScalar(b, i0) {
                fe_bad += 1
            }
        }
        // HeadScan, fed in pieces of 1 to 64 bytes, from a start of 0 to 2.
        var s0 := SvR(mut rng, 3)
        if s0 > n {
            s0 = n
        }
        var p := HeadPos(s: s0, scan: s0, skipped: 0, hend: -1, bad: -1)
        var q := p
        var e := s0
        for {
            e += 1 + SvR(mut rng, 64)
            if e > n {
                e = n
            }
            p = HeadScan(b, p, e)
            q = SvHeadScanScalar(b, q, e)
            hs_cases += 1
            if !SvSame(p, q) {
                hs_bad += 1
                break
            }
            if p.hend >= 0 || p.bad >= 0 || e == n {
                break
            }
        }
        // ReadHead, with limits around the input's length, reads of 1 to
        // 17 bytes and reader buffers of 1 to 64 bytes; the outcome and the
        // bytes left in the reader after a head must agree.
        for k in 0..4 {
            var limit := SvR(mut rng, n + 8) - 2
            if k == 0 {
                limit = 1 << 20
            }
            step := 1 + SvR(mut rng, 17)
            cap := 1 + SvR(mut rng, 64)
            var ar := try Arena.New(sys.heap)
            defer ar.Free()
            var m1 := core.HtMemOf(b, step)
            var rb1: [64]u8 = [0; 64]
            var r1 := core.HtMemReader(sys.io, mut m1, mut rb1[0..cap])
            v1 := SvCode(&ar, sys.io, mut r1, limit, false)
            left1 := SvRest(mut r1)
            var m2 := core.HtMemOf(b, step)
            var rb2: [64]u8 = [0; 64]
            var r2 := core.HtMemReader(sys.io, mut m2, mut rb2[0..cap])
            v2 := SvCode(&ar, sys.io, mut r2, limit, true)
            left2 := SvRest(mut r2)
            rh_cases += 1
            if v1 >= 0 {
                rh_heads += 1
            }
            // After an error the reader is abandoned (ReadRequest's and
            // Fetch's callers close it), so only a head read whole must
            // leave the same bytes: a lone CR before the request line is
            // BadRequest in both, after one more byte or after its line.
            if v1 != v2 || (v1 >= 0 && left1 != left2) {
                rh_bad += 1
                try Print(mut out, "readhead differs: case {c} n {n} limit {limit} step {step} cap {cap}: {v1} left {left1}, scalar {v2} left {left2}\n", (c, n, limit, step, cap, v1, left1, v2, left2))
            }
        }
    }
    try Print(mut out, "fieldend {fe_cases} bad {fe_bad}\n", (fe_cases, fe_bad))
    try Print(mut out, "headscan {hs_cases} bad {hs_bad}\n", (hs_cases, hs_bad))
    try Print(mut out, "readhead {rh_cases} heads {rh_heads} bad {rh_bad}\n", (rh_cases, rh_heads, rh_bad))
    try out.Flush()
}
`

const svHttpMain = `import std.http

export Main(sys: &System): http.Error|ReadError!void {
    try http.SvHttpRun(sys, 0x9E3779B97F4A7C15)
}
`

// TestSTDVECTOR_HttpScansDiff runs FieldEnd, HeadScan and ReadHead against
// their scalar references, dev and release.
func TestSTDVECTOR_HttpScansDiff(t *testing.T) {
	t.Parallel()
	fx := map[string]string{"core/zz_httpmem.mk": htMemFixture, "http/zz_vectest.mk": svHttpFixture}
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			r := runNative(t, buildNative(t, fx, svHttpMain, flags...), "")
			if r.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
			var fe, feb, hs, hsb, rh, rhh, rhb int
			if _, err := fmt.Sscanf(out[strings.Index(out, "fieldend"):], "fieldend %d bad %d\nheadscan %d bad %d\nreadhead %d heads %d bad %d\n", &fe, &feb, &hs, &hsb, &rh, &rhh, &rhb); err != nil {
				t.Fatalf("output: %v\n%s", err, out)
			}
			t.Logf("fieldend %d cases, headscan %d steps, readhead %d cases (%d heads)", fe, hs, rh, rhh)
			if feb != 0 || hsb != 0 || rhb != 0 {
				t.Fatalf("differences: fieldend %d, headscan %d, readhead %d\n%.4000s", feb, hsb, rhb, out)
			}
			if fe < 100000 || hs < 3000 || rh != 12000 || rhh < 500 {
				t.Fatalf("too few cases reached: %s", out)
			}
		})
	}
}

// svFailFixture is core/zz_vecfail.mk: a Reader over bytes in memory that
// fails once its position reaches fail_at.
const svFailFixture = `// Test fixture (std/std-vector_http_test.go): a Reader that fails.
import std.raw

export struct SvFail {
    data: *u8
    n: int
    pos: int
    fail_at: int
}

export SvFailOf(b: []u8, fail_at: int): SvFail {
    return SvFail(data: raw.Data(b), n: b.Len(), pos: 0, fail_at: fail_at)
}

export SvFailReader(io: Io, m: mut SvFail, buf: mut []u8): Reader from m, buf {
    unsafe "the reader's ctx is m's address, alive while the reader holds m" {
        return NewReader(io, raw.Of(&SvFailRT), raw.Addr(raw.OfMut(m)), buf)
    }
}

SvFailRead(r: mut Reader, buf: mut []u8): ReadError!int {
    unsafe "ctx is the live SvFail" {
        p := raw.FromAddr[SvFail](ReaderCtx(&*r))
        if (*p).pos >= (*p).fail_at {
            fail ReadError
        }
        var k := (*p).fail_at - (*p).pos
        if k > (*p).n - (*p).pos {
            k = (*p).n - (*p).pos
        }
        if k > 3 {
            k = 3
        }
        if k > buf.Len() {
            k = buf.Len()
        }
        for x in 0..k {
            buf[x] = raw.Read(raw.Offset((*p).data, (*p).pos + x))
        }
        (*p).pos += k
        return k
    }
}

const SvFailRT = ReaderTable(read: SvFailRead, line: ReadLineBytewise)
`

// svFailRun is http/zz_vecfail.mk, the driver: std.core is std-internal,
// so the reader is made inside std.
const svFailRun = `// Test fixture (std/std-vector_http_test.go): the driver.
import std.core
import std.net

export SvFailRun(sys: &System): Error!void {
    var out := sys.Stdout()
    texts: [3]str = ["GET / HTTP/1.1\r\nHost: x\r\n\r\n", "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 10\r\n\r\n0123456789", "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nabcde\r\n0\r\n\r\n"]
    for t in 0..3 {
        b := texts[t].AsBytes()
        for at in 0..b.Len() + 1 {
            var ar := try Arena.New(sys.heap)
            defer ar.Free()
            var m := core.SvFailOf(b, at)
            var rb: [8]u8 = [0; 8]
            var r := core.SvFailReader(sys.io, mut m, mut rb[..])
            var k := "ok"
            _ = ReadRequest(&ar, sys.io, mut r, 4096) else e {
                match e {
                    ReadError {
                        k = "readerror"
                    }
                    net.Reset {
                        k = "reset"
                    }
                    _ {
                        k = "other"
                    }
                }
            }
            try Print(mut out, "{t} {at} {k}\n", (t, at, k))
        }
    }
    try out.Flush()
}
`

const svFailMain = `import std.http

export Main(sys: &System): http.Error!void {
    try http.SvFailRun(sys)
}
`

// TestSTDVECTOR_HttpReadError: a failed read of ReadRequest's
// Reader, at every byte of a head, a Content-Length body and a chunked
// body, is ReadError (not net.Reset), and a read that never fails is a
// request.
func TestSTDVECTOR_HttpReadError(t *testing.T) {
	t.Parallel()
	texts := []string{"GET / HTTP/1.1\r\nHost: x\r\n\r\n", "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 10\r\n\r\n0123456789", "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nabcde\r\n0\r\n\r\n"}
	var want strings.Builder
	for ti, s := range texts {
		for at := 0; at <= len(s); at++ {
			k := "readerror"
			if at == len(s) {
				k = "ok"
			}
			fmt.Fprintf(&want, "%d %d %s\n", ti, at, k)
		}
	}
	fx := map[string]string{"core/zz_vecfail.mk": svFailFixture, "http/zz_vecfail.mk": svFailRun}
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			r := runNative(t, buildNative(t, fx, svFailMain, flags...), "")
			if r.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			diffLines(t, strings.ReplaceAll(r.stdout, "\r\n", "\n"), want.String())
		})
	}
}
