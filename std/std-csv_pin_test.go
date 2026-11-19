// Why: csv.Records' fast paths must not change any outcome, and the oracle
// tests accept a Quote or a TooLarge where a limit is tight.
// What: TestSTDCSV_RecordsPinned against std/csv/testdata/records.pinned.
// How: hashes the outcomes, lines and columns of errors included, of inputs
// aimed at the fast paths, per buffer size and option set, in dev and release.

package std

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// csvPinChunk is how many inputs share one pinned hash.
const csvPinChunk = 128

// csvPinOpts are the option sets the outcomes are pinned under, by name.
var csvPinOpts = []struct {
	name string
	o    csvOpts
}{
	{"default", csvDefault},
	{"tight", csvOpts{',', '"', 3, 5}},
	{"apostrophe-maxint", csvOpts{',', '\'', math.MaxInt, 2}},
	{"semicolon-64", csvOpts{';', '"', 64, 4096}},
}

// csvPinSizes are the buffer sizes: two and three index slots, then 16,
// 64 and 2048.
var csvPinSizes = []int{64, 80, 100, 512, 2048, 1 << 16}

// csvPinMix is splitmix64, the inputs' own generator, so they do not move
// when another test's inputs do.
func csvPinMix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// csvPinInputs are the pinned inputs, in a fixed order.
func csvPinInputs() []string {
	in := []string{"", ",", ",,", "\n", "\r", "\r\n", "\n\n", "\r\n\r\n", "\r\r\n", "a", "a\n", "a\r", "a\r\n",
		"a,b", "a,b\n", "a,b\r\n", ",a\n", "a,\n", "a,\r\n", "a,,b\n", "\n\na\n\n", "a\n\nb", "a\rb\n", "a\r,b\r\r\n",
		`"a"`, "\"a\",\"b\"\n", "\"a\"\"b\",c\r\n", "\"a\nb\",c\n", "\"a\r\nb\"\r\n", `""`, `""""`, "\"\",\"\"\n",
		"a\"b,c\n", "\"a\"b\n", "\"a", "\"a\n", "\"a\r", "x,\"y\"\r", "x,\"y\"\r\r\n", "\"q\"\"\"x",
		"\xff", "a,\xff\n", "\xc3\xa9,\xc3\n", "\xe2\x82\xac", "\"\xe2\x82\",x\n", "\xc3,\xa9\n"}
	// Field ends at every offset of the stop mask: after j bytes, records
	// whose middle field holds 0 to 130 bytes, ending in LF or CR LF.
	for j := 0; j < 64; j++ {
		var sb strings.Builder
		sb.WriteString(strings.Repeat("p", j))
		for l := 0; l <= 130; l++ {
			sb.WriteString(",")
			sb.WriteString(strings.Repeat("x", l))
			if (j+l)%2 == 0 {
				sb.WriteString(",z\n")
			} else {
				sb.WriteString(",z\r\n")
			}
		}
		in = append(in, sb.String())
	}
	// A last record with no line break, or with a lone CR, of every length
	// to 130.
	for l := 0; l <= 130; l++ {
		in = append(in, "a,"+strings.Repeat("x", l), strings.Repeat("y", l)+"\r")
	}
	// Records of k fields around every buffer's slot count, empty and
	// one-byte fields, a quoted last field, with and without a line break.
	for _, k := range []int{1, 2, 3, 4, 5, 6, 15, 16, 17, 63, 64, 65, 2047, 2048, 2049} {
		in = append(in, strings.Repeat(",", k-1)+"\nb\n", strings.Repeat("a,", k-1)+"a\r\nb\n",
			strings.Repeat("a,", k-1)+"\"q\"\n", strings.Repeat(",", k-1))
	}
	// Fields longer than each buffer, bare, between others, quoted, and
	// quoted with "" pairs.
	for _, l := range []int{31, 32, 33, 63, 64, 65, 79, 80, 81, 99, 100, 101, 127, 128, 129, 511, 512, 513,
		2047, 2048, 2049, 49151, 49152, 49153, 65535, 65536, 65537} {
		x := strings.Repeat("x", l)
		in = append(in, x+"\ny\n", "a,"+x+",b\r\n", "\""+x+"\"\n", "\""+strings.Repeat("x\"\"", l/3)+"\",c\n")
	}
	// A byte that is not UTF-8, a stray continuation or a cut lead at
	// every position of a 37-byte and of an 80-byte document.
	short := "ab,cde,\xc3\xa9f,ghij,\"k,l\",mnop\r\nqr,st\n"
	long := strings.Repeat("abcdefghi,", 7) + "\xe2\x82\xac" + "klmnop\n"
	for _, base := range []string{short, long} {
		for p := 0; p < len(base); p++ {
			for _, c := range []byte{0xFF, 0x80, 0xC3, 0xE2} {
				b := []byte(base)
				b[p] = c
				in = append(in, string(b))
			}
		}
	}
	// Byte soups of the grammar's pieces.
	alpha := []string{"a", "b", " ", ",", ",", "\"", "\"", "\"\"", "\r", "\n", "\r\n", "\xc3\xa9", "\xff", "x",
		strings.Repeat("w", 20), ";", "'"}
	st := uint64(0x5eed)
	for i := 0; i < 1700; i++ {
		max := uint64(60)
		if i >= 1500 {
			max = 700
		}
		st += 0x9E3779B97F4A7C15
		n := csvPinMix(st) % max
		var sb strings.Builder
		for j := uint64(0); j < n; j++ {
			st += 0x9E3779B97F4A7C15
			sb.WriteString(alpha[csvPinMix(st)%uint64(len(alpha))])
		}
		in = append(in, sb.String())
	}
	return in
}

