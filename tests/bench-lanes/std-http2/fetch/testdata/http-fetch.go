// http-fetch, Go: n GET requests through one net/http.Client (keep-alive,
// one idle connection) to a responder goroutine in the same program, over
// loopback. The responder is the Mk column's and uses no HTTP library: it
// reads a request's head to its blank line, takes the id from the target
// and writes a fixed head with a body of 16 + id % 1024 letters. Each body
// is read whole with io.ReadAll and closed. Prints the checksum: the sum
// over every reply of its status, its body's length and its first and
// last bytes. Usage: http-fetch <n>.
package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
)

type rng struct{ s uint64 }

func (g *rng) next() uint64 {
	g.s += 0x9E3779B97F4A7C15
	z := g.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

const letters = "abcdefghijklmnopqrstuvwxyz"

// headEnd is the offset just past the first CRLF CRLF in b, -1 when b
// holds none: the Mk column's HeadEnd, the same scalar loop, so the
// responder is the same code in both columns (not bytes.Index here).
func headEnd(b []byte) int {
	if len(b) < 4 {
		return -1
	}
	for i := 0; i < len(b)-3; i++ {
		if b[i] == 13 && b[i+1] == 10 && b[i+2] == 13 && b[i+3] == 10 {
			return i + 4
		}
	}
	return -1
}

// id is the decimal number after "/items/" in the request line of h: the
// Mk column's Id, the same loops.
func id(h []byte) int {
	i := 0
	for i < len(h) && h[i] != '/' {
		i++
	}
	i += 7
	v := 0
	for i < len(h) && h[i] >= '0' && h[i] <= '9' {
		v = v*10 + int(h[i]-'0')
		i++
	}
	return v
}

func respond(l net.Listener) {
	c, err := l.Accept()
	if err != nil {
		return
	}
	defer c.Close()
	rb := make([]byte, 16384)
	wb := make([]byte, 0, 2048)
	e := 0
	for {
		end := headEnd(rb[:e])
		for end < 0 {
			n, err := c.Read(rb[e:])
			if n == 0 || err != nil {
				return
			}
			e += n
			end = headEnd(rb[:e])
		}
		size := 16 + id(rb[:end])%1024
		wb = append(wb[:0], "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: "...)
		wb = strconv.AppendInt(wb, int64(size), 10)
		wb = append(wb, "\r\n\r\n"...)
		for k := 0; k < size; k++ {
			wb = append(wb, letters[k%26])
		}
		if _, err := c.Write(wb); err != nil {
			return
		}
		e = copy(rb, rb[end:e])
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: http-fetch <n>")
		os.Exit(2)
	}
	n, err := strconv.Atoi(os.Args[1])
	if err != nil || n < 1 {
		fmt.Fprintln(os.Stderr, "usage: http-fetch <n>")
		os.Exit(2)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	go respond(l)
	c := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 1, DisableCompression: true}}
	g := rng{s: 1}
	var total uint64
	for range n {
		u := fmt.Sprintf("http://127.0.0.1:%d/items/%d", port, g.next()%1000000)
		r, err := c.Get(u)
		if err != nil {
			panic(err)
		}
		b, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			panic(err)
		}
		total += uint64(r.StatusCode) + uint64(len(b))
		if len(b) > 0 {
			total += uint64(b[0]) + uint64(b[len(b)-1])
		}
	}
	fmt.Println(total)
}
