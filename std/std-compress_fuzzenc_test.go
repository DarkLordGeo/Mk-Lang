// Why: Deflate and Gzip must produce valid streams for any input.
// What: FuzzSTDCOMPRESS_Deflate, through the oracle of FuzzSTDCOMPRESS_Inflate; Go's compress/flate and
// compress/gzip must read each output back to the input.
// How: each input is also sent doubled with a byte changed, so short inputs give matches at every distance.
//
//	go test ./std -run '^$' -fuzz FuzzSTDCOMPRESS_Deflate -fuzztime 5m -parallel 2

package std

import (
	"bytes"
	"testing"
)

func FuzzSTDCOMPRESS_Deflate(f *testing.F) {
	for kind := 0; kind < 5; kind++ {
		f.Add(czInput(kind, 300, 5))
	}
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0}, 600))
	f.Add([]byte("abcabcabcabcabcabcabcabcabcabcabcabc"))
	f.Fuzz(func(t *testing.T, data []byte) {
		dup := append(append([]byte{}, data...), data...)
		if len(data) > 0 {
			dup[len(data)+len(data)/2] ^= 0x5A
		}
		ins := [][]byte{data, data, dup, dup}
		recs := []czRec{{op: czDeflate, data: data}, {op: czGzip, data: data}, {op: czDeflate, data: dup}, {op: czGzip, data: dup}}
		got := czAsk(t, recs)
		for i, r := range recs {
			if got[i].verdict != "enc" {
				t.Fatalf("op %d (% x): %v", r.op, ins[i][:min(len(ins[i]), 64)], got[i])
			}
			out, err := czGoRead(r.op, got[i].enc)
			if err != nil || !bytes.Equal(out, ins[i]) {
				t.Errorf("op %d (% x): Go read %d bytes, err %v; want the %d input bytes", r.op, ins[i][:min(len(ins[i]), 64)], len(out), err, len(ins[i]))
			}
		}
	})
}