// csvPinLines are the pinned file's lines for the outcomes got of the
// inputs under one option set and buffer size: one hash per chunk.
func csvPinLines(name string, size int, got []string) []string {
	var lines []string
	for c := 0; c*csvPinChunk < len(got); c++ {
		h := sha256.New()
		for _, g := range got[c*csvPinChunk : min(len(got), (c+1)*csvPinChunk)] {
			fmt.Fprintf(h, "%d\n%s", len(g), g)
		}
		lines = append(lines, fmt.Sprintf("%s %d %d %s", name, size, c, hex.EncodeToString(h.Sum(nil))[:16]))
	}
	return lines
}

// TestSTDCSV_RecordsPinned holds csv.Records' outcomes to
// std/csv/testdata/records.pinned, which a reader without the written-out
// per-field work of Bare (the index entry, Stop's mask test, Close) printed.
// The oracle tests (std-csv_diff_test.go) accept a Quote or a TooLarge where
// a limit is tight, and a small buffer's outcome up to any TooLarge; this
// test holds every outcome, the lines and columns of its errors included, to
// exactly what that reader printed. Its inputs aim at those steps: fields of
// 0 to 130 bytes ending at every offset of the 64-byte stop mask, records of
// exactly the index's slots and one more at each buffer size, fields longer
// than each buffer, a byte that is not UTF-8 (or a lead that is cut) at
// every position, CR LF, a missing final line break, empty fields and
// records, and quoted fields with "" and line breaks; at buffers of 64, 80,
// 100, 512, 2048 and 65536 bytes, under four option sets, at dev and
// release. MK_CSV_PIN_UPDATE=1 rewrites the file: read its diff, since any
// change there is a change in what Next returns.
func TestSTDCSV_RecordsPinned(t *testing.T) {
	path := filepath.Join("csv", "testdata", "records.pinned")
	inputs := csvPinInputs()
	update := os.Getenv("MK_CSV_PIN_UPDATE") == "1"
	var want []string
	if !update {
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("%v (MK_CSV_PIN_UPDATE=1 writes it)", err)
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if l := strings.TrimSpace(sc.Text()); l != "" && !strings.HasPrefix(l, "#") {
				want = append(want, l)
			}
		}
		f.Close()
	}
	for _, mode := range []string{"dev", "release"} {
		t.Run(mode, func(t *testing.T) {
			var flags []string
			if mode == "release" {
				flags = []string{"--release"}
			}
			exe := csvBuild(t, csvDriver, flags...)
			var lines []string
			for _, po := range csvPinOpts {
				for _, size := range csvPinSizes {
					lines = append(lines, csvPinLines(po.name, size, csvRun(t, exe, inputs, size, po.o))...)
				}
			}
			if update {
				if mode == "dev" {
					head := fmt.Sprintf("# csv.Records outcomes of %d inputs (csvPinInputs), %d to a hash: option set, buffer bytes, chunk, sha256 prefix.\n# Written by TestSTDCSV_RecordsPinned with MK_CSV_PIN_UPDATE=1 (std-csv_pin_test.go).\n", len(inputs), csvPinChunk)
					if err := os.WriteFile(path, []byte(head+strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
						t.Fatal(err)
					}
					want = lines
				}
			}
			if len(lines) != len(want) {
				t.Fatalf("%d pinned hashes, %d computed (MK_CSV_PIN_UPDATE=1 rewrites %s)", len(want), len(lines), path)
			}
			bad := 0
			for i := range lines {
				if lines[i] != want[i] {
					bad++
					f := strings.Fields(lines[i])
					t.Errorf("outcomes differ from the pinned ones: %s (got %s); inputs %s*%d onward of that chunk", want[i], f[3], f[2], csvPinChunk)
				}
			}
			t.Logf("%d inputs, %d hashes, %d differ", len(inputs), len(lines), bad)
		})
	}
}
