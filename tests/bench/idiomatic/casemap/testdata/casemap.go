// casemap, Go, idiomatic std: the task of README.md with strings.ToLower
// (unicode.ToLower per rune, one scalar to one), on each line of the
// generated mixed-script text (strings.SplitSeq at "\n"; the empty piece
// after the last newline adds nothing), each result left to the garbage
// collector. The text is built once with strings.Builder.WriteRune from
// splitmix64. Usage: casemap <kib> <rounds> <seed>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

var ranges = [11][2]rune{{0x41, 0x5A}, {0x61, 0x7A}, {0xC0, 0xD6}, {0xD8, 0xDE}, {0xE0, 0xF6}, {0xF8, 0xFE}, {0x391, 0x3A1}, {0x3A4, 0x3A9}, {0x3B1, 0x3C1}, {0x3C4, 0x3C9}, {0x410, 0x44F}}

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func gen(bytes int, seed uint64) string {
	var tab []rune
	for _, r := range ranges {
		for c := r[0]; c <= r[1]; c++ {
			tab = append(tab, c)
		}
	}
	var t strings.Builder
	st := seed
	for t.Len() < bytes {
		st += 0x9e3779b97f4a7c15
		letters := int(mix(st)%12) + 1
		for range letters {
			st += 0x9e3779b97f4a7c15
			t.WriteRune(tab[mix(st)%uint64(len(tab))])
		}
		st += 0x9e3779b97f4a7c15
		if mix(st)%12 == 0 {
			t.WriteByte('\n')
		} else {
			t.WriteByte(' ')
		}
	}
	t.WriteByte('\n')
	return t.String()
}

func round(text string) uint64 {
	h := uint64(0xcbf29ce484222325)
	var n uint64
	for line := range strings.SplitSeq(text, "\n") {
		lo := strings.ToLower(line)
		for i := 0; i < len(lo); i++ {
			h = (h ^ uint64(lo[i])) * 0x100000001b3
		}
		n += uint64(len(lo))
	}
	return h + n
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: casemap <kib> <rounds> <seed>")
		os.Exit(2)
	}
	kib, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	seed, _ := strconv.ParseUint(os.Args[3], 10, 64)
	text := gen(kib*1024, seed)
	var total uint64
	for range rounds {
		total += round(text)
	}
	fmt.Println(total)
}
