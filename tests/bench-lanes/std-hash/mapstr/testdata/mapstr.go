// mapstr, Go, idiomatic std: the task of ../README.md with a
// map[string]uint64 whose keys are substrings of one generated string (no
// copy per key), Go's Swiss-table map and its runtime string hash
// (AES-based where the CPU has AES-NI, seeded per map). Built with
// GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// keys returns the n keys and their n misses (the same bytes with the
// first letter in upper case, so never a key).
func keys(n int, seed uint64, minLen, maxLen int) ([]string, []string) {
	var buf, miss []byte
	ends := make([]int, 0, n)
	st := seed
	span := uint64(maxLen - minLen + 1)
	for i := 0; i < n; i++ {
		st += 0x9e3779b97f4a7c15
		z := mix(st)
		l := minLen + int(z%span)
		for j := 0; j < l; j++ {
			if j%8 == 0 {
				z = mix(z + uint64(j))
			}
			c := byte('a' + (z>>(8*uint(j%8))&0xFF)%26)
			buf = append(buf, c)
			if j == 0 {
				c -= 'a' - 'A'
			}
			miss = append(miss, c)
		}
		ends = append(ends, len(buf))
	}
	all, allMiss := string(buf), string(miss)
	ks := make([]string, n)
	ms := make([]string, n)
	start := 0
	for i, e := range ends {
		ks[i], ms[i] = all[start:e], allMiss[start:e]
		start = e
	}
	return ks, ms
}

func round(ks, ms []string, r uint64) uint64 {
	m := map[string]uint64{}
	for i, k := range ks {
		m[k] = uint64(i) + r
	}
	var sum uint64
	for i, k := range ks {
		if v, ok := m[k]; ok {
			sum += v
		}
		if _, ok := m[ms[i]]; ok {
			sum++
		}
		if i%2 == 0 {
			delete(m, k)
		}
	}
	for _, k := range ks {
		if _, ok := m[k]; ok {
			sum += 3
		}
	}
	return sum + uint64(len(m))
}

func main() {
	if len(os.Args) != 6 {
		fmt.Fprintln(os.Stderr, "usage: mapstr <n> <rounds> <seed> <minlen> <maxlen>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	seed, _ := strconv.ParseUint(os.Args[3], 10, 64)
	minLen, _ := strconv.Atoi(os.Args[4])
	maxLen, _ := strconv.Atoi(os.Args[5])
	ks, ms := keys(n, seed, minLen, maxLen)
	var total uint64
	for r := 0; r < rounds; r++ {
		total += round(ks, ms, uint64(r))
	}
	fmt.Println(total)
}
