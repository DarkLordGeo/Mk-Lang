// json-codec-write, Go: encoding/json Unmarshal of a 1 MB document into a
// slice of structs once, then an Encoder with SetEscapeHTML(false) writing
// the slice as compact JSON into one bytes.Buffer, reset before each, reps
// times (README.md). The checksum is reps times the count of the bytes
// { } [ ] : , and " in the last output, which the newline Encode adds does
// not change. The struct types are the four levels of the Mk column (R3 has
// no Next; its "next", always null, is an unknown member, ignored on the
// read). Built with GOAMD64=v2 -pgo=off.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

type R3 struct {
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Score float64  `json:"score"`
	Tags  []string `json:"tags"`
	OK    bool     `json:"ok"`
}

type R2 struct {
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Score float64  `json:"score"`
	Tags  []string `json:"tags"`
	OK    bool     `json:"ok"`
	Next  *R3      `json:"next"`
}

type R1 struct {
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Score float64  `json:"score"`
	Tags  []string `json:"tags"`
	OK    bool     `json:"ok"`
	Next  *R2      `json:"next"`
}

type R0 struct {
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Score float64  `json:"score"`
	Tags  []string `json:"tags"`
	OK    bool     `json:"ok"`
	Next  *R1      `json:"next"`
}

// structural counts the bytes of b that are { } [ ] : , or ".
func structural(b []byte) uint64 {
	var n uint64
	for _, c := range b {
		switch c {
		case '{', '}', '[', ']', ':', ',', '"':
			n++
		}
	}
	return n
}

func main() {
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	reps, err := strconv.Atoi(os.Args[2])
	if err != nil {
		panic(err)
	}
	var rs []R0
	if err := json.Unmarshal(b, &rs); err != nil {
		panic(err)
	}
	var buf bytes.Buffer
	for range reps {
		buf.Reset()
		e := json.NewEncoder(&buf)
		e.SetEscapeHTML(false)
		if err := e.Encode(rs); err != nil {
			panic(err)
		}
	}
	fmt.Println(structural(buf.Bytes()) * uint64(reps))
}
