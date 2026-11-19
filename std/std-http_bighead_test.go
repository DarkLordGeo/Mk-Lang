// Why: http.Serve reads a head into an 8 KiB stack buffer and, past that, a
// 32 KiB arena buffer (serve.mk, HeadBuf and HeadMax); both bounds need tests.
// What: TestHttp_LargeHead against the notes service (limit 65,536), dev and release.
// How: heads of every length around both bounds, alone, pipelined with a next
// request in the same write, and followed by a body in the same write.

package std

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// htHeadOf is a GET /hello head of exactly n bytes (n >= 64), padded by
// one X-Pad field.
func htHeadOf(n int) string {
	pre := "GET /hello HTTP/1.1\r\nHost: x\r\nX-Pad: "
	return pre + strings.Repeat("p", n-len(pre)-4) + "\r\n\r\n"
}

func TestHttp_LargeHead(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "tests", "bench-lanes", "std-http", "notes", "main.mk"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			addr := htStartServer(t, root, string(src), prof)
			// one sends data on a new connection, reads count replies and
			// returns their status codes.
			one := func(data string, count int) []int {
				t.Helper()
				c, err := net.Dial("tcp", addr)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(10 * time.Second))
				go io.WriteString(c, data)
				r := bufio.NewReader(c)
				var codes []int
				for range count {
					res, err := http.ReadResponse(r, nil)
					if err != nil {
						t.Errorf("%d bytes: %v (after %v)", len(data), err, codes)
						return codes
					}
					io.Copy(io.Discard, res.Body)
					res.Body.Close()
					codes = append(codes, res.StatusCode)
				}
				return codes
			}
			for _, n := range []int{64, 8191, 8192, 8193, 8200, 16383, 16384, 16385, 24576, 32767, 32768} {
				if got := one(htHeadOf(n), 1); fmt.Sprint(got) != "[200]" {
					t.Errorf("head of %d bytes: %v", n, got)
				}
				// Pipelined: the bytes after a large head are the next request.
				if got := one(htHeadOf(n)+htHeadOf(100)+htHeadOf(n), 3); fmt.Sprint(got) != "[200 200 200]" {
					t.Errorf("pipelined heads of %d, 100, %d bytes: %v", n, n, got)
				}
			}
			for _, n := range []int{32769, 33000, 40000} {
				if got := one(htHeadOf(n), 1); fmt.Sprint(got) != "[431]" {
					t.Errorf("head of %d bytes: %v", n, got)
				}
			}
			// A large head with its body in the same write.
			body := `{"title":"big","body":"head"}`
			for _, n := range []int{8150, 8192, 9000, 30000} {
				pre := fmt.Sprintf("POST /notes HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\nX-Pad: ", len(body))
				head := pre + strings.Repeat("q", n-len(pre)-4) + "\r\n\r\n"
				if got := one(head+body+htHeadOf(200), 2); fmt.Sprint(got) != "[201 200]" {
					t.Errorf("POST with a head of %d bytes: %v", n, got)
				}
			}
			// Empty lines before a large head are skipped (RFC 9112 2.2).
			if got := one("\r\n\r\n"+htHeadOf(12000), 1); fmt.Sprint(got) != "[200]" {
				t.Errorf("empty lines, then a head of 12000 bytes: %v", got)
			}
			// A bare LF past the stack buffer is still refused.
			bad := htHeadOf(12000)
			bad = bad[:10000] + "\n" + bad[10001:]
			if got := one(bad, 1); fmt.Sprint(got) != "[400]" {
				t.Errorf("bare LF at 10000: %v", got)
			}
		})
	}
}
