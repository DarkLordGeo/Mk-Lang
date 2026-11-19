// Why: a client that accepts an ambiguous reply framing can be desynchronised
// (response smuggling); these cases once got through.
// What: TestStdHttp_FetchRefusesLengthOverBodyBytes, ClientRefusesSmugglingReplies,
// ClientAgreesWithGoOnRefusals and ClientPoolStress.
// How: a raw server writes exact reply bytes (http2ReplyServer), and Go's
// http.ReadResponse says which framings it refuses.

package std

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestStdHttp_FetchRefusesLengthOverBodyBytes: a reply that states
// a Content-Length of 2,000,000 to a client whose body_bytes is 1,000,000
// is TooLarge at once, though the server then sends nothing; before the
// fix Fetch waited for the body, and failed ReadError when the server reset.
func TestStdHttp_FetchRefusesLengthOverBodyBytes(t *testing.T) {
	exe := http2Build(t, http2ClientProg)
	u := http2ReplyServer(t, []byte("HTTP/1.1 200 OK\r\nContent-Length: 2000000\r\n\r\n"))
	start := time.Now()
	r := runNative(t, exe, "", u, "body1m", "GET", "", "-")
	http2Want(t, r.stdout+r.stderr, []string{"error TooLarge"})
	t.Logf("refused after %v", time.Since(start))
	// At the limit it is not refused: the body is read (here cut short by
	// the server, so ReadError), never TooLarge.
	u = http2ReplyServer(t, []byte("HTTP/1.1 200 OK\r\nContent-Length: 1000000\r\n\r\nabc"))
	r = runNative(t, exe, "", u, "body1m", "GET", "", "-")
	if strings.Contains(r.stdout, "TooLarge") {
		t.Errorf("a Content-Length at body_bytes was refused:\n%s", r.stdout)
	}
}

