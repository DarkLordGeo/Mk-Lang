// regexnoprefix, Go, idiomatic std: ../README.md's task with regexp (RE2
// syntax, linear in the input). The pattern [a-z]+=5[0-9]{2}
// [a-z]+=[0-9]{4,}ms has no literal prefix. It is compiled once with
// regexp.MustCompile; each round splits the log with strings.Lines,
// strips each line's \n or \r\n as Mk's str.Lines does, and runs
// FindStringIndex on every line, adding 1,000,000 plus the match's
// length. Usage: regexnoprefix <file> <rounds>.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const pattern = `[a-z]+=5[0-9]{2} [a-z]+=[0-9]{4,}ms`

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: regexnoprefix <file> <rounds>")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	text := string(b)
	rounds, _ := strconv.Atoi(os.Args[2])
	re := regexp.MustCompile(pattern)
	var total int64
	for range rounds {
		for line := range strings.Lines(text) {
			// Mk's str.Lines strips the line's end, so Go's line does too
			// and both engines read the same bytes (RX-5).
			if strings.HasSuffix(line, "\n") {
				line = strings.TrimSuffix(line[:len(line)-1], "\r")
			}
			if loc := re.FindStringIndex(line); loc != nil {
				total += 1_000_000 + int64(loc[1]-loc[0])
			}
		}
	}
	fmt.Println(total)
}
