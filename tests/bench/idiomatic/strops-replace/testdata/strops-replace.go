// strops-replace, Go, idiomatic std: the task of README.md with
// strings.ReplaceAll (non-overlapping, left to right; a new string when
// anything matches, the input itself, unallocated, when nothing does), three
// in turn on each line of strings.SplitSeq at "\n". The file
// is read once with os.ReadFile and checked by utf8.Valid. Usage:
// strops-replace <file> <rounds>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

func round(text string) uint64 {
	h := uint64(0xcbf29ce484222325)
	var n uint64
	for line := range strings.SplitSeq(text, "\n") {
		a := strings.ReplaceAll(line, "e", "EEE")
		b := strings.ReplaceAll(a, "s ", "S")
		c := strings.ReplaceAll(b, " a", "_A")
		for i := 0; i < len(c); i++ {
			h = (h ^ uint64(c[i])) * 0x100000001b3
		}
		n += uint64(len(c))
	}
	return h + n
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: strops-replace <file> <rounds>")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil || !utf8.Valid(b) {
		fmt.Fprintln(os.Stderr, "strops-replace: cannot read", os.Args[1], err)
		os.Exit(1)
	}
	text := string(b)
	rounds, _ := strconv.Atoi(os.Args[2])
	var total uint64
	for range rounds {
		total += round(text)
	}
	fmt.Println(total)
}
