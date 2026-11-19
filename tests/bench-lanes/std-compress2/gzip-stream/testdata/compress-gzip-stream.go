// compress-gzip-stream, Go, idiomatic std: one compress/gzip.Writer at the
// default level, Reset each round onto a bufio.Writer of 64 KiB over the
// null device (the path argument), takes a 50 MB log text in 64 KiB
// writes and is closed (the final block and the trailer), rounds times.
// The checksum adds every input byte and the input length once per round.
// Usage: compress-gzip-stream <file> <out> <rounds>.
package main

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: compress-gzip-stream <file> <out> <rounds>")
		os.Exit(2)
	}
	text, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	rounds, _ := strconv.Atoi(os.Args[3])
	var sum uint64
	for _, c := range text {
		sum += uint64(c)
	}
	sum += uint64(len(text))
	f, err := os.Create(os.Args[2])
	if err != nil {
		panic(err)
	}
	bw := bufio.NewWriterSize(f, 64<<10)
	zw := gzip.NewWriter(bw)
	var total uint64
	for range rounds {
		zw.Reset(bw)
		for p := 0; p < len(text); p += 64 << 10 {
			if _, err := zw.Write(text[p:min(p+64<<10, len(text))]); err != nil {
				panic(err)
			}
		}
		if err := zw.Close(); err != nil {
			panic(err)
		}
		total += sum
	}
	if err := bw.Flush(); err != nil {
		panic(err)
	}
	f.Close()
	fmt.Println(total)
}
