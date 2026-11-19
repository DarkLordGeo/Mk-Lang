// Why: http.Fetch must read every reply form a real server sends.
// What: TestHttp_Fetch, dev and release.
// How: against Go's net/http server (Content-Length and chunked replies, headers and
// body as seen) and a raw server (an interim 100, a close-delimited reply, a bad
// status line), and URL errors with their offsets.

package std

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const htFetchMain = `import std.http

export Main(sys: &System): http.Error|WriteError!void {
    var out := sys.Stdout()
    hs: [2]http.Header = [http.Header(name: "X-One", value: "1"), http.Header(name: "X-Two", value: "a\r\nb")]
    reply := http.Fetch(sys.arena, sys.io, sys.args[1], sys.args[2], hs[..], sys.args[3].AsBytes(), 67108864) else e {
        match e {
            http.BadRequest(offset) {
                try Print(mut out, "bad {offset}\n", (offset))
            }
            http.TooLarge {
                try out.Str("large\n")
            }
            _ {
                try out.Str("other\n")
            }
        }
        try out.Flush()
        return
    }
    s := reply.status
    try Print(mut out, "status {s}\n", (s))
    for i in 0..reply.headers.Len() {
        h := reply.headers[i]
        try Print(mut out, "h {n}:{v}\n", (n: h.name, v: h.value))
    }
    try out.Str("b ")
    try out.Bytes(reply.body)
    try out.Str("\n")
    try out.Flush()
}
`

func htBuildFetch(t *testing.T, root, prof string) string {
	t.Helper()
	stdDir := filepath.Join(root, "std")
	if _, err := os.Stat(stdDir); err != nil {
		htStd(t, root, nil)
	}
	dir := filepath.Join(root, "fetch-"+prof)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(htFetchMain), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	exe := filepath.Join(dir, "fetch")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"build", "-o", exe, "main.mk"}
	if prof == "release" {
		args = []string{"build", "--release", "-o", exe, "main.mk"}
	}
	out, status := mkCmd(t, dir, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// htRawServer answers every connection with reply after reading its head,
// then closes it.
func htRawServer(t *testing.T, reply string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				if _, err := http.ReadRequest(br); err != nil {
					return
				}
				io.WriteString(c, reply)
			}()
		}
	}()
	return l.Addr().String()
}

func TestHttp_Fetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/echo":
			b, _ := io.ReadAll(r.Body)
			w.Header().Set("X-Seen", fmt.Sprintf("%s %s?%s one=%s two=%q host=%v len=%d", r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-One"), r.Header.Get("X-Two"), r.Host != "", r.ContentLength))
			w.Write(b)
		case "/chunked":
			for i := range 100 {
				fmt.Fprintf(w, "piece %d\n", i)
				w.(http.Flusher).Flush()
			}
		case "/nobody":
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	interim := htRawServer(t, "HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
	closing := htRawServer(t, "HTTP/1.0 200 OK\r\nX-A: 1\r\n\r\nuntil the end")
	broken := htRawServer(t, "HTTP/1.1 2000 OK\r\n\r\n")
	var chunked strings.Builder
	for i := range 100 {
		fmt.Fprintf(&chunked, "piece %d\n", i)
	}
	cases := []struct {
		method, url, body string
		want              []string // lines that must appear, in order
	}{
		{"POST", srv.URL + "/echo?q=1", "hello", []string{"status 200", `h X-Seen:POST /echo?q=1 one=1 two="a  b" host=true len=5`, "b hello"}},
		{"GET", srv.URL + "/echo", "", []string{"status 200", `h X-Seen:GET /echo? one=1 two="a  b" host=true len=0`, "b "}},
		{"GET", "HTTP://" + host + "?x=1", "", []string{"status 404"}},
		{"GET", srv.URL + "/chunked", "", []string{"status 200", "h Transfer-Encoding:chunked", "b " + chunked.String()}},
		{"GET", srv.URL + "/nobody", "", []string{"status 204", "b "}},
		{"HEAD", srv.URL + "/echo", "", []string{"status 200", "b "}},
		{"GET", "http://" + interim + "/", "", []string{"status 200", "b ok"}},
		{"GET", "http://" + closing + "/", "", []string{"status 200", "h X-A:1", "b until the end"}},
		{"GET", "http://" + broken + "/", "", []string{"bad 12"}},
		{"GET", "https://" + host + "/", "", []string{"bad 0"}},
		{"GET", "ftp://x/", "", []string{"bad 0"}},
		{"GET", "http:///x", "", []string{"bad 7"}},
		{"GET", "http://a b/", "", []string{"bad 8"}},
		{"GET", "http://u@h/", "", []string{"bad 8"}},
		{"GET", "http://h:x/", "", []string{"bad 9"}},
		{"GET", "http://h:/", "", []string{"bad 9"}},
		{"GET", "http://h/a b", "", []string{"bad 10"}},
		{"BAD METHOD", srv.URL, "", []string{"bad 0"}},
	}
	root := t.TempDir()
	for _, prof := range []string{"dev", "release"} {
		exe := htBuildFetch(t, root, prof)
		for _, c := range cases {
			r := runNative(t, exe, "", c.method, c.url, c.body)
			if r.code != 0 {
				t.Errorf("%s %s %s: exit %d\n%s", prof, c.method, c.url, r.code, r.stderr)
				continue
			}
			out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
			at := 0
			for _, w := range c.want {
				k := strings.Index(out[at:], w+"\n")
				if k < 0 {
					t.Errorf("%s %s %s: no %q after byte %d in\n%s", prof, c.method, c.url, w, at, out)
					break
				}
				at += k + len(w) + 1
			}
		}
	}
}
