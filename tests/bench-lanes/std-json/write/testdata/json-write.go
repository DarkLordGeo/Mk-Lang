// json-write, Go: encoding/json decoding a 1 MB document into any once, with
// UseNumber so numbers stay text as in Mk, then an Encoder with
// SetEscapeHTML(false) writing the tree as compact JSON into one
// bytes.Buffer, reset before each, reps times (README.md). The Encoder ends
// its output with a newline, which the checksum does not count. Go writes an
// object's keys sorted; the lengths are the same. Built with GOAMD64=v2
// -pgo=off.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

func main() {
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	reps, err := strconv.Atoi(os.Args[2])
	if err != nil {
		panic(err)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		panic(err)
	}
	var buf bytes.Buffer
	var total uint64
	for range reps {
		buf.Reset()
		e := json.NewEncoder(&buf)
		e.SetEscapeHTML(false)
		if err := e.Encode(v); err != nil {
			panic(err)
		}
		total += uint64(buf.Len() - 1)
	}
	fmt.Println(total)
}
