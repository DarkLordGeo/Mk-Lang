// json-small, Go, idiomatic std: the task of README.md with encoding/json
// on small documents: each line of the file (bytes.SplitSeq at '\n'; the
// array's "[", "," or "]" cut off; an empty line skipped) is one document.
// Per document: a json.Decoder with UseNumber into any (as the json row),
// json.Marshal (compact JSON), the same decode of that text, and the walk
// of the second tree; the garbage collector frees the trees. Usage:
// json-small <file> <rounds>.
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

func doc(line []byte) []byte {
	d := bytes.TrimPrefix(line, []byte("["))
	if bytes.HasSuffix(d, []byte(",")) || bytes.HasSuffix(d, []byte("]")) {
		d = d[:len(d)-1]
	}
	return d
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: json-small <file> <rounds>")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	rounds, _ := strconv.Atoi(os.Args[2])
	var total int64
	for range rounds {
		for line := range bytes.SplitSeq(b, []byte("\n")) {
			d := doc(line)
			if len(d) == 0 {
				continue
			}
			s, err := json.Marshal(decode(d))
			if err != nil {
				panic(err)
			}
			total += walk(decode(s))
		}
	}
	fmt.Println(total)
}
