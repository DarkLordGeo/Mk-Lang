// wordfreq, Go, idiomatic std: the task of README.md. os.ReadFile and
// utf8.Valid once; each round splits the text with strings.FieldsSeq and
// counts every word in a map[string]uint64 (the runtime's map hash:
// AES-based with AES-NI, seeded per map, so chosen keys cannot target it)
// with m[w]++, the keys substrings of the text; the map grows from empty.
// Then the checksum over the entries: sum of FNV-1a 64 of the word times
// its count, plus the number of distinct words, all wrapping. Usage:
// wordfreq <file> <rounds>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

func fnv(w string) uint64 {
	h := uint64(0xcbf29ce484222325)
	for i := 0; i < len(w); i++ {
		h = (h ^ uint64(w[i])) * 0x100000001b3
	}
	return h
}

func round(text string) uint64 {
	m := map[string]uint64{}
	for w := range strings.FieldsSeq(text) {
		m[w]++
	}
	var sum uint64
	for k, v := range m {
		sum += fnv(k) * v
	}
	return sum + uint64(len(m))
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: wordfreq <file> <rounds>")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil || !utf8.Valid(b) {
		fmt.Fprintln(os.Stderr, "wordfreq: cannot read", os.Args[1], err)
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
