// read, Go: read a file through bufio.Reader (64 KiB) over an os.File by
// Read calls of want bytes each, r times; prints the bytes read and the
// wrapping sum of each call's first byte. A want of at least the buffer
// reads straight into the caller's slice (bufio's rule). Usage: read
// <file> <want> <r>.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
)

func pass(p string, d []byte) (bytes int64, sum uint64) {
	f, err := os.Open(p)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 65536)
	for {
		k, err := r.Read(d)
		if k > 0 {
			bytes += int64(k)
			sum += uint64(d[0])
		}
		if err == io.EOF {
			return
		}
		if err != nil {
			panic(err)
		}
	}
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: read <file> <want> <r>")
		os.Exit(2)
	}
	want, _ := strconv.Atoi(os.Args[2])
	reps, _ := strconv.Atoi(os.Args[3])
	d := make([]byte, want)
	var bytes int64
	var sum uint64
	for range reps {
		a, b := pass(os.Args[1], d)
		bytes += a
		sum += b
	}
	fmt.Println(bytes, sum)
}
