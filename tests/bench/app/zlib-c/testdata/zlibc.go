// zlib-c, Go, std: the task of README.md with compress/zlib at level 6
// (zlib.NewWriterLevel, the zlib framing of RFC 1950). The input file is
// read in 64 KiB chunks, each chunk is written to the zlib writer, which
// writes through a bufio.Writer of 64 KiB over the output file, and Close
// writes the final block and the Adler-32 trailer. The program prints the
// count of input bytes.
// Usage: zlibc <in> <out>.
package main

import (
	"bufio"
	"compress/zlib"
	"fmt"
	"io"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: zlibc <in> <out>")
		os.Exit(2)
	}
	src, err := os.Open(os.Args[1])
	if err != nil {
		die(err)
	}
	dst, err := os.Create(os.Args[2])
	if err != nil {
		die(err)
	}
	w := bufio.NewWriterSize(dst, 64<<10)
	zw, err := zlib.NewWriterLevel(w, 6)
	if err != nil {
		die(err)
	}
	buf := make([]byte, 64<<10)
	var total int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := zw.Write(buf[:n]); werr != nil {
				die(werr)
			}
			total += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			die(err)
		}
	}
	if err := zw.Close(); err != nil {
		die(err)
	}
	if err := w.Flush(); err != nil {
		die(err)
	}
	if err := dst.Close(); err != nil {
		die(err)
	}
	src.Close()
	fmt.Println(total)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "zlibc:", err)
	os.Exit(1)
}
