// regexrare, Go: regexp FindIndex of two patterns whose
// literal prefix is rare in the text, as the Mk program runs them
// (README.md). Usage: regexrare <mib> <reps> <seed> [gen|eng|enggen]. The
// text is the Mk program's, byte for byte: mib MiB from splitmix64(seed),
// with "needle42 #7 " written 100 bytes before the end; each rep finds, from
// the start, "#[0-9]+" and "needle[0-9]+", and the checksum sums the match
// starts. With eng or enggen the letters follow English frequencies (eng,
// the Mk program's Eng), "needle42 theseus7 " is planted and the first
// pattern is "theseus[0-9]+". Go has no build-time regex, so the gen
// variants (Mk's gen regex.Matcher code) run the same compiled regexps.
// Built with GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"os"
	"regexp"
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

// eng is the Mk program's Eng: 104 letters in English frequency
// proportions, 20 spaces, 3 digits, a newline.
var eng = [128]byte{0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x74, 0x74, 0x74, 0x74, 0x74, 0x74, 0x74, 0x74, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x6F, 0x6F, 0x6F, 0x6F, 0x6F, 0x6F, 0x6F, 0x6F, 0x69, 0x69, 0x69, 0x69, 0x69, 0x69, 0x69, 0x6E, 0x6E, 0x6E, 0x6E, 0x6E, 0x6E, 0x6E, 0x73, 0x73, 0x73, 0x73, 0x73, 0x73, 0x73, 0x68, 0x68, 0x68, 0x68, 0x68, 0x68, 0x72, 0x72, 0x72, 0x72, 0x72, 0x72, 0x64, 0x64, 0x64, 0x64, 0x6C, 0x6C, 0x6C, 0x6C, 0x63, 0x63, 0x63, 0x75, 0x75, 0x75, 0x6D, 0x6D, 0x77, 0x77, 0x66, 0x66, 0x67, 0x67, 0x79, 0x79, 0x70, 0x70, 0x62, 0x62, 0x76, 0x6B, 0x6A, 0x78, 0x71, 0x7A, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x30, 0x35, 0x39, 0x0A}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return n
}

func main() {
	mib := atoi(os.Args[1])
	reps := atoi(os.Args[2])
	seed := uint64(atoi(os.Args[3]))
	mode := ""
	if len(os.Args) > 4 {
		mode = os.Args[4]
	}
	isEng := mode == "eng" || mode == "enggen"
	n := mib * 1048576
	b := make([]byte, n)
	m := mix{seed}
	for i := range n {
		if isEng {
			b[i] = eng[m.next()%128]
			continue
		}
		r := m.next() % 32
		switch {
		case r < 26:
			b[i] = byte(0x61 + r)
		case r < 29:
			b[i] = 0x20
		case r < 31:
			b[i] = byte(0x30 + m.next()%10)
		default:
			b[i] = 0x0A
		}
	}
	plant, p1 := "needle42 #7 ", "#[0-9]+"
	if isEng {
		plant, p1 = "needle42 theseus7 ", "theseus[0-9]+"
	}
	copy(b[n-100:], plant)
	one := regexp.MustCompile(p1)
	six := regexp.MustCompile("needle[0-9]+")
	var total uint64
	for range reps {
		if x := one.FindIndex(b); x != nil {
			total += uint64(x[0])
		}
		if y := six.FindIndex(b); y != nil {
			total += uint64(y[0])
		}
	}
	fmt.Println(total)
}
