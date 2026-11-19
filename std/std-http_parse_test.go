// Why: http.ReadRequest is stricter than Go on purpose (CRLF only, no obs-fold, no
// Content-Length with Transfer-Encoding, one Host) and must still agree with Go.
// What: htModel, TestHttp_ParseTableModel, TestHttp_ParseAgreesWithGo, TestHttp_ParseDiff.
// How: streams serving a few bytes per read, compared line for line with htModel on an
// adversarial table and seeded mutations; Go's ReadRequest accepts all htModel accepts.

package std

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// htMemFixture is core/zz_httpmem.mk in the test's copy of std: a Reader
// over bytes in memory that serves at most step bytes per read.
const htMemFixture = `// Test fixture (std/std-http_parse_test.go): an in-memory Reader.
import std.raw

export struct HtMem {
    data: *u8
    n: int
    pos: int
    step: int
}

export HtMemOf(b: []u8, step: int): HtMem {
    return HtMem(data: raw.Data(b), n: b.Len(), pos: 0, step: step)
}

export HtMemReader(io: Io, m: mut HtMem, buf: mut []u8): Reader from m, buf {
    unsafe "the reader's ctx is m's address, alive while the reader holds m" {
        return NewReader(io, raw.Of(&HtMemRT), raw.Addr(raw.OfMut(m)), buf)
    }
}

HtMemRead(r: mut Reader, buf: mut []u8): ReadError!int {
    unsafe "ctx is the live HtMem" {
        p := raw.FromAddr[HtMem](ReaderCtx(&*r))
        var k := (*p).n - (*p).pos
        if k > (*p).step {
            k = (*p).step
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

const HtMemRT = ReaderTable(read: HtMemRead, line: ReadLineBytewise)
`

// htParseFixture is http/zz_parsetest.mk: the driver. Records on stdin are
// "<len> <step> <cap> <limit>\n<bytes>"; for each it reads requests until
// one fails and prints them, then the outcome and "end".
const htParseFixture = `// Test fixture (std/std-http_parse_test.go): the ReadRequest driver.
import std.core

export HtParseRun(sys: &System): Error|ReadError|ParseError|Utf8Error!void {
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](1 << 26)
    total := try inp.ReadAll(buf)
    data := buf[0..total]
    var out := sys.Stdout()
    var i := 0
    for i < total {
        var f: [4]int = [0; 4]
        var k := 0
        var j := i
        for data[j] != 0x0A {
            if data[j] == 0x20 {
                k += 1
            } else {
                f[k] = f[k] * 10 + int(data[j] - 0x30)
            }
            j += 1
        }
        rec := data[j + 1..j + 1 + f[0]]
        i = j + 1 + f[0]
        var ar := try Arena.New(sys.heap)
        defer ar.Free()
        var m := core.HtMemOf(rec, f[1])
        var rb: [256]u8 = [0; 256]
        var r := core.HtMemReader(sys.io, mut m, mut rb[0..f[2]])
        for {
            req := ReadRequest(&ar, sys.io, mut r, f[3]) else e {
                match e {
                    BadRequest(offset) {
                        try Print(mut out, "bad {offset}\n", (offset))
                    }
                    TooLarge {
                        try out.Str("large\n")
                    }
                    Closed {
                        try out.Str("eof\n")
                    }
                    _ {
                        try out.Str("other\n")
                    }
                }
                break
            }
            try Print(mut out, "req {m} {p} q={q}\n", (m: req.method, p: req.path, q: req.query))
            for x in 0..req.headers.Len() {
                try Print(mut out, "h {n}:{v}\n", (n: req.headers[x].name, v: req.headers[x].value))
            }
            try out.Str("b ")
            for x in 0..req.body.Len() {
                try out.Bytes(HtHex(req.body[x])[..])
            }
            try out.Str("\n")
        }
        try out.Str("end\n")
    }
    try out.Flush()
}

HtHex(c: u8): [2]u8 {
    d := "0123456789abcdef".AsBytes()
    return [d[int(c >> 4)], d[int(c & 15)]]
}
`

const htParseMain = `import std.http

export Main(sys: &System): http.Error|ReadError|ParseError|Utf8Error!void {
    try http.HtParseRun(sys)
}
`

// htRecord is one stream for the driver.
type htRecord struct {
	data             []byte
	step, cap, limit int
}