// http2Smuggling are reply framings std.http refuses on requests and
// responses alike.
var http2Smuggling = []struct{ name, reply string }{
	{"te-and-cl", "HTTP/1.1 200 OK\r\nContent-Length: 3\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"},
	{"two-cl-differ", "HTTP/1.1 200 OK\r\nContent-Length: 3\r\nContent-Length: 4\r\n\r\nabcd"},
	{"cl-not-digits", "HTTP/1.1 200 OK\r\nContent-Length: +3\r\n\r\nabc"},
	{"te-not-final-chunked", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked, gzip\r\n\r\n0\r\n\r\n"},
	{"te-gzip-chunked", "HTTP/1.1 200 OK\r\nTransfer-Encoding: gzip, chunked\r\n\r\n0\r\n\r\n"},
	{"te-on-http10", "HTTP/1.0 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"},
	{"bare-lf-head", "HTTP/1.1 200 OK\nContent-Length: 0\n\n"},
	{"bare-cr-head", "HTTP/1.1 200 OK\r\nX: a\rb\r\nContent-Length: 0\r\n\r\n"},
	{"bare-lf-chunk-line", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n3\nabc\r\n0\r\n\r\n"},
	{"chunk-size-not-hex", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\nabc\r\n0\r\n\r\n"},
	{"obs-fold", "HTTP/1.1 200 OK\r\nX: a\r\n b\r\nContent-Length: 0\r\n\r\n"},
	{"space-before-first-field", "HTTP/1.1 200 OK\r\n X: a\r\nContent-Length: 0\r\n\r\n"},
	{"space-before-colon", "HTTP/1.1 200 OK\r\nX : a\r\nContent-Length: 0\r\n\r\n"},
	{"non-token-name", "HTTP/1.1 200 OK\r\nX(y): a\r\nContent-Length: 0\r\n\r\n"},
	{"nul-in-value", "HTTP/1.1 200 OK\r\nX: a\x00b\r\nContent-Length: 0\r\n\r\n"},
	{"ctl-in-value", "HTTP/1.1 200 OK\r\nX: a\x01b\r\nContent-Length: 0\r\n\r\n"},
	{"chunk-size-overflow", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nfffffffffffffffffff\r\n"},
}

// TestStdHttp_ClientRefusesSmugglingReplies: each shape is an error
// through Fetch, and through Do with 7-byte Exchange.Body reads (mode do,
// which prints "end" only when the whole body was read).
func TestStdHttp_ClientRefusesSmugglingReplies(t *testing.T) {
	exe := http2Build(t, http2ClientProg)
	for _, c := range http2Smuggling {
		t.Run(c.name, func(t *testing.T) {
			r := runNative(t, exe, "", http2ReplyServer(t, []byte(c.reply)), "fetch", "GET", "", "-")
			out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
			if !strings.HasPrefix(out, "error ") {
				t.Errorf("Fetch accepted %q:\n%s%s", c.reply, out, r.stderr)
			}
			r = runNative(t, exe, "", http2ReplyServer(t, []byte(c.reply)), "do", "GET", "", "-")
			out = strings.ReplaceAll(r.stdout, "\r\n", "\n")
			if strings.Contains(out, "\nend\n") {
				t.Errorf("Do read %q to its end:\n%s%s", c.reply, out, r.stderr)
			}
		})
	}
}

// TestStdHttp_ClientAgreesWithGoOnRefusals: the differential half of the
// smuggling test.
// For the fuzz seeds and the smuggling shapes, Go's http.ReadResponse and
// a read of the body to its end are the oracle: a reply Go refuses, Mk's
// Fetch refuses too (it may refuse more, by std.http's stricter rules).
func TestStdHttp_ClientAgreesWithGoOnRefusals(t *testing.T) {
	exe := http2Build(t, http2ClientProg)
	replies := []string{
		"HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5;ext=1\r\nhello\r\n0\r\nTrailer: x\r\n\r\n",
		"HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nshort",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhelloXX0\r\n\r\n",
	}
	for _, c := range http2Smuggling {
		replies = append(replies, c.reply)
	}
	goRefuses := 0
	for _, reply := range replies {
		res, err := http.ReadResponse(bufio.NewReader(bytes.NewReader([]byte(reply))), nil)
		if err == nil {
			_, err = io.ReadAll(res.Body)
			res.Body.Close()
		}
		if err == nil {
			continue
		}
		goRefuses++
		r := runNative(t, exe, "", http2ReplyServer(t, []byte(reply)), "fetch", "GET", "", "-")
		out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
		if !strings.HasPrefix(out, "error ") {
			t.Errorf("Go refuses %q (%v), Mk accepted it:\n%s%s", reply, err, out, r.stderr)
		}
	}
	if goRefuses < 8 {
		t.Errorf("only %d replies refused by Go: the oracle checks too little", goRefuses)
	}
}

// http2StressProg: args are four base URLs, the tasks and the requests per
// task. One Client with all 16 kept connections serves every task; task
// t's request i goes to origin t % 4 (a task with t % 4 == 3 rotates, to
// (t + i) % 4, so other origins' idle connections are evicted) and must
// come back as "t/i". It
// prints "good N bad M", then "final K": the 200s of one more request per
// origin after the tasks end.
const http2StressProg = `import std.http
import std.url

const NoHeaders: []http.Header = []

export Main(sys: &System): http.Error|url.Error|WriteError|ParseError!void {
    var out := sys.Stdout()
    args := sys.args
    tasks := Cast[int](try args[5].ParseInt())
    per := Cast[int](try args[6].ParseInt())
    var l := http.DefaultClientLimits
    l.idle_connections = 16
    c := http.Client.New(sys.heap, l)
    defer c.Close()
    good := Mutex[int].New(0)
    bad := Mutex[int].New(0)
    scope s := sys.io.Scope() {
        for t in 0..tasks {
            s.go Worker(sys.heap, sys.io, &c, args[1..5], t, per, &good, &bad)
        }
    }
    var final := 0
    for k in 0..4 {
        if One(sys.heap, sys.io, &c, args[1 + k], 9999, k) {
            final += 1
        }
    }
    g := good.Into()
    b := bad.Into()
    try Print(mut out, "good {g} bad {b}\nfinal {final}\n", (g, b, final))
    try out.Flush()
}

// Worker makes per requests and counts the right replies in good, the
// rest in bad.
Worker(h: &Heap, io: Io, c: &http.Client, bases: []str, t: int, per: int, good: &Mutex[int], bad: &Mutex[int]) {
    for i in 0..per {
        k := if t % 4 == 3 { (t + i) % 4 } else { t % 4 }
        ok := One(h, io, c, bases[k], t, i)
        m := if ok { good } else { bad }
        var g := m.Lock(io) else {
            return
        }
        *g.Mut() += 1
        g.Unlock()
    }
}

// One fetches base/r/t/i and checks that the body is "t/i".
One(h: &Heap, io: Io, c: &http.Client, base: str, t: int, i: int): bool {
    var ar := Arena.New(h) else {
        return false
    }
    defer ar.Free()
    s := ar.Fmt("{base}/r/{t}/{i}", (base, t, i)) else {
        return false
    }
    want := ar.Fmt("{t}/{i}", (t, i)) else {
        return false
    }
    u := url.Parse(&ar, s, none) else {
        return false
    }
    req := http.ClientRequest(method: "GET", url: &u, headers: NoHeaders, body: "".AsBytes())
    r := c.Fetch(&ar, io, &req) else {
        return false
    }
    if r.status != 200 || r.body.Len() != want.Len() {
        return false
    }
    wb := want.AsBytes()
    for k in 0..wb.Len() {
        if r.body[k] != wb[k] {
            return false
        }
    }
    return true
}
`

// TestStdHttp_ClientPoolStress: the pool's lock-free claim (the
// CAS on a slot's busy word, its release by a store, eviction and stale
// reaping) under native carriers, 1 and 16: 16 tasks of 40 requests each
// over 4 origins on one Client with 16 kept connections, most tasks
// reusing their origin's connections and four rotating over the origins,
// the server closing every fifth reply's connection with a reset (linger
// 0, so it leaves no TIME_WAIT). Every reply is the right one, the client
// goes on afterwards, and the server accepts at most one connection per
// request. One carrier configuration runs at a time.
func TestStdHttp_ClientPoolStress(t *testing.T) {
	exe := http2Build(t, http2StressProg)
	var conns atomic.Int64
	var bases []string
	for range 4 {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var tk, i int
			if _, err := fmt.Sscanf(r.URL.Path, "/r/%d/%d", &tk, &i); err != nil {
				http.NotFound(w, r)
				return
			}
			if (tk*31+i)%5 == 0 {
				w.Header().Set("Connection", "close")
			}
			fmt.Fprintf(w, "%d/%d", tk, i)
		}))
		srv.Config.ConnState = func(c net.Conn, s http.ConnState) {
			if s == http.StateNew {
				conns.Add(1)
				// The server's close is a reset: no TIME_WAIT entry.
				if tc, ok := c.(*net.TCPConn); ok {
					tc.SetLinger(0)
				}
			}
		}
		srv.Start()
		defer srv.Close()
		bases = append(bases, srv.URL)
	}
	for _, carriers := range []string{"1", "16"} {
		t.Run("carriers-"+carriers, func(t *testing.T) {
			before := conns.Load()
			args := append(append([]string{}, bases...), "16", "40")
			r := runCarriers(t, exe, carriers, args...)
			if r.code != 0 || !strings.Contains(r.stdout, "good 640 bad 0\n") || !strings.Contains(r.stdout, "final 4\n") {
				t.Fatalf("exit %d:\n%s%s", r.code, r.stdout, r.stderr)
			}
			if n := conns.Load() - before; n > 644 {
				t.Errorf("%d connections for 644 requests", n)
			} else {
				t.Logf("%d connections for 644 requests", n)
			}
		})
	}
}
