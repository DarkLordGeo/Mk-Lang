// jsonlong, Go: encoding/json on one array of long
// strings built in the program from splitmix64(seed), as the Mk program
// builds it (README.md). Usage: jsonlong <mode> <strings> <reps> <seed>.
// parse decodes the document into any reps times and sums the decoded
// string lengths; write decodes it once, then writes it as compact JSON
// with an Encoder (SetEscapeHTML(false), so '<', '>' and '&' stay bytes as
// in Mk) into one bytes.Buffer reset before each, reps times, and sums the
// lengths without the Encoder's trailing newline. Built with GOAMD64=v2
// -pgo=off.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

type mix struct{ x uint64 }

func (m *mix) next() uint64 {
	m.x += 0x9E3779B97F4A7C15
	z := m.x
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// doc is the Mk program's Doc, byte for byte.
func doc(strings int, seed uint64) []byte {
	m := mix{seed}
	var t bytes.Buffer
	t.WriteByte('[')
	var buf [1100]byte
	for k := range strings {
		if k > 0 {
			t.WriteByte(',')
		}
		n := 100 + int(m.next()%1000)
		i := 0
		buf[i] = '"'
		i++
		for i < n {
			r := m.next()
			if r%256 == 0 && i+2 < n {
				buf[i] = '\\'
				if (r>>8)%2 == 0 {
					buf[i+1] = 'n'
				} else {
					buf[i+1] = '"'
				}
				i += 2
			} else {
				c := byte(0x20 + (r>>16)%95)
				if c == '"' || c == '\\' {
					c = 'a'
				}
				buf[i] = c
				i++
			}
		}
		buf[n] = '"'
		t.Write(buf[:n+1])
	}
	t.WriteByte(']')
	return t.Bytes()
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return n
}

func main() {
	mode := os.Args[1]
	text := doc(atoi(os.Args[2]), uint64(atoi(os.Args[4])))
	reps := atoi(os.Args[3])
	var total uint64
	if mode == "parse" {
		for range reps {
			var v any
			if err := json.Unmarshal(text, &v); err != nil {
				panic(err)
			}
			for _, e := range v.([]any) {
				if s, ok := e.(string); ok {
					total += uint64(len(s))
				}
			}
		}
	} else {
		var v any
		if err := json.Unmarshal(text, &v); err != nil {
			panic(err)
		}
		var buf bytes.Buffer
		for range reps {
			buf.Reset()
			e := json.NewEncoder(&buf)
			e.SetEscapeHTML(false)
			if err := e.Encode(v); err != nil {
				panic(err)
			}
			total += uint64(buf.Len() - 1)
		}
	}
	fmt.Println(total)
}
