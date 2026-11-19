// Why: a client reads bytes a hostile server chooses; it must never panic or
// hang on them.
// What: FuzzStdHttp_ClientReply, any reply bytes through Client.Fetch built
// with --release; seeds are Go's replies and tests_target.mk's smuggling shapes.
// How: each input's listener answers once, waits for the client's close and
// resets (linger 0), so no TIME_WAIT is left. go test -fuzz explores.
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"bufio"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// http2ReplyServer answers one connection with reply and returns its URL.
func http2ReplyServer(t *testing.T, reply []byte) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		tc := c.(*net.TCPConn)
		defer func() {
			tc.SetLinger(0)
			tc.Close()
		}()
		tc.SetDeadline(time.Now().Add(20 * time.Second))
		if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
			return
		}
		c.Write(reply)
		// No FIN from this side: the client closes first (its FIN_WAIT_2
		// then gets this reset, and neither side keeps a TIME_WAIT entry),
		// or after 300 ms the reset ends a reply delimited by the close.
		tc.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		var b [512]byte
		for {
			if _, err := c.Read(b[:]); err != nil {
				return
			}
		}
	}()
	return "http://" + l.Addr().String() + "/"
}

// FuzzStdHttp_ClientReply: any bytes a server sends as its reply (status
// line, fields, framing, chunked body, interim replies) go through
// Client.Fetch, built with --release, over loopback. The client must never
// panic or hang: it prints a status and a body, or one error, and exits 0.
// Each input's listener answers one connection with the bytes, waits for the
// client's close and resets the connection (linger 0), never sending a FIN of
// its own (a listener that half-closed after the reply left one TIME_WAIT
// entry per input). go test runs the seeds; go test -fuzz
// FuzzStdHttp_ClientReply -parallel 2 explores (at most 5 minutes a run).
func FuzzStdHttp_ClientReply(f *testing.F) {
	for _, s := range []string{
		"HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5;ext=1\r\nhello\r\n0\r\nTrailer: x\r\n\r\n",
		"HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 204 No Content\r\n\r\n",
		"HTTP/1.0 200 OK\r\n\r\nuntil the end",
		"HTTP/1.1 302 Found\r\nLocation: /\r\nContent-Length: 0\r\n\r\n",
		"HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nshort",
		"HTTP/1.1 200 OK\r\nContent-Length: 3\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n",
		"HTTP/1.1 200 OK\r\nContent-Length: 3\r\nContent-Length: 4\r\n\r\nabcd",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nfffffffffffffffffff\r\n",
		"HTTP/1.1 200 OK\r\nX: a\r\n b\r\n\r\n",
		"HTTP/1.1 200 OK\nContent-Length: 0\n\n",
		"HTTP/1.1 999 \r\nContent-Encoding: gzip\r\nContent-Length: 2\r\n\r\n\x1f\x8b",
	} {
		f.Add([]byte(s))
	}
	// The program is built once, into the target's own directory: a seed's
	// TempDir is removed when that seed ends.
	dir := f.TempDir()
	var once sync.Once
	var exe string
	f.Fuzz(func(t *testing.T, reply []byte) {
		once.Do(func() {
			built := http2Build(t, http2ClientProg)
			b, err := os.ReadFile(built)
			if err != nil {
				t.Fatal(err)
			}
			exe = filepath.Join(dir, filepath.Base(built))
			if err := os.WriteFile(exe, b, 0o755); err != nil {
				t.Fatal(err)
			}
		})
		if exe == "" {
			// A fuzz worker builds the program when it starts; a build
			// that failed (a std file mid-edit) is no finding.
			t.Skip("the client did not build")
		}
		if len(reply) > 1<<16 {
			reply = reply[:1<<16]
		}
		u := http2ReplyServer(t, reply)
		r := runNative(t, exe, "", u, "fuzz", "GET", "", "-")
		if r.code != 0 {
			t.Fatalf("exit %d on %q\n%s%s", r.code, reply, r.stdout, r.stderr)
		}
		out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
		if !strings.HasPrefix(out, "status ") && !strings.HasPrefix(out, "error ") {
			t.Fatalf("neither a status nor an error on %q:\n%s", reply, out)
		}
	})
}
