// Why: the Deflate matcher (shaped like Go's fastEncL6) has edges that ordinary text rarely reaches.
// What: adversarial inputs aimed at those edges, written by Mk's Deflate and Gzip.
// How: Go's compress/flate and compress/gzip read each output back to the input.

package std

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"fmt"
	"io"
	"testing"
)

// czAdversarial returns the named inputs: matches longer than MaxLong (1
// MiB, the cap that keeps the tables' entries below 2^32), runs whose
// match lengths straddle the split at 258 to 261, distances at the window
// edge (32767, 32768, 32769), repeats past the window, every length around
// the 13-byte floor below which Deflate writes only literals, and a long
// literal run (the growing scan stride) followed by matches.
func czAdversarial() (names []string, inputs [][]byte) {
	add := func(name string, b []byte) {
		names = append(names, name)
		inputs = append(inputs, b)
	}
	add("zeros 3 MiB", make([]byte, 3<<20))
	r := czRng{77}
	rnd := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(r.next())
		}
		return b
	}
	var runs []byte
	for k := 250; k <= 270; k++ {
		for rep := 0; rep < 3; rep++ {
			runs = append(runs, rnd(3)...)
			runs = append(runs, bytes.Repeat([]byte{byte(k)}, k)...)
		}
	}
	add("runs 250 to 270", runs)
	for _, d := range []int{32767, 32768, 32769} {
		p := rnd(d)
		add(fmt.Sprintf("period %d", d), append(append(append([]byte{}, p...), p...), p...))
	}
	p := rnd(40000)
	add("repeat past the window", append(append([]byte{}, p...), p...))
	q := rnd(20000)
	add("repeat inside the window", append(append([]byte{}, q...), q...))
	for n := 9; n <= 14; n++ {
		add(fmt.Sprintf("zeros %d", n), make([]byte, n))
		add(fmt.Sprintf("random %d", n), rnd(n))
	}
	lit := rnd(1 << 20)
	add("literal run then matches", append(lit, bytes.Repeat([]byte("abcdefgh"), 10000)...))
	text := czInput(2, 200000, 9)
	mixed := append(append(append([]byte{}, rnd(100000)...), text...), rnd(100000)...)
	add("random text random", append(mixed, text...))
	return names, inputs
}

func TestSTDCOMPRESS_EncodeAdversarial(t *testing.T) {
	names, inputs := czAdversarial()
	var recs []czRec
	var want [][]byte
	for i, b := range inputs {
		recs = append(recs, czRec{op: czDeflate, data: b, name: "deflate " + names[i]}, czRec{op: czGzip, data: b, name: "gzip " + names[i]})
		want = append(want, b, b)
	}
	for _, mode := range []string{"--release", ""} {
		got := czRun(t, czOracleExe(t, mode), recs)
		for i, r := range recs {
			if got[i].verdict != "enc" {
				t.Fatalf("%s %s: %v", mode, r.name, got[i])
			}
			out, err := czGoRead(r.op, got[i].enc)
			if err != nil || !bytes.Equal(out, want[i]) {
				t.Errorf("%s %s: Go read %d bytes, err %v; want the %d input bytes", mode, r.name, len(out), err, len(want[i]))
			}
		}
	}
}

// czGoRead reads an encoding of op (czDeflate or czGzip) with Go's reader.
func czGoRead(op byte, enc []byte) ([]byte, error) {
	if op == czDeflate {
		return io.ReadAll(flate.NewReader(bytes.NewReader(enc)))
	}
	zr, err := gzip.NewReader(bytes.NewReader(enc))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}
