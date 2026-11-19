// regexunicode, Go, idiomatic std: ../README.md's task with regexp (RE2
// syntax, linear in the input). The patterns \pL+, \w+\s\pL and
// \p{Lu}\pL* are compiled once with regexp.MustCompile; each round runs
// FindAllStringIndex of each over the whole log, adding 1,000,000 plus
// the length of every match. Usage: regexunicode <file> <rounds>.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

var patterns = []string{`\pL+`, `\w+\s\pL`, `\p{Lu}\pL*`}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: regexunicode <file> <rounds>")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	text := string(b)
	rounds, _ := strconv.Atoi(os.Args[2])
	var res []*regexp.Regexp
	for _, p := range patterns {
		res = append(res, regexp.MustCompile(p))
	}
	var total int64
	for range rounds {
		for _, re := range res {
			for _, m := range re.FindAllStringIndex(text, -1) {
				total += 1_000_000 + int64(m[1]-m[0])
			}
		}
	}
	fmt.Println(total)
}
