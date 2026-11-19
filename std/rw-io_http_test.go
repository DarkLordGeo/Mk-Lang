// Why: an HTTP/1.0 client cannot read chunked bodies, and a Connection
// field naming both close and keep-alive must close.
// What: TestRwIo_HttpOldClients, std.http's Serve against RFC 9112's rules.
// How: raw Go connections over loopback to htServeMain of
// std-http_serve_test.go, dev and release builds, Windows host:
//
//   - a Connection field with both close and keep-alive ends the
//     connection: the close option wins in every version (RFC 9112 9.3,
//     9.6), so the reply says Connection: close and the server closes;
//   - a handler's Transfer-Encoding: chunked is not sent to an HTTP/1.0
//     request, which cannot frame it (RFC 9112 6.1): the reply carries a
//     Content-Length and the bytes.

package std

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// rwIoRawReply sends req on a new connection and returns what the server
// sent and whether it closed the connection within 5 s.
func rwIoRawReply(t *testing.T, addr, req string) (string, bool) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, req); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(c)
	return string(b), err == nil
}

func TestRwIo_HttpOldClients(t *testing.T) {
	root := t.TempDir()
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			addr := htStartServer(t, root, htServeMain, prof)
			for _, tc := range []struct{ name, req string }{
				{"HTTP/1.0 close then keep-alive in one field", "GET /files/fixed HTTP/1.0\r\nConnection: close, keep-alive\r\n\r\n"},
				{"HTTP/1.0 keep-alive then close in two fields", "GET /files/fixed HTTP/1.0\r\nConnection: keep-alive\r\nConnection: close\r\n\r\n"},
				{"HTTP/1.1 keep-alive and close", "GET /files/fixed HTTP/1.1\r\nHost: h\r\nConnection: keep-alive, close\r\n\r\n"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					got, closed := rwIoRawReply(t, addr, tc.req)
					if !closed {
						t.Errorf("the server kept the connection open; it sent %q", got)
					}
					res, err := http.ReadResponse(bufio.NewReader(strings.NewReader(got)), nil)
					if err != nil {
						t.Fatalf("reply %q: %v", got, err)
					}
					body, _ := io.ReadAll(res.Body)
					if res.StatusCode != 200 || string(body) != "fixed\n" {
						t.Errorf("reply %d %q, want 200 \"fixed\\n\"", res.StatusCode, body)
					}
					// net/http moves the Connection field into res.Close, so
					// the raw head is read for it.
					head, _, _ := strings.Cut(got, "\r\n\r\n")
					if !res.Close || strings.Count(head, "Connection:") != 1 || !strings.Contains(head, "\r\nConnection: close") {
						t.Errorf("want exactly one Connection field, close; reply %q", got)
					}
				})
			}
			t.Run("HTTP/1.0 to a chunked handler", func(t *testing.T) {
				got, closed := rwIoRawReply(t, addr, "GET /chunks/2 HTTP/1.0\r\n\r\n")
				if !closed {
					t.Errorf("the server kept the connection open; it sent %q", got)
				}
				head, body, ok := strings.Cut(got, "\r\n\r\n")
				if !ok {
					t.Fatalf("no head in %q", got)
				}
				if strings.Contains(strings.ToLower(head), "transfer-encoding") {
					t.Errorf("a reply to HTTP/1.0 has Transfer-Encoding: %q", got)
				}
				if !strings.Contains(head, "\r\nContent-Length: 14") || body != "line 0\nline 1\n" {
					t.Errorf("reply %q, want Content-Length: 14 and the body \"line 0\\nline 1\\n\"", got)
				}
			})
			t.Run("HTTP/1.1 to a chunked handler stays chunked", func(t *testing.T) {
				got, _ := rwIoRawReply(t, addr, "GET /chunks/2 HTTP/1.1\r\nHost: h\r\nConnection: close\r\n\r\n")
				if !strings.Contains(got, "\r\nTransfer-Encoding: chunked\r\n") || !strings.HasSuffix(got, "0\r\n\r\n") {
					t.Errorf("reply %q, want chunked framing", got)
				}
			})
		})
	}
}
