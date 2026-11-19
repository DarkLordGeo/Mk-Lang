// compress-adler32, Go, idiomatic std: hash/adler32.Checksum of a 50 MB log
// text, rounds times, round r over the text from byte r % 64 on (no cell
// writes to its buffer). The checksum adds the rounds' values.
// Usage: compress-adler32 <file> <rounds>.
package main

import (
	"fmt"
	"hash/adler32"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: compress-adler32 <file> <rounds>")
		os.Exit(2)
	}
	text, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	rounds, _ := strconv.Atoi(os.Args[2])
	var total uint64
	for r := range rounds {
		total += uint64(adler32.Checksum(text[r%64:]))
	}
	fmt.Println(total)
}
