// regex, Go, idiomatic std: the task of README.md with regexp (RE2 syntax;
// a one-pass, backtracking or NFA matcher chosen per pattern, linear in the
// input). The pattern is compiled once with regexp.MustCompile; each round
// splits the log with strings.Lines and runs FindStringIndex on every
// line, counting the matches and adding their lengths. Usage: regex <file>
// <rounds>.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const pattern = `status=5[0-9][0-9] dur=[0-9]{4,}ms`

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: regex <file> <rounds>")
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
			if loc := re.FindStringIndex(line); loc != nil {
				total += 1_000_000 + int64(loc[1]-loc[0])
			}
		}
	}
	fmt.Println(total)
}
