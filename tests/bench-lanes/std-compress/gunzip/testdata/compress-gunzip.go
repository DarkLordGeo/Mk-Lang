// compress-gunzip, Go, idiomatic std, one-shot shape: compress/gzip into
// one output buffer. Go's std has no one-shot gunzip, so the program reads
// the gzip.Reader into one buffer of the trailer's ISIZE plus one byte (the
// byte lets the last Read see io.EOF, by which the reader has checked the
// trailer's CRC-32 and length), made once before the loop, with the one
// gzip.Reader Reset each round. Each round then adds every decompressed
// byte and the length to the checksum. Go's reader still copies every byte
// from its dictionary window into the buffer: its std has no other form.
// Usage: compress-gunzip <file> <rounds>.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: compress-gunzip <file> <rounds>")
		os.Exit(2)
	}
	gz, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	rounds, _ := strconv.Atoi(os.Args[2])
	if len(gz) < 18 {
		panic("not a gzip file")
	}
	out := make([]byte, int(binary.LittleEndian.Uint32(gz[len(gz)-4:]))+1)
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		panic(err)
	}
	var total uint64
	for range rounds {
		if err := zr.Reset(bytes.NewReader(gz)); err != nil {
			panic(err)
		}
		n := 0
		for {
			if n == len(out) {
				panic("output longer than the trailer's ISIZE")
			}
			k, err := zr.Read(out[n:])
			n += k
			if err == io.EOF {
				break
			}
			if err != nil {
				panic(err)
			}
		}
		for _, c := range out[:n] {
			total += uint64(c)
		}
		total += uint64(n)
	}
	fmt.Println(total)
}
