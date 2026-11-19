// http-stream, Go: n POST requests through one net/http.Client
// (keep-alive, one idle connection), each streaming a 65,536-byte file as
// its body: chunked for an even request, with Content-Length for an odd
// one. The responder is a goroutine in the same program, the Mk column's
// code, with no HTTP library: it reads a head to its blank line, reads the
// body by the framing the head names (Content-Length, or chunked with no
// extensions or trailers), and answers with the body's byte count times
// 1,000,003 plus the sum of its bytes, in decimal. Prints the checksum:
// the sum over every reply of its status and that number. Usage:
// http-stream <n>.
package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

const size = 65536

// The responder is the Mk column's code, function for function (In, Fill,
// Line, Body, Starts, Number): the same byte loops in both columns, so the
// ratio does not depend on the fixture (no bufio.ReadSlice, bytes.Index or
// a sum through a pointer per byte here).

// in is the responder's buffered view of its connection: rb[s:e] holds
// bytes read and not yet used.
type in struct {
	rb   [16384]byte
	s, e int
}

// fill reads more bytes into rb when none are left: false at the end.
func fill(c net.Conn, b *in) bool {
	if b.s < b.e {
		return true
	}
	n, err := c.Read(b.rb[:])
	if err != nil && n == 0 {
		return false
	}
	b.s = 0
	b.e = n
	return n > 0
}

// line reads one line, its LF included, into l: its length, 0 at the end
// or for a line longer than l.
func line(c net.Conn, b *in, l *[256]byte) int {
	n := 0
	for {
		if !fill(c, b) {
			return 0
		}
		x := b.rb[b.s]
		b.s++
		if n == 256 {
			return 0
		}
		l[n] = x
		n++
		if x == 10 {
			return n
		}
	}
}

// body reads n bytes, adding them to count and sum: false at the end. Each
// piece is summed into a local, then added once, as in the Mk column.
func body(c net.Conn, b *in, n int, count, sum *uint64) bool {
	left := n
	for left > 0 {
		if !fill(c, b) {
			return false
		}
		k := min(b.e-b.s, left)
		var t uint64
		for _, x := range b.rb[b.s : b.s+k] {
			t += uint64(x)
		}
		*sum += t
		*count += uint64(k)
		b.s += k
		left -= k
	}
	return true
}

// starts: line begins with p, letters compared without case, as the Mk
// column's Starts.
func starts(line []byte, p string) bool {
	if len(line) < len(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		if line[i]|32 != p[i]|32 {
			return false
		}
	}
	return true
}

// number is the number in base (10 or 16) at the start of b.
func number(b []byte, base int) int {
	v := 0
	for _, x := range b {
		d := -1
		if x >= 48 && x <= 57 {
			d = int(x - 48)
		} else if base == 16 && x >= 97 && x <= 102 {
			d = int(x - 87)
		} else if base == 16 && x >= 65 && x <= 70 {
			d = int(x - 55)
		}
		if d < 0 {
			return v
		}
		v = v*base + d
	}
	return v
}

func respond(l net.Listener) {
	c, err := l.Accept()
	if err != nil {
		return
	}
	defer c.Close()
	b := &in{}
	var lb [256]byte
	wb := make([]byte, 0, 256)
	for {
		chunked, length := false, -1
		for {
			n := line(c, b, &lb)
			if n == 0 {
				return
			}
			if n == 2 {
				break
			}
			if starts(lb[:n], "Transfer-Encoding: chunked") {
				chunked = true
			} else if starts(lb[:n], "Content-Length: ") {
				length = number(lb[16:n], 10)
			}
		}
		var count, sum uint64
		ok := true
		if chunked {
			for ok {
				n := line(c, b, &lb)
				if n == 0 {
					ok = false
					break
				}
				k := number(lb[:n], 16)
				if k == 0 {
					ok = line(c, b, &lb) == 2
					break
				}
				ok = body(c, b, k, &count, &sum) && line(c, b, &lb) == 2
			}
		} else if length > 0 {
			ok = body(c, b, length, &count, &sum)
		}
		if !ok {
			return
		}
		v := strconv.AppendUint(nil, count*1000003+sum, 10)
		wb = append(wb[:0], "HTTP/1.1 200 OK\r\nContent-Length: "...)
		wb = strconv.AppendInt(wb, int64(len(v)), 10)
		wb = append(wb, "\r\n\r\n"...)
		wb = append(wb, v...)
		if _, err := c.Write(wb); err != nil {
			return
		}
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: http-stream <n>")
		os.Exit(2)
	}
	n, err := strconv.Atoi(os.Args[1])
	if err != nil || n < 1 {
		fmt.Fprintln(os.Stderr, "usage: http-stream <n>")
		os.Exit(2)
	}
	data := make([]byte, size)
	for i := range data {
		data[i] = byte('a' + i%26)
	}
	f, err := os.CreateTemp("", "http-stream-")
	if err != nil {
		panic(err)
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.Write(data); err != nil {
		panic(err)
	}
	f.Close()
	path, _ = filepath.Abs(path)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	go respond(l)
	c := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 1, DisableCompression: true}}
	var total uint64
	for i := range n {
		b, err := os.Open(path)
		if err != nil {
			panic(err)
		}
		// An *os.File body: net/http streams it, chunked unless the
		// request states its length.
		req, err := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/up/%d", port, i), io.Reader(b))
		if err != nil {
			panic(err)
		}
		if i%2 == 1 {
			req.ContentLength = size
		} else {
			req.ContentLength = -1
		}
		r, err := c.Do(req)
		if err != nil {
			panic(err)
		}
		v, err := io.ReadAll(r.Body)
		r.Body.Close()
		b.Close()
		if err != nil {
			panic(err)
		}
		x, _ := strconv.ParseUint(string(v), 10, 64)
		total += uint64(r.StatusCode) + x
	}
	fmt.Println(total)
}
