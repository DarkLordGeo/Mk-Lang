// compress-gzip, Go, idiomatic std: compress/gzip at DefaultCompression
// (level 6) of a 50 MB log text, rounds times. One bytes.Buffer, grown once
// before the loop to the size of the Zig column's output buffer, and one
// gzip.Writer, Reset onto the buffer each round (SC-2: the writer's reuse
// form), with one Write and Close. The checksum adds every input byte and
// the input length once per round; the compressed size goes to stderr.
// Usage: compress-gzip <file> <rounds>.
package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: compress-gzip <file> <rounds>")
		os.Exit(2)
	}
	text, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	rounds, _ := strconv.Atoi(os.Args[2])
	var buf bytes.Buffer
	buf.Grow(len(text) + len(text)>>4 + 1<<20)
	zw, err := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
	if err != nil {
		panic(err)
	}
	var total uint64
	size := 0
	for range rounds {
		buf.Reset()
		zw.Reset(&buf)
		if _, err := zw.Write(text); err != nil {
			panic(err)
		}
		if err := zw.Close(); err != nil {
			panic(err)
		}
		size = buf.Len()
		for _, c := range text {
			total += uint64(c)
		}
		total += uint64(len(text))
	}
	fmt.Println(total)
	fmt.Fprintf(os.Stderr, "compressed %d\n", size)
}
