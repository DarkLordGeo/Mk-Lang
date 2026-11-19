// keywords, Go: the keyword classifier of ../README.md, a chain of
// w == "kw" tests against 16 literals per word, as a lexer's keyword match.
// The bytes go into a []byte by a byte loop as Mk's, become one string by a
// single conversion (one copy; Go has no validation pass, where Mk's
// str.From checks UTF-8 once), and each word is text[a:b], a view (both
// offsets' bounds and the range checked; gc removes the checks it proves;
// no UTF-8 boundary check). n, rounds, the offsets, the lengths and the loop
// indices are int (64 bits, Mk's int), so i % 1000 is a signed remainder as
// in Mk; the splitmix64 state, the classes and the checksum are uint64 and
// wrap (overflow class "wraps"). Built with GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

var keywords = [16]string{"if", "else", "for", "while", "return", "break", "continue", "fn",
	"let", "var", "const", "match", "struct", "enum", "import", "export"}

// classify is 1 + the index of the keyword w equals, or 0.
func classify(w string) uint64 {
	if w == "if" {
		return 1
	}
	if w == "else" {
		return 2
	}
	if w == "for" {
		return 3
	}
	if w == "while" {
		return 4
	}
	if w == "return" {
		return 5
	}
	if w == "break" {
		return 6
	}
	if w == "continue" {
		return 7
	}
	if w == "fn" {
		return 8
	}
	if w == "let" {
		return 9
	}
	if w == "var" {
		return 10
	}
	if w == "const" {
		return 11
	}
	if w == "match" {
		return 12
	}
	if w == "struct" {
		return 13
	}
	if w == "enum" {
		return 14
	}
	if w == "import" {
		return 15
	}
	if w == "export" {
		return 16
	}
	return 0
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: keywords <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	buf := make([]byte, n*8)
	offs := make([]int, n+1)
	p := 0
	for i := 0; i < n; i++ {
		offs[i] = p
		st += 0x9e3779b97f4a7c15
		z := mix(st)
		kind := z % 4
		if kind < 2 {
			k := keywords[(z>>8)%16]
			for j := 0; j < len(k); j++ {
				buf[p] = k[j]
				p += 1
			}
			if kind == 1 {
				buf[p-1] = byte(0x61 + (z>>16)%26)
			}
		} else {
			l := 1 + int((z>>8)%8)
			for j := 0; j < l; j++ {
				st += 0x9e3779b97f4a7c15
				buf[p] = byte(0x61 + mix(st)%26)
				p += 1
			}
		}
	}
	offs[n] = p
	text := string(buf[:p])
	var total uint64
	for r := 0; r < rounds; r++ {
		for i := 0; i < n; i++ {
			total += classify(text[offs[i]:offs[i+1]]) * uint64(i%1000+1)
		}
	}
	fmt.Println(total)
}