func htEncode(recs []htRecord) []byte {
	var b bytes.Buffer
	for _, r := range recs {
		fmt.Fprintf(&b, "%d %d %d %d\n", len(r.data), r.step, r.cap, r.limit)
		b.Write(r.data)
	}
	return b.Bytes()
}

// htBuildParse builds the driver in a profile ("dev" or "release").
func htBuildParse(t *testing.T, root, prof string) string {
	t.Helper()
	stdDir := filepath.Join(root, "std")
	if _, err := os.Stat(stdDir); err != nil {
		htStd(t, root, map[string]string{"core/zz_httpmem.mk": htMemFixture, "http/zz_parsetest.mk": htParseFixture})
	}
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(htParseMain), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	exe := filepath.Join(root, "parse-"+prof)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"build", "-o", exe, "main.mk"}
	if prof == "release" {
		args = []string{"build", "--release", "-o", exe, "main.mk"}
	}
	out, status := mkCmd(t, root, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// htRunParse runs the driver over recs and returns its lines.
func htRunParse(t *testing.T, exe string, recs []htRecord) []string {
	t.Helper()
	in := filepath.Join(filepath.Dir(exe), "in.bin")
	if err := os.WriteFile(in, htEncode(recs), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var stdin strings.Builder
	if _, err := io.Copy(&stdin, f); err != nil {
		t.Fatal(err)
	}
	r := runNative(t, exe, stdin.String())
	if r.code != 0 {
		t.Fatalf("%s: exit %d\n%s", filepath.Base(exe), r.code, r.stderr)
	}
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
}

// ---- The model: the strict grammar of parse.mk, written again in Go. ----

type htReq struct {
	method, path, query string
	headers             [][2]string
	body                []byte
}

type htHead struct {
	htReq
	minor, length int
	chunked       bool
}

type htBad struct{ out string }

func htBadAt(off int) htBad { return htBad{fmt.Sprintf("bad %d", off)} }

var htLarge = htBad{"large"}

func htTchar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

func htCtl(c byte) bool { return c != '\t' && (c < 0x20 || c == 0x7F) }

// htModelStream is the driver's output for one record.
func htModelStream(r htRecord) []string {
	var lines []string
	pos := 0
	for {
		req, bad := htModelOne(r.data, &pos, r.limit)
		if bad != nil {
			lines = append(lines, bad.out)
			break
		}
		lines = append(lines, fmt.Sprintf("req %s %s q=%s", req.method, req.path, req.query))
		for _, h := range req.headers {
			lines = append(lines, "h "+h[0]+":"+h[1])
		}
		lines = append(lines, "b "+fmt.Sprintf("%x", req.body))
	}
	return append(lines, "end")
}

func htModelOne(data []byte, pos *int, limit int) (htReq, *htBad) {
	var buf []byte
	skipped := 0
	for {
		if *pos >= len(data) {
			if len(buf) == 0 && skipped == 0 {
				return htReq{}, &htBad{"eof"}
			}
			b := htBadAt(skipped + len(buf))
			return htReq{}, &b
		}
		if skipped+len(buf) >= limit {
			return htReq{}, &htLarge
		}
		c := data[*pos]
		*pos++
		if len(buf) == 0 && c == '\r' {
			if *pos >= len(data) || data[*pos] != '\n' {
				b := htBadAt(skipped + 1)
				return htReq{}, &b
			}
			*pos++
			skipped += 2
			continue
		}
		buf = append(buf, c)
		n := len(buf)
		if c == '\n' {
			if n < 2 || buf[n-2] != '\r' {
				b := htBadAt(skipped + n - 1)
				return htReq{}, &b
			}
			if n >= 4 && buf[n-3] == '\n' {
				break
			}
		}
	}
	if !utf8.Valid(buf) {
		i := 0
		for i < len(buf) {
			r, size := utf8.DecodeRune(buf[i:])
			if r == utf8.RuneError && size <= 1 {
				break
			}
			i += size
		}
		b := htBadAt(skipped + i)
		return htReq{}, &b
	}
	h, bad := htParseHead(buf, false, skipped)
	if bad != nil {
		return htReq{}, bad
	}
	used := skipped + len(buf)
	if h.chunked {
		body, bad := htModelChunked(data, pos, limit-used, used)
		if bad != nil {
			return htReq{}, bad
		}
		h.body = body
		return h.htReq, nil
	}
	if h.length > limit-used {
		return htReq{}, &htLarge
	}
	length := max(h.length, 0)
	got := min(length, len(data)-*pos)
	h.body = slices.Clone(data[*pos : *pos+got])
	*pos += got
	if got < length {
		b := htBadAt(used + got)
		return htReq{}, &b
	}
	return h.htReq, nil
}

func htModelChunked(data []byte, pos *int, budget, base int) ([]byte, *htBad) {
	const (
		size = iota
		ext
		sizeLF
		dat
		datCR
		datLF
		trailer
		trailerLine
		trailerLF
		finalLF
		done
		sizeWS
		trailerName
	)
	st, sz, digits, off := size, 0, 0, 0
	var body []byte
	bad := func() ([]byte, *htBad) { b := htBadAt(base + off); return nil, &b }
	for st != done {
		if st == dat {
			got := min(sz, len(data)-*pos)
			body = append(body, data[*pos:*pos+got]...)
			*pos += got
			off += got
			if got < sz {
				return bad()
			}
			sz, st = 0, datCR
			continue
		}
		if *pos >= len(data) {
			return bad()
		}
		c := data[*pos]
		*pos++
		if off >= budget {
			return nil, &htLarge
		}
		switch st {
		case size:
			d := -1
			switch {
			case c >= '0' && c <= '9':
				d = int(c - '0')
			case c >= 'a' && c <= 'f':
				d = int(c-'a') + 10
			case c >= 'A' && c <= 'F':
				d = int(c-'A') + 10
			}
			switch {
			case d >= 0:
				if sz > (budget-off-d)/16 {
					return nil, &htLarge
				}
				sz = sz*16 + d
				digits++
			case digits == 0:
				return bad()
			case c == '\r':
				st = sizeLF
			case c == ';':
				st = ext
			case c == ' ' || c == '\t':
				st = sizeWS
			default:
				return bad()
			}
		case sizeWS:
			switch c {
			case ';':
				st = ext
			case '\r':
				st = sizeLF
			case ' ', '\t':
			default:
				return bad()
			}
		case ext:
			if c == '\r' {
				st = sizeLF
			} else if htCtl(c) {
				return bad()
			}
		case sizeLF:
			if c != '\n' {
				return bad()
			}
			if sz == 0 {
				st = trailer
			} else {
				if sz > budget-off-1 {
					return nil, &htLarge
				}
				st = dat
			}
			digits = 0
		case datCR:
			if c != '\r' {
				return bad()
			}
			st = datLF
		case datLF:
			if c != '\n' {
				return bad()
			}
			st, sz = size, 0
		case trailer:
			if c == '\r' {
				st = finalLF
			} else if htTchar(c) {
				st = trailerName
			} else {
				return bad()
			}
		case trailerName:
			if c == ':' {
				st = trailerLine
			} else if !htTchar(c) {
				return bad()
			}
		case trailerLine:
			if c == '\r' {
				st = trailerLF
			} else if htCtl(c) {
				return bad()
			}
		case trailerLF:
			if c != '\n' {
				return bad()
			}
			st = trailer
		case finalLF:
			if c != '\n' {
				return bad()
			}
			st = done
		}
		off++
	}
	if body == nil {
		body = []byte{}
	}
	return body, nil
}

func htParseHead(b []byte, reply bool, base int) (htHead, *htBad) {
	n := len(b)
	h := htHead{minor: 1, length: -1}
	fail := func(off int) (htHead, *htBad) { x := htBadAt(base + off); return htHead{}, &x }
	version := func(i int) (int, int) {
		for k := 0; k < 7; k++ {
			if i+k >= n || b[i+k] != "HTTP/1."[k] {
				return 0, i + k
			}
		}
		if i+7 >= n || b[i+7] != '0' && b[i+7] != '1' {
			return 0, i + 7
		}
		return int(b[i+7] - '0'), -1
	}
	i := 0
	authority, absolute := "", false
	if !reply {
		ms := i
		for i < n && htTchar(b[i]) {
			i++
		}
		if i == ms || i >= n || b[i] != ' ' {
			return fail(i)
		}
		h.method = string(b[ms:i])
		i++
		ts := i
		for i < n && b[i] != ' ' && b[i] != '\r' && b[i] != '\n' {
			i++
		}
		if i == ts || i >= n || b[i] != ' ' {
			return fail(i)
		}
		path, query, auth, abs, at := htTarget(h.method, b, ts, i)
		if at >= 0 {
			return fail(at)
		}
		h.path, h.query, authority, absolute = path, query, auth, abs
		i++
		m, at := version(i)
		if at >= 0 {
			return fail(at)
		}
		h.minor = m
		i += 8
	}
	if i+1 >= n || b[i] != '\r' || b[i+1] != '\n' {
		return fail(i)
	}
	i += 2
	hosts, flags, lenAt, teAt := 0, 0, -1, -1
	h.headers = [][2]string{}
	for {
		if i >= n {
			return fail(n)
		}
		if b[i] == '\r' {
			if i+1 >= n || b[i+1] != '\n' {
				return fail(i + 1)
			}
			i += 2
			break
		}
		ns := i
		for i < n && htTchar(b[i]) {
			i++
		}
		if i == ns || i >= n || b[i] != ':' {
			return fail(i)
		}
		name := string(b[ns:i])
		i++
		for i < n && (b[i] == ' ' || b[i] == '\t') {
			i++
		}
		vs, ve := i, i
		for i < n && b[i] != '\r' {
			if htCtl(b[i]) {
				return fail(i)
			}
			if b[i] != ' ' && b[i] != '\t' {
				ve = i + 1
			}
			i++
		}
		if i+1 >= n || b[i+1] != '\n' {
			return fail(i + 1)
		}
		value := string(b[vs:ve])
		switch strings.ToLower(name) {
		case "content-length":
			v := -1
			if value != "" && strings.Trim(value, "0123456789") == "" {
				u, err := strconv.ParseUint(value, 10, 63)
				if err != nil {
					return htHead{}, &htLarge
				}
				v = int(u)
			}
			if v == -1 {
				return fail(vs)
			}
			if h.length >= 0 && h.length != v {
				return fail(ns)
			}
			h.length, lenAt = v, ns
		case "transfer-encoding":
			if h.chunked || !strings.EqualFold(value, "chunked") {
				return fail(vs)
			}
			h.chunked, teAt = true, ns
		case "host":
			hosts++
			if hosts > 1 && !reply {
				return fail(ns)
			}
			if !reply {
				if _, at := htAuthority(b, vs, ve); at >= 0 {
					return fail(at)
				}
				if absolute && !strings.EqualFold(value, authority) {
					return fail(vs)
				}
			}
		case "connection":
			for _, t := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
				if strings.EqualFold(t, "close") {
					flags |= 1
				} else if strings.EqualFold(t, "keep-alive") {
					flags |= 2
				}
			}
		}
		h.headers = append(h.headers, [2]string{name, value})
		i += 2
	}
	if h.chunked && h.length >= 0 {
		return fail(max(teAt, lenAt))
	}
	if h.chunked && h.minor == 0 {
		return fail(teAt)
	}
	if !reply && h.minor == 1 && hosts == 0 {
		return fail(n - 2)
	}
	return h, nil
}

// htTarget reads a request target in its four forms (RFC 9112 3.2), as
// std/http/target.mk's ParseTarget: origin-form, absolute-form for http
// and https, authority-form for CONNECT only, "*" for OPTIONS only, each
// byte in RFC 3986's path and query sets. at is the offset of the refusal,
// or -1.
func htTarget(method string, b []byte, ts, te int) (path, query, auth string, absolute bool, at int) {
	if method == "CONNECT" {
		colon, bad := htAuthority(b, ts, te)
		if bad >= 0 {
			return "", "", "", false, bad
		}
		if colon < 0 || colon+1 == te {
			return "", "", "", false, te
		}
		return string(b[ts:te]), "", string(b[ts:te]), false, -1
	}
	if b[ts] == '*' && te == ts+1 {
		if method != "OPTIONS" {
			return "", "", "", false, ts
		}
		return "*", "", "", false, -1
	}
	ps := ts
	if b[ts] != '/' {
		k := ts
		for k < te && (b[k] >= 'a' && b[k] <= 'z' || b[k] >= 'A' && b[k] <= 'Z' || k > ts && (b[k] >= '0' && b[k] <= '9' || b[k] == '+' || b[k] == '-' || b[k] == '.')) {
			k++
		}
		if k == ts || te-k < 3 || b[k] != ':' || b[k+1] != '/' || b[k+2] != '/' {
			return "", "", "", false, ts
		}
		scheme := strings.ToLower(string(b[ts : k+3]))
		if scheme != "http://" && scheme != "https://" {
			return "", "", "", false, ts
		}
		j := k + 3
		for j < te && b[j] != '/' && b[j] != '?' {
			j++
		}
		if j == k+3 {
			return "", "", "", false, k + 3
		}
		if _, bad := htAuthority(b, k+3, j); bad >= 0 {
			return "", "", "", false, bad
		}
		auth, absolute, ps = string(b[k+3:j]), true, j
	}
	q := ps
	for q < te && b[q] != '?' {
		if !htPathByte(b[q]) || b[q] == '%' && !htEscape(b, q, te) {
			return "", "", "", false, q
		}
		q++
	}
	for x := q + 1; x < te; x++ {
		if !htPathByte(b[x]) && b[x] != '?' || b[x] == '%' && !htEscape(b, x, te) {
			return "", "", "", false, x
		}
	}
	path = "/"
	if q > ps {
		path = string(b[ps:q])
	}
	if q < te {
		query = string(b[q+1 : te])
	}
	return path, query, auth, absolute, -1
}

// htAuthority checks b[s:e] as uri-host [":" port] (RFC 3986 3.2.2, 3.2.3),
// as target.mk's CheckAuthority: the port's colon, or -1, and the offset
// of a refusal, or -1.
func htAuthority(b []byte, s, e int) (colon, at int) {
	i := s
	if i < e && b[i] == '[' {
		i++
		for i < e && b[i] != ']' {
			if !htUnreserved(b[i]) && !htSubDelim(b[i]) && b[i] != ':' {
				return -1, i
			}
			i++
		}
		if i == e || i == s+1 {
			return -1, i
		}
		i++
	} else {
		for i < e && b[i] != ':' {
			if b[i] == '%' {
				if !htEscape(b, i, e) {
					return -1, i
				}
			} else if !htUnreserved(b[i]) && !htSubDelim(b[i]) {
				return -1, i
			}
			i++
		}
	}
	if i == e {
		return -1, -1
	}
	if b[i] != ':' {
		return -1, i
	}
	for x := i + 1; x < e; x++ {
		if b[x] < '0' || b[x] > '9' {
			return -1, x
		}
	}
	return i, -1
}

func htUnreserved(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~'
}

func htSubDelim(c byte) bool {
	return c == '!' || c == '$' || c >= '&' && c <= ',' || c == ';' || c == '='
}

func htPathByte(c byte) bool {
	return htUnreserved(c) || htSubDelim(c) || c == ':' || c == '@' || c == '/' || c == '%'
}

// htEscape: b[i] is "%" followed by two hex digits before e.
func htEscape(b []byte, i, e int) bool {
	hex := func(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
	return i+2 < e && hex(b[i+1]) && hex(b[i+2])
}

// ---- Inputs ----

// htRng is splitmix64.
type htRng struct{ s uint64 }

func (r *htRng) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (r *htRng) below(n int) int { return int(r.next() % uint64(n)) }

func (r *htRng) pick(xs ...string) string { return xs[r.below(len(xs))] }

// htGen is one valid request; it always has a Host on HTTP/1.1.
func htGen(r *htRng) []byte {
	var b bytes.Buffer
	for range r.below(3) / 2 {
		b.WriteString("\r\n")
	}
	method := r.pick("GET", "POST", "PUT", "DELETE", "HEAD", "PATCH", "OPTIONS", "X-Custom")
	minor := 1
	if r.below(8) == 0 {
		minor = 0
	}
	path := ""
	for range 1 + r.below(4) {
		path += "/" + r.pick("a", "notes", "v1", "%20x", "x.y", "~u", "123", "")
	}
	target := path
	if r.below(3) == 0 {
		target += "?" + r.pick("a=1", "q=x%20y&z", "", "k")
	}
	if r.below(10) == 0 {
		target = "http://example.com" + target
	}
	fmt.Fprintf(&b, "%s %s HTTP/1.%d\r\n", method, target, minor)
	if minor == 1 {
		fmt.Fprintf(&b, "%s:%s%s\r\n", r.pick("Host", "host", "HOST"), r.pick("", " ", "\t "), r.pick("example.com", "h:8080", "[::1]:1"))
	}
	for range r.below(5) {
		name := r.pick("Accept", "X-A", "user-agent", "Cookie", "X_y", "a!#$%&'*+-.^`|~z")
		val := r.pick("text/html", "", "a b  c", "x\tz", "é", "日本", "q=0.9, */*")
		fmt.Fprintf(&b, "%s:%s%s%s\r\n", name, r.pick("", " ", "  ", "\t"), val, r.pick("", " ", "\t"))
	}
	if minor == 0 && r.below(2) == 0 {
		b.WriteString("Connection: keep-alive\r\n")
	}
	body := make([]byte, r.below(3)*r.below(40))
	for i := range body {
		body[i] = byte(r.next())
	}
	switch {
	case minor == 1 && r.below(3) == 0:
		b.WriteString("Transfer-Encoding: chunked\r\n\r\n")
		rest := body
		for len(rest) > 0 {
			k := 1 + r.below(len(rest))
			fmt.Fprintf(&b, r.pick("%x", "%X", "%04x"), k)
			b.WriteString(r.pick("", ";ext", ";a=b;c=\"d\"", " ;x"))
			b.WriteString("\r\n")
			b.Write(rest[:k])
			b.WriteString("\r\n")
			rest = rest[k:]
		}
		b.WriteString("0\r\n")
		if r.below(3) == 0 {
			b.WriteString("Trailer-A: 1\r\n")
		}
		b.WriteString("\r\n")
	case len(body) > 0 || r.below(4) == 0:
		fmt.Fprintf(&b, "Content-Length: %d\r\n\r\n", len(body))
		b.Write(body)
	default:
		b.WriteString("\r\n")
	}
	return b.Bytes()
}

// htTable is the adversarial table: each input with the outcome line the
// driver must print last (before "end"), offsets counted by hand.
var htTable = []struct {
	in, last string
}{
	{"GET / HTTP/1.1\r\nHost: x\r\n\r\n", "eof"},
	{"", "eof"},
	{"\r\n\r\nGET / HTTP/1.1\r\nHost: x\r\n\r\n", "eof"},
	{"\r\r\n", "bad 1"},
	{"\n", "bad 0"},
	{"GET / HTTP/1.1\nHost: x\r\n\r\n", "bad 14"},
	{"GET / HTTP/1.1\r\nHost: x\n\r\n", "bad 23"},
	{"GET / HTTP/1.1\r\nHost : x\r\n\r\n", "bad 20"},
	{"GET / HTTP/1.1\r\nHost: x\r\n Folded\r\n\r\n", "bad 25"},
	{"GET / HTTP/1.1\r\nHost: x\ry\r\n\r\n", "bad 24"},
	{"GET / HTTP/1.1\r\nHost: x\x00\r\n\r\n", "bad 23"},
	{"GET / HTTP/1.1\r\nHost: x\x7f\r\n\r\n", "bad 23"},
	{"GET / HTTP/1.1\r\n\r\n", "bad 16"},
	{"GET / HTTP/1.1\r\nHost: a\r\nHost: b\r\n\r\n", "bad 25"},
	{"GET / HTTP/1.0\r\n\r\n", "eof"},
	{"GET / HTTP/2.0\r\nHost: x\r\n\r\n", "bad 11"},
	{"GET / HTTP/1.2\r\nHost: x\r\n\r\n", "bad 13"},
	{"GET / http/1.1\r\nHost: x\r\n\r\n", "bad 6"},
	{"GET  / HTTP/1.1\r\nHost: x\r\n\r\n", "bad 4"},
	{"G(T / HTTP/1.1\r\nHost: x\r\n\r\n", "bad 1"},
	{" GET / HTTP/1.1\r\nHost: x\r\n\r\n", "bad 0"},
	{"GET /a#f HTTP/1.1\r\nHost: x\r\n\r\n", "bad 6"},
	{"GET /\xc3\xa9 HTTP/1.1\r\nHost: x\r\n\r\n", "bad 5"},
	{"GET example.com/ HTTP/1.1\r\nHost: x\r\n\r\n", "bad 4"},
	{"GET / HTTP/1.1\r\nHost: \xff\r\n\r\n", "bad 22"},
	{"GET / HTTP/1.1\r\nHost: \xe0\x80\x80\r\n\r\n", "bad 22"},
	{"POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 3\r\nTransfer-Encoding: chunked\r\n\r\n", "bad 45"},
	{"POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\nContent-Length: 3\r\n\r\n", "bad 54"},
	{"POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 3\r\nContent-Length: 4\r\n\r\nabcd", "bad 45"},
	{"POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 3\r\nContent-Length: 3\r\n\r\nabc", "eof"},
	{"POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 3, 3\r\n\r\nabc", "bad 42"},
	{"POST / HTTP/1.1\r\nHost: x\r\nContent-Length: +3\r\n\r\nabc", "bad 42"},
	{"POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 99999999999999999999\r\n\r\n", "large"},
	{"POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\n\r\nabc", "bad 50"},
	{"POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: gzip, chunked\r\n\r\n", "bad 45"},
	{"POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\n\r\n", "bad 73"},
	{"POST / HTTP/1.0\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n", "bad 17"},
	{"POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\nfffffffffffffffffffffffff\r\n", "large"},
	{"POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n3\nabc\r\n0\r\n\r\n", "bad 57"},
	{"POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabcd\r\n0\r\n\r\n", "bad 62"},
	{"POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n;3\r\n", "bad 56"},
	{"POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n0\r\n", "bad 67"},
	{"POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n0\r\nT\x01: v\r\n\r\n", "bad 68"},
	{"POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n3;a\x00\r\nabc\r\n0\r\n\r\n", "bad 59"},
	{"POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: Chunked\r\n\r\n1\r\nz\r\n0\r\nX: 1\r\n\r\n", "eof"},
	{"GET / HTTP/1.1\r\nHost: x\r\n\r\nGET /b HTTP/1.1\r\nHost: y\r\n\r\nGARBAGE\r\n\r\n", "bad 7"},
	{"OPTIONS * HTTP/1.1\r\nHost: x\r\n\r\n", "eof"},
	{"CONNECT h:443 HTTP/1.1\r\nHost: h:443\r\n\r\n", "eof"},
	{"GET http://h?x=1 HTTP/1.1\r\nHost: h\r\n\r\n", "eof"},
	{"GET 1http://h/ HTTP/1.1\r\nHost: h\r\n\r\n", "bad 4"},
	{"GET / HTTP/1.1\r\nHost: x\r\n\r", "bad 26"},
	{"GET / HTTP/1.1", "bad 14"},
}

// htLimitTable: limits at the edge, on one 39-byte request with a 3-byte body.
func htLimitTable() []htRecord {
	req := []byte("POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 3\r\n\r\nabc")
	var recs []htRecord
	for _, l := range []int{1, len(req) - 4, len(req) - 3, len(req) - 1, len(req), len(req) + 1} {
		recs = append(recs, htRecord{data: req, step: 7, cap: 16, limit: l})
	}
	ch := []byte("POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n0\r\n\r\n")
	for l := len(ch) - 16; l <= len(ch)+1; l++ {
		recs = append(recs, htRecord{data: ch, step: 3, cap: 0, limit: l})
	}
	return recs
}

func htInputs() []htRecord {
	var recs []htRecord
	for i, c := range htTable {
		recs = append(recs, htRecord{data: []byte(c.in), step: 1 + i%5, cap: (i * 37) % 257, limit: 1 << 20})
	}
	recs = append(recs, htLimitTable()...)
	r := &htRng{s: 0x68747470}
	for range 3000 {
		var b []byte
		for range 1 + r.below(3) {
			b = append(b, htGen(r)...)
		}
		rec := htRecord{data: b, step: 1 + r.below(64), cap: r.below(257), limit: 1 << 20}
		if r.below(10) == 0 {
			rec.limit = 1 + r.below(len(b)+8)
		}
		recs = append(recs, rec)
		// A one-byte mutation of the same stream.
		m := slices.Clone(b)
		p := r.below(len(m))
		alpha := []byte("\r\n \t:;0aZ\x00\x7f\xff\xc3/?#")
		switch r.below(3) {
		case 0:
			m[p] = alpha[r.below(len(alpha))]
		case 1:
			m = slices.Insert(m, p, alpha[r.below(len(alpha))])
		default:
			m = slices.Delete(m, p, p+1)
		}
		recs = append(recs, htRecord{data: m, step: 1 + r.below(64), cap: r.below(257), limit: 1 << 20})
	}
	return recs
}

// TestHttp_ParseTableModel checks the model against the hand-counted
// offsets of the table before it judges Mk.
func TestHttp_ParseTableModel(t *testing.T) {
	for _, c := range htTable {
		got := htModelStream(htRecord{data: []byte(c.in), step: 1, limit: 1 << 20})
		if last := got[len(got)-2]; last != c.last {
			t.Errorf("%q: model ends %q, table %q", c.in, last, c.last)
		}
	}
}

// TestHttp_ParseAgreesWithGo: every request the model accepts, Go's
// net/http.ReadRequest accepts with the same method, target, fields and
// body.
func TestHttp_ParseAgreesWithGo(t *testing.T) {
	checked := 0
	for _, rec := range htInputs() {
		var accepted []htReq
		pos := 0
		for {
			req, bad := htModelOne(rec.data, &pos, rec.limit)
			if bad != nil {
				break
			}
			accepted = append(accepted, req)
		}
		br := bufio.NewReader(bytes.NewReader(rec.data))
		for k, want := range accepted {
			// Go does not skip empty lines before a request line, which
			// RFC 9112 2.2 says a server SHOULD do; Mk does.
			for {
				p, _ := br.Peek(2)
				if string(p) != "\r\n" {
					break
				}
				br.Discard(2)
			}
			g, err := http.ReadRequest(br)
			if err != nil {
				t.Errorf("%q: request %d: Go refuses what Mk accepts: %v", rec.data, k, err)
				break
			}
			body, err := io.ReadAll(g.Body)
			if err != nil && strings.Contains(err.Error(), "invalid byte in chunk length") && bytes.Contains(rec.data, []byte(" ;")) {
				// Go refuses BWS before a chunk extension's ";", which
				// RFC 9112 7.1.1's grammar allows and Mk accepts.
				break
			}
			if err != nil {
				t.Errorf("%q: request %d: Go body: %v", rec.data, k, err)
				break
			}
			if dp, _ := url.PathUnescape(want.path); g.Method != want.method || g.URL.RawQuery != want.query || g.URL.Path != dp && !(want.path == "/" && g.URL.Path == "") && !(want.path == "*" && g.URL.Path == "*") && g.Method != "CONNECT" {
				t.Errorf("%q: request %d: Go %s %q ?%q, Mk %s %q ?%q", rec.data, k, g.Method, g.URL.Path, g.URL.RawQuery, want.method, want.path, want.query)
			}
			if !bytes.Equal(body, want.body) {
				t.Errorf("%q: request %d: Go body %q, Mk %q", rec.data, k, body, want.body)
			}
			var gh, mh []string
			for name, vs := range g.Header {
				for _, v := range vs {
					gh = append(gh, strings.ToLower(name)+":"+v)
				}
			}
			for _, h := range want.headers {
				n := strings.ToLower(h[0])
				// Go keeps one of several equal Content-Lengths.
				if n == "content-length" && slices.Contains(mh, n+":"+h[1]) {
					continue
				}
				if n != "host" && n != "transfer-encoding" {
					mh = append(mh, n+":"+h[1])
				}
			}
			slices.Sort(gh)
			slices.Sort(mh)
			if !slices.Equal(gh, mh) {
				t.Errorf("%q: request %d: Go fields %q, Mk %q", rec.data, k, gh, mh)
			}
			checked++
		}
	}
	t.Logf("%d requests accepted by the model, all accepted alike by Go", checked)
}

// TestHttp_ParseDiff runs ReadRequest (dev and release) over every input
// and compares each line with the model's.
func TestHttp_ParseDiff(t *testing.T) {
	recs := htInputs()
	var want []string
	for _, r := range recs {
		want = append(want, htModelStream(r)...)
	}
	root := t.TempDir()
	for _, prof := range []string{"dev", "release"} {
		exe := htBuildParse(t, root, prof)
		got := htRunParse(t, exe, recs)
		bad := 0
		for k := range max(len(want), len(got)) {
			var w, g string
			if k < len(want) {
				w = want[k]
			}
			if k < len(got) {
				g = got[k]
			}
			if w != g {
				bad++
				if bad <= 10 {
					t.Errorf("%s: line %d: got %q, want %q", prof, k, g, w)
				}
			}
		}
		if bad > 0 {
			t.Fatalf("%s: %d of %d lines differ", prof, bad, len(want))
		}
		t.Logf("%s: %d records, %d lines, all equal to the model", prof, len(recs), len(want))
	}
}

// htStd makes the test's std directory under root: a copy of MK_HTTP_BASE
// (a snapshot of a whole std, for use while the tree's other std files
// are mid-edit) with this tree's std/http over it, or this tree's std; then
// the fixtures.
func htStd(t *testing.T, root string, fixtures map[string]string) string {
	t.Helper()
	base := os.Getenv("MK_HTTP_BASE")
	if base == "" {
		return copyStd(t, root, fixtures)
	}
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stdDir, "http")); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(stdDir, "http"), os.DirFS("http")); err != nil {
		t.Fatal(err)
	}
	for p, text := range fixtures {
		if err := os.WriteFile(filepath.Join(stdDir, filepath.FromSlash(p)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return stdDir
}
