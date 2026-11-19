// Why: inputs the fuzzers found to break std.compress and std.crypto must
// keep working.
// What: TestRwData_* over the seed files under testdata/fuzz/<target>, which
// the fuzz targets also replay.
// How: runs every seed through czRun, czAsk or cfAsk, so a hang fails after
// czRun's 5 minutes or the test timeout instead of sticking.

package std

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rwSeed reads a Go fuzz corpus file holding one []byte value.
func rwSeed(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(strings.TrimSpace(string(b)), "\n", 2)
	if len(lines) != 2 || lines[0] != "go test fuzz v1" {
		t.Fatalf("%s: not a fuzz corpus file", path)
	}
	v := strings.TrimSpace(lines[1])
	if !strings.HasPrefix(v, "[]byte(") || !strings.HasSuffix(v, ")") {
		t.Fatalf("%s: not one []byte value", path)
	}
	s, err := strconv.Unquote(v[len("[]byte(") : len(v)-1])
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return []byte(s)
}

// rwSeeds returns the seed files of a fuzz target under testdata/fuzz.
func rwSeeds(t *testing.T, target string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "fuzz", target, "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no seeds of %s under testdata", target)
	}
	return files
}

// TestRwData_DeflateSeedsFinish: the two Deflate seeds were reported as
// hangs. Std's Deflate encodes each in about 10 ms (this test); the hang
// is czAsk's, which writes all four records to the oracle's stdin before
// reading any answer, so once the encoded output fills the stdout pipe
// both sides waited; czAsk now writes from a goroutine, as cfAsk does.
// This test encodes each seed as the fuzz target does (the input, and the
// input doubled with one byte changed, as Deflate and Gzip records), in
// one oracle run under czRun's 5-minute limit, and Go's readers must give
// the inputs back.
func TestRwData_DeflateSeedsFinish(t *testing.T) {
	files := rwSeeds(t, "FuzzSTDCOMPRESS_Deflate")
	exe := czOracleExe(t, "")
	for _, f := range files {
		data := rwSeed(t, f)
		dup := append(append([]byte{}, data...), data...)
		if len(data) > 0 {
			dup[len(data)+len(data)/2] ^= 0x5A
		}
		ins := [][]byte{data, data, dup, dup}
		recs := []czRec{{op: czDeflate, data: data}, {op: czGzip, data: data}, {op: czDeflate, data: dup}, {op: czGzip, data: dup}}
		start := time.Now()
		got := czRun(t, exe, recs)
		t.Logf("%s: %d bytes, %v, encoded %d, %d, %d, %d bytes", filepath.Base(f), len(data), time.Since(start),
			len(got[0].enc), len(got[1].enc), len(got[2].enc), len(got[3].enc))
		for i, r := range recs {
			if got[i].verdict != "enc" {
				t.Fatalf("%s op %d: %v", filepath.Base(f), r.op, got[i])
			}
			out, err := czGoRead(r.op, got[i].enc)
			if err != nil || !bytes.Equal(out, ins[i]) {
				t.Errorf("%s op %d: Go read %d bytes, err %v; want the %d input bytes", filepath.Base(f), r.op, len(out), err, len(ins[i]))
			}
		}
	}
}

// TestRwData_InflateTruncatedTail checks the two Inflate seeds whose
// streams end with fewer bits than the block's end-of-block code: the
// stream is truncated, as Go's compress/flate says, not corrupt at its
// end. Every prefix of each seed is checked against the model too.
func TestRwData_InflateTruncatedTail(t *testing.T) {
	files := rwSeeds(t, "FuzzSTDCOMPRESS_Inflate")
	exe := czOracleExe(t, "")
	var recs []czRec
	for _, f := range files {
		data := rwSeed(t, f)
		for n := 0; n <= len(data); n++ {
			recs = append(recs,
				czRec{op: czInflate, limit: czNoLimit, data: data[:n], name: filepath.Base(f)},
				czRec{op: czGunzip, limit: czNoLimit, data: data[:n], name: filepath.Base(f)})
		}
	}
	got := czRun(t, exe, recs)
	for i, r := range recs {
		want := czModel(r)
		if strings.HasPrefix(want.verdict, "error") {
			continue
		}
		if !czSame(got[i], want) {
			t.Errorf("%s op %d, %d bytes: got %v, want %v", r.name, r.op, len(r.data), got[i], want)
		}
	}
}

// TestRwData_CryptoLongInputSeed: the crypto seed's command line is about
// 100 KB, past the 64 KiB process buffer Reader.Line reads stdin through,
// so the oracle fails with ReadError, as the reference says Reader.Line
// does on a longer line; std.crypto is not reached. The fuzz function now
// cuts each input to 16 KiB, whose line fits; this test sends the seed cut
// the same way and wants the Go reference's answer.
func TestRwData_CryptoLongInputSeed(t *testing.T) {
	for _, f := range rwSeeds(t, "FuzzSTDCRYPTO_Primitives") {
		data := rwSeed(t, f)
		c := cfDecode(t, data[:min(len(data), 16<<10)])
		if got := cfAsk(t, "", []string{c.line})[0]; got != c.want {
			t.Errorf("%s: command %.200s\n got  %.200s\n want %.200s", filepath.Base(f), c.line, got, c.want)
		}
	}
}

// TestRwData_DeflateSeedsThroughCzAsk sends each Deflate seed's four
// records through czAsk, as FuzzSTDCOMPRESS_Deflate does: answers larger
// than a pipe's buffer come back while the records are still being
// written, so czAsk returns all four instead of deadlocking.
func TestRwData_DeflateSeedsThroughCzAsk(t *testing.T) {
	for _, f := range rwSeeds(t, "FuzzSTDCOMPRESS_Deflate") {
		data := rwSeed(t, f)
		dup := append(append([]byte{}, data...), data...)
		dup[len(data)+len(data)/2] ^= 0x5A
		recs := []czRec{{op: czDeflate, data: data}, {op: czGzip, data: data}, {op: czDeflate, data: dup}, {op: czGzip, data: dup}}
		if got := czAsk(t, recs); len(got) != len(recs) {
			t.Errorf("%s: %d answers for %d records", filepath.Base(f), len(got), len(recs))
		}
	}
}
