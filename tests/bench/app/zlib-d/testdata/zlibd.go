// zlib-d, Go, std: the task of README.md with compress/zlib's reader (the
// zlib framing of RFC 1950; it checks the trailer's Adler-32 and returns
// zlib.ErrChecksum on a mismatch). The compressed file is read through a
// bufio.Reader of 64 KiB, the decompressed stream is read in 64 KiB chunks,
// and each chunk is written to the output file. The program prints the
// count of decompressed bytes.
// Usage: zlibd <in> <out>.
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
		fmt.Fprintln(os.Stderr, "usage: zlibd <in> <out>")
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
	zr, err := zlib.NewReader(bufio.NewReaderSize(src, 64<<10))
	if err != nil {
		die(err)
	}
	buf := make([]byte, 64<<10)
	var total int64
	for {
		n, err := zr.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
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
	if err := zr.Close(); err != nil {
		die(err)
	}
	if err := dst.Close(); err != nil {
		die(err)
	}
	src.Close()
	fmt.Println(total)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "zlibd:", err)
	os.Exit(1)
}
