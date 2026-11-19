// strops-split, Go, idiomatic std: the task of README.md with
// strings.SplitSeq(text, "e") (empty pieces kept), strings.TrimSpace on
// each piece, and strings.FieldsSeq over the text. The file is read once
// with os.ReadFile and checked by utf8.Valid. Usage: strops-split <file>
// <rounds>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

func round(text string) uint64 {
	var total uint64
	h := uint64(0xcbf29ce484222325)
	for piece := range strings.SplitSeq(text, "e") {
		t := strings.TrimSpace(piece)
		total++
		h = (h ^ uint64(len(t))) * 0x100000001b3
	}
	total += h
	for w := range strings.FieldsSeq(text) {
		total += uint64(len(w)) + 1
	}
	return total
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: strops-split <file> <rounds>")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil || !utf8.Valid(b) {
		fmt.Fprintln(os.Stderr, "strops-split: cannot read", os.Args[1], err)
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
