// encoding-utf8valid, Go, idiomatic std: the line of README.md repeated n
// times, made valid rounds times by bytes.ToValidUTF8 with U+FFFD, which
// returns a new slice (the input is valid, so it is a copy). The checksum
// adds, per round, the text's length and one byte of it at a moving index.
// Usage: encoding-utf8valid <n> <rounds>.
package main

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
)

const line = "Hello, wörld! Привет мир. 水は冷たい \U0001F600\n"

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: encoding-utf8valid <n> <rounds>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	b := bytes.Repeat([]byte(line), n)
	rep := []byte("\uFFFD")
	var total uint64
	for r := 0; r < rounds; r++ {
		out := bytes.ToValidUTF8(b, rep)
		total += uint64(len(out)) + uint64(out[(r*7919)%len(out)])
	}
	fmt.Println(total)
}
