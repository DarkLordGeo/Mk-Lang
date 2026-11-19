// regexreplace, Go, idiomatic std: ../README.md's task with regexp (RE2
// syntax, linear in the input). The pattern is compiled once with
// regexp.MustCompile; each round runs ReplaceAllString of every match of
// (svc|req)=([a-z0-9]+) in the whole log by $2:$1, adding the output's
// length and a sample of its bytes (every 4,096th byte, weighted by its
// position) to the checksum. Usage: regexreplace <file> <rounds>.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

const (
	pattern  = `(svc|req)=([a-z0-9]+)`
	template = `$2:$1`
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: regexreplace <file> <rounds>")
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
		out := re.ReplaceAllString(text, template)
		total += int64(len(out))
		for i := 0; i < len(out); i += 4096 {
			total += int64(out[i]) * int64(i/4096%251+1)
		}
	}
	fmt.Println(total)
}
