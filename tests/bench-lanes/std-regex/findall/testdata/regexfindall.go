// regexfindall, Go, idiomatic std: ../README.md's task with regexp (RE2
// syntax, linear in the input). The pattern [a-z]+=5[0-9][0-9] has no
// literal prefix, so regexp cannot skip to a fixed string. The pattern is
// compiled once with regexp.MustCompile; each round runs
// FindAllStringIndex over the whole log, adding 1,000,000 plus the length
// of every match. Usage: regexfindall <file> <rounds>.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

const pattern = `[a-z]+=5[0-9][0-9]`

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: regexfindall <file> <rounds>")
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
		for _, m := range re.FindAllStringIndex(text, -1) {
			total += 1_000_000 + int64(m[1]-m[0])
		}
	}
	fmt.Println(total)
}
