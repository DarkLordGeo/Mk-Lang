// regexgroup, Go, idiomatic std: ../README.md's task with regexp (RE2
// syntax, linear in the input). The pattern ([a-z]{1,64})=([0-9]+) is
// compiled once with regexp.MustCompile; each round runs
// FindAllStringSubmatchIndex over the whole log, adding 1,000,000 plus the
// length of group 1 of every match. Usage: regexgroup <file> <rounds>.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

const pattern = `([a-z]{1,64})=([0-9]+)`

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: regexgroup <file> <rounds>")
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
		for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
			if m[2] >= 0 {
				total += 1_000_000 + int64(m[3]-m[2])
			}
		}
	}
	fmt.Println(total)
}
