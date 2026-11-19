// Why: a streamed request body cannot be replayed, so DoStreaming must pick
// its framing right and refuse redirects that would resend it.
// What: TestStdHttp_DoStreamingAgainstGoServer.
// How: a Mk program streams its standard input as the body (Content-Length
// when given a length, chunked otherwise); Go checks the framing and bytes.

package std

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// http2StreamProg: args are URL, mode (len, chunked, short, redir0: chunked
// with ClientLimits.redirects 0), method and a
// length (for len and short). The body is standard input. It prints
// "status N", "b BODY" and "end", or "error E"; in mode short it then
// fetches args[1] again and prints "again N".
const http2StreamProg = `import std.http
import std.url

const NoHeaders: []http.Header = []

export Main(sys: &System): http.Error|url.Error|WriteError|ParseError!void {
    var out := sys.Stdout()
    args := sys.args
    u := try url.Parse(sys.arena, args[1], none)
    mode := args[2]
    var l := http.DefaultClientLimits
    if mode == "redir0" || mode == "do0" {
        l.redirects = 0
    }
    c := http.Client.New(sys.heap, l)
    defer c.Close()
    req := http.ClientRequest(method: args[3], url: &u, headers: NoHeaders, body: "".AsBytes())
    var size: ?i64 = none
    if mode != "chunked" && mode != "redir0" {
        size = some(try args[4].ParseInt())
    }
    var arena := try Arena.New(sys.heap)
    defer arena.Free()
    var input := sys.Stdin()
    var ex := c.DoStreaming(&arena, sys.io, &req, mut input, size) else e {
        try Print(mut out, "error {e}\n", (e))
        try out.Flush()
        if mode == "short" {
            req2 := http.ClientRequest(method: "GET", url: &u, headers: NoHeaders, body: "".AsBytes())
            r := try c.Fetch(sys.arena, sys.io, &req2)
            try Print(mut out, "again {s}\n", (s: r.status))
            try out.Flush()
        }
        return
    }
    onerr ex.Abandon()
    try Print(mut out, "status {s}\n", (s: ex.status))
    try out.Str("b ")
    var rb: [0]u8 = []
    var r := ex.Body(mut rb[..])
    var piece: [512]u8 = [0; 512]
    for {
        n := try r.Read(mut piece[..])
        if n == 0 {
            break
        }
        try out.Bytes(piece[0..n])
    }
    try out.Str("\nend\n")
    try out.Flush()
    try ex.Close(sys.io)
}
`

// TestStdHttp_DoStreamingAgainstGoServer: Go's server checks the framing it
// saw (TransferEncoding, ContentLength) and the bytes. Redirects that would
// resend the body fail with BodyNotReplayable; 303, and 301 after POST, are
// followed with GET and no body. A body that ends before its stated length is
// ReadError, and the next request on the same client still works.
func TestStdHttp_DoStreamingAgainstGoServer(t *testing.T) {
	exe := http2Build(t, http2StreamProg)
	seen := &http2Seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		seen.add(r, b)
		switch r.URL.Path {
		case "/echo":
			if err != nil {
				w.WriteHeader(400)
				return
			}
			fmt.Fprintf(w, "%s te=%q len=%d n=%d sha=%x", r.Method, r.TransferEncoding, r.ContentLength, len(b), sha256.Sum256(b))
		case "/temporary":
			http.Redirect(w, r, "/echo", http.StatusTemporaryRedirect)
		case "/permanent":
			http.Redirect(w, r, "/echo", http.StatusPermanentRedirect)
		case "/see-other":
			http.Redirect(w, r, "/echo", http.StatusSeeOther)
		case "/moved":
			http.Redirect(w, r, "/echo", http.StatusMovedPermanently)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	small := "hello, streamed world"
	big := strings.Repeat("0123456789abcdef", 70000) // 1,120,000 bytes: many 16 KiB pieces
	sha := func(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
	cases := []struct {
		name, path, mode, method, length, stdin string
		want                                    []string
		saw                                     string
	}{
		{"length", "/echo", "len", "POST", fmt.Sprint(len(small)), small,
			[]string{"status 200", fmt.Sprintf(`b POST te=[] len=%d n=%d sha=%s`, len(small), len(small), sha(small)), "end"}, ""},
		{"chunked", "/echo", "chunked", "POST", "", small,
			[]string{"status 200", fmt.Sprintf(`b POST te=["chunked"] len=-1 n=%d sha=%s`, len(small), sha(small)), "end"}, ""},
		{"chunked-big", "/echo", "chunked", "PUT", "", big,
			[]string{"status 200", fmt.Sprintf(`b PUT te=["chunked"] len=-1 n=%d sha=%s`, len(big), sha(big)), "end"}, ""},
		{"length-big", "/echo", "len", "POST", fmt.Sprint(len(big)), big,
			[]string{"status 200", fmt.Sprintf(`b POST te=[] len=%d n=%d sha=%s`, len(big), len(big), sha(big)), "end"}, ""},
		{"length-prefix", "/echo", "len", "POST", "5", small,
			[]string{"status 200", fmt.Sprintf(`b POST te=[] len=5 n=5 sha=%s`, sha(small[:5])), "end"}, ""},
		{"chunked-empty", "/echo", "chunked", "POST", "", "",
			[]string{"status 200", fmt.Sprintf(`b POST te=["chunked"] len=-1 n=0 sha=%s`, sha("")), "end"}, ""},
		{"307", "/temporary", "chunked", "POST", "", small, []string{"error BodyNotReplayable"}, ""},
		{"308", "/permanent", "len", "PUT", fmt.Sprint(len(small)), small, []string{"error BodyNotReplayable"}, ""},
		{"301-put", "/moved", "len", "PUT", fmt.Sprint(len(small)), small, []string{"error BodyNotReplayable"}, ""},
		{"303", "/see-other", "chunked", "POST", "", small,
			[]string{"status 200", fmt.Sprintf(`b GET te=[] len=0 n=0 sha=%s`, sha("")), "end"}, ""},
		{"301-post", "/moved", "len", "POST", fmt.Sprint(len(small)), small,
			[]string{"status 200", fmt.Sprintf(`b GET te=[] len=0 n=0 sha=%s`, sha("")), "end"}, ""},
		{"307-empty", "/temporary", "len", "PUT", "0", "",
			[]string{"status 200", fmt.Sprintf(`b PUT te=[] len=0 n=0 sha=%s`, sha("")), "end"}, ""},
		{"short", "/echo", "short", "POST", "100", small, []string{"error ReadError", "again 200"}, ""},
		// Redirects 0 follows none and returns the 307 as it
		// is, not BodyNotReplayable or TooManyRedirects.
		{"redir0-307", "/temporary", "redir0", "POST", "", small, []string{"status 307", "end"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := runNative(t, exe, c.stdin, srv.URL+c.path, c.mode, c.method, c.length)
			http2Want(t, r.stdout+r.stderr, c.want)
		})
	}
}
