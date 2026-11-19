// json-parse, Go: encoding/json decoding a 1 MB document into any, with
// UseNumber so numbers stay text as in Mk, reps times, and a walk of each
// tree (README.md). Built with GOAMD64=v2 -pgo=off.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

func walk(v any) uint64 {
	switch x := v.(type) {
	case string:
		return 1 + uint64(len(x))
	case []any:
		n := uint64(1)
		for _, e := range x {
			n += walk(e)
		}
		return n
	case map[string]any:
		n := uint64(1)
		for k, e := range x {
			n += uint64(len(k)) + walk(e)
		}
		return n
	}
	return 1
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
	var total uint64
	for range reps {
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			panic(err)
		}
		total += walk(v)
	}
	fmt.Println(total)
}
