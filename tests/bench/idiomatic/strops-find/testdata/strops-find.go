// strops-find, Go, idiomatic std: the task of README.md with
// strings.Index on line[p:], over the lines of strings.SplitSeq(text,
// "\n"). The file is read once with os.ReadFile and checked by
// utf8.Valid. Usage: strops-find <file> <rounds>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

var needles = [8]string{"the", "ing", "qzx", "e a", "tion", "s an d", "abcdefgh", "xylophone quartz"}

func round(text string) uint64 {
	var total uint64
	for line := range strings.SplitSeq(text, "\n") {
		for _, nd := range needles {
			p := 0
			for {
				q := strings.Index(line[p:], nd)
				if q < 0 {
					break
				}
				at := p + q
				total += uint64(at) + 1
				p = at + len(nd)
			}
		}
	}
	return total
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: strops-find <file> <rounds>")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil || !utf8.Valid(b) {
		fmt.Fprintln(os.Stderr, "strops-find: cannot read", os.Args[1], err)
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
