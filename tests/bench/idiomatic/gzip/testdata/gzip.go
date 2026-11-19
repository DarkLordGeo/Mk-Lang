// gzip, Go, idiomatic std: the task of README.md with compress/gzip. The
// file is read once; each round makes a gzip.NewReader over it and reads
// the decompressed stream in 64 KiB chunks (the reader checks the CRC-32
// and length of the gzip trailer), adding every byte and the length to the
// checksum. Usage: gzip <file> <rounds>.
package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: gzip <file> <rounds>")
		os.Exit(2)
	}
	gz, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	rounds, _ := strconv.Atoi(os.Args[2])
	buf := make([]byte, 64<<10)
	var total uint64
	for range rounds {
		zr, err := gzip.NewReader(bytes.NewReader(gz))
		if err != nil {
			panic(err)
		}
		for {
			n, err := zr.Read(buf)
			for _, c := range buf[:n] {
				total += uint64(c)
			}
			total += uint64(n)
			if err == io.EOF {
				break
			}
			if err != nil {
				panic(err)
			}
		}
	}
	fmt.Println(total)
}
