// bufwrite, Go, idiomatic std: the task of README.md with os.Create in
// os.TempDir, bufio.NewWriterSize(f, 65536), fmt.Fprintf of each line
// ("%d %x %d\n"; through the general formatter, as every column, critic
// F26), Flush and Close; then os.ReadFile, FNV-1a over its bytes, and
// os.Remove. Usage: bufwrite <n>.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: bufwrite <n>")
		os.Exit(2)
	}
	n, _ := strconv.ParseUint(os.Args[1], 10, 64)
	p := filepath.Join(os.TempDir(), "mkbench-bufwrite-go.txt")
	f, err := os.Create(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	w := bufio.NewWriterSize(f, 65536)
	for u := range n {
		h := u * 0x9e3779b97f4a7c15
		fmt.Fprintf(w, "%d %x %d\n", u, h, u*7)
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	h := uint64(0xcbf29ce484222325)
	for _, c := range b {
		h = (h ^ uint64(c)) * 0x100000001b3
	}
	total := uint64(len(b)) + h
	os.Remove(p)
	fmt.Println(total)
}
