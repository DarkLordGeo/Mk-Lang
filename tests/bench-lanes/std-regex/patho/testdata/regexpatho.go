// regexpatho, Go, idiomatic std: ../README.md's task with regexp (RE2
// syntax, linear in the input; for this pattern and input size its NFA
// simulation). The pattern (a|aa)*c is compiled once with
// regexp.MustCompile; each round runs FindStringIndex on n bytes of 'a'
// (no match) and on the same bytes with a final 'c'. Usage: regexpatho <n>
// <rounds>. Prints the checksum: per round, n plus the second match's
// length.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const pattern = `(a|aa)*c`

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: regexpatho <n> <rounds>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	all := strings.Repeat("a", n) + "c"
	as := all[:n]
	re := regexp.MustCompile(pattern)
	var total int64
	for range rounds {
		if m := re.FindStringIndex(as); m != nil {
			total -= int64(m[1] - m[0] + 1)
		}
		if m := re.FindStringIndex(all); m != nil {
			total += int64(n) + int64(m[1]-m[0])
		}
	}
	fmt.Println(total)
}
