// log, Go: the standard library's log.Logger with the prefix "INFO " and no
// flags, Printf of n lines of two integer holes per rep into one
// bytes.Buffer, reset before each rep, reps times (README.md). Built with
// GOAMD64=v2 -pgo=off.
package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strconv"
)

func main() {
	n, err := strconv.Atoi(os.Args[1])
	if err != nil {
		panic(err)
	}
	reps, err := strconv.Atoi(os.Args[2])
	if err != nil {
		panic(err)
	}
	var buf bytes.Buffer
	l := log.New(&buf, "INFO ", 0)
	var total uint64
	for r := 0; r < reps; r++ {
		buf.Reset()
		for k := 0; k < n; k++ {
			l.Printf("event %d took %d us", k, (k*7)%1000)
		}
		total += uint64(buf.Len())
	}
	fmt.Println(total)
}
