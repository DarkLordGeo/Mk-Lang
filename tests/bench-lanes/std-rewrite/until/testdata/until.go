// until, Go: split a file into records through bufio.Reader (64 KiB) over
// an os.File with ReadSlice (no record reaches the buffer's size), once on
// '\n' and once on ' ', r times; prints the records and the bytes taken
// (each record with its delimiter). Usage: until <file> <r>.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
)

func split(p string, delim byte) (n, bytes int64) {
	f, err := os.Open(p)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 65536)
	for {
		s, err := r.ReadSlice(delim)
		if len(s) > 0 {
			n++
			bytes += int64(len(s))
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
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: until <file> <r>")
		os.Exit(2)
	}
	reps, _ := strconv.Atoi(os.Args[2])
	var n, bytes int64
	for range reps {
		for _, d := range []byte{'\n', ' '} {
			a, b := split(os.Args[1], d)
			n += a
			bytes += b
		}
	}
	fmt.Println(n, bytes)
}
