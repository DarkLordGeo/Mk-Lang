// http-parse, Go: net/http.ReadRequest over a bufio.Reader of 4,096 bytes
// on a loopback TCP stream, the same n requests as the Mk column (a
// splitmix64 stream, README.md), reps times, in batches of at most 32,768
// bytes written and then read by the one goroutine; every body read to its
// end. Prints the checksum: the sum over every request of its method, path,
// query, body and field lengths plus its field count. Built with
// GOAMD64=v2 -pgo=off. Usage: http-parse <n> <reps>.
package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
)

type rng struct{ s uint64 }

func (r *rng) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// build returns the n requests back to back and the offset of each, with
// the end last.
func build(n int) ([]byte, []int) {
	var b []byte
	starts := make([]int, 0, n+1)
	g := rng{s: 1}
	for range n {
		starts = append(starts, len(b))
		r := g.next()
		post := r%4 == 0
		method := "GET"
		if post {
			method = "POST"
		}
		b = fmt.Appendf(b, "%s /api/v1/items/%d", method, (r>>8)%1000000)
		if (r>>40)&1 == 1 {
			b = fmt.Appendf(b, "?page=%d&sort=asc", (r>>41)%100)
		}
		b = append(b, " HTTP/1.1\r\nHost: bench.example.com\r\n"+
			"User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36\r\n"+
			"Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8\r\n"+
			"Accept-Language: en-US,en;q=0.9\r\n"+
			"Accept-Encoding: gzip, deflate, br\r\n"...)
		b = fmt.Appendf(b, "Cookie: session=%d\r\nConnection: keep-alive\r\n", g.next())
		if post {
			l := 16 + int((r>>20)%240)
			b = fmt.Appendf(b, "Content-Type: application/json\r\nContent-Length: %d\r\n\r\n", l)
			for k := range l {
				b = append(b, byte('a'+k%26))
			}
		} else {
			b = append(b, "\r\n"...)
		}
	}
	starts = append(starts, len(b))
	return b, starts
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: http-parse <n> <reps>")
		os.Exit(2)
	}
	n, err1 := strconv.Atoi(os.Args[1])
	reps, err2 := strconv.Atoi(os.Args[2])
	if err1 != nil || err2 != nil || n < 1 {
		fmt.Fprintln(os.Stderr, "usage: http-parse <n> <reps>")
		os.Exit(2)
	}
	data, starts := build(n)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	out, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		panic(err)
	}
	in, err := ln.Accept()
	if err != nil {
		panic(err)
	}
	br := bufio.NewReaderSize(in, 4096)
	var body [4096]byte
	var total uint64
	for range reps {
		for i := 0; i < n; {
			j := i
			for j < n && starts[j+1]-starts[i] <= 32768 {
				j++
			}
			if _, err := out.Write(data[starts[i]:starts[j]]); err != nil {
				panic(err)
			}
			for range j - i {
				req, err := http.ReadRequest(br)
				if err != nil {
					panic(err)
				}
				got, err := io.ReadFull(req.Body, body[:req.ContentLength])
				if err != nil {
					panic(err)
				}
				s := len(req.Method) + len(req.URL.Path) + len(req.URL.RawQuery) + got
				fields := 0
				for k, vs := range req.Header {
					for _, v := range vs {
						s += len(k) + len(v)
						fields++
					}
				}
				if _, ok := req.Header["Host"]; !ok {
					s += len("Host") + len(req.Host)
					fields++
				}
				total += uint64(s + fields)
			}
			i = j
		}
	}
	out.Close()
	in.Close()
	ln.Close()
	fmt.Println(total)
}
