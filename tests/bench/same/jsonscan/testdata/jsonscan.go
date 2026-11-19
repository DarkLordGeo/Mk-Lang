// jsonscan, Go: the reference algorithm of ../README.md.
// A []byte from os.ReadFile, indexed with b[i] (bounds checked; gc removes
// the checks it proves). The token hash is uint64 and wraps (Go's integers
// wrap), as the overflow class "wraps" says. The if chain is in the README's
// order; no switch. Built with GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func scan(b []byte, h0 uint64) uint64 {
	h := h0
	n := len(b)
	i := 0
	for i < n {
		c := b[i]
		if c == ' ' || c == '\n' || c == '\r' || c == '\t' {
			i++
		} else if c == '"' {
			i++
			l := 0
			for i < n && b[i] != '"' {
				if b[i] == '\\' {
					i++
				}
				i++
				l++
			}
			i++
			h = h*31 + uint64(l)
		} else if c == '-' || (c >= '0' && c <= '9') {
			var neg uint64
			if c == '-' {
				neg = 1
				i++
			}
			var v uint64
			for i < n && ((b[i] >= '0' && b[i] <= '9') || b[i] == '.') {
				if b[i] != '.' {
					v = v*10 + uint64(b[i]-'0')
				}
				i++
			}
			h = h*31 + (v*2 + neg)
		} else if c == 't' {
			h = h*31 + 1
			i += 4
		} else if c == 'f' {
			h = h*31 + 2
			i += 5
		} else if c == 'n' {
			h = h*31 + 3
			i += 4
		} else {
			h = h*31 + uint64(c)
			i++
		}
	}
	return h
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: jsonscan <file> <rounds>")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	rounds, _ := strconv.Atoi(os.Args[2])
	var total uint64
	for r := 0; r < rounds; r++ {
		total = scan(b, total)
	}
	fmt.Println(total)
}
