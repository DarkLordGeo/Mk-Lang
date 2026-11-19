// json, Go, idiomatic std: the task of README.md with encoding/json. Each
// round: a Decoder with UseNumber (numbers kept as text, as in Mk) decodes
// the document into any, json.Marshal serialises the tree (compact; map
// keys sorted, <, > and & escaped, which the parse undoes), a second
// Decoder with UseNumber decodes that, and the second tree is walked.
// Usage: json <file> <rounds>.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

func walk(v any) int64 {
	switch x := v.(type) {
	case nil:
		return 7
	case bool:
		if x {
			return 3
		}
		return 5
	case json.Number:
		f, err := strconv.ParseFloat(string(x), 64)
		if err != nil {
			return 0
		}
		return 1 + int64(f)
	case string:
		return 1 + int64(len(x))
	case []any:
		n := int64(1)
		for _, e := range x {
			n += walk(e)
		}
		return n
	case map[string]any:
		n := int64(1)
		for k, e := range x {
			n += 1 + int64(len(k)) + walk(e)
		}
		return n
	}
	panic("unexpected JSON value")
}

func decode(b []byte) any {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		panic(err)
	}
	return v
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: json <file> <rounds>")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	rounds, _ := strconv.Atoi(os.Args[2])
	var total int64
	for range rounds {
		s, err := json.Marshal(decode(b))
		if err != nil {
			panic(err)
		}
		total += walk(decode(s))
	}
	fmt.Println(total)
}
