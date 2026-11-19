// json-codec, Go: encoding/json Unmarshal of a 1 MB document into a slice
// of structs, reps times, and a sum over the records (README.md). The
// struct types are the four levels of the Mk column (R3 has no Next; its
// "next", always null, is an unknown member, ignored). Built with
// GOAMD64=v2 -pgo=off.
package main

import (
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

// recSum is one record's share of the checksum: 1, its id, its name's
// length, its score truncated, its tags' count and lengths, 1 if ok.
func recSum(id int64, name string, score float64, tags []string, ok bool) int64 {
	n := 1 + id + int64(len(name)) + int64(score) + int64(len(tags))
	for _, t := range tags {
		n += int64(len(t))
	}
	if ok {
		n++
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
	var total int64
	for range reps {
		var rs []R0
		if err := json.Unmarshal(b, &rs); err != nil {
			panic(err)
		}
		for i := range rs {
			r0 := &rs[i]
			total += recSum(r0.ID, r0.Name, r0.Score, r0.Tags, r0.OK)
			if r1 := r0.Next; r1 != nil {
				total += recSum(r1.ID, r1.Name, r1.Score, r1.Tags, r1.OK)
				if r2 := r1.Next; r2 != nil {
					total += recSum(r2.ID, r2.Name, r2.Score, r2.Tags, r2.OK)
					if r3 := r2.Next; r3 != nil {
						total += recSum(r3.ID, r3.Name, r3.Score, r3.Tags, r3.OK)
					}
				}
			}
		}
	}
	fmt.Println(total)
}
