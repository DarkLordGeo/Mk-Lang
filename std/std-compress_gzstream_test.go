// Why: the streaming gzip decoder has paths the one-shot Gunzip lacks: long headers, split trailers, member boundaries at refills.
// What: tests of Inflater.NewGzip over stdin in seeded piece sizes.
// How: each answer must equal one-shot Gunzip's (output, or the error with its offset) and the RFC 1952 Go model
// (czGunzipModel); headers pass the 16 KiB input buffer and trailers split across refills.

package std

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"strings"
	"testing"
)

// czGzMember builds one gzip member by hand: the header with the given
// optional fields (FHCRC when hcrc, its CRC flipped when badHcrc), Go's raw
// deflate of b at level 6, and the trailer.
func czGzMember(t *testing.T, b []byte, extra []byte, name, comment string, hcrc, badHcrc bool) []byte {
	t.Helper()
	var flg byte
	if extra != nil {
		flg |= 4
	}
	if name != "" {
		flg |= 8
	}
	if comment != "" {
		flg |= 16
	}
	if hcrc {
		flg |= 2
	}
	h := []byte{0x1f, 0x8b, 8, flg, 0, 0, 0, 0, 0, 255}
	if extra != nil {
		h = binary.LittleEndian.AppendUint16(h, uint16(len(extra)))
		h = append(h, extra...)
	}
	if name != "" {
		h = append(append(h, name...), 0)
	}
	if comment != "" {
		h = append(append(h, comment...), 0)
	}
	if hcrc {
		c := uint16(crc32.ChecksumIEEE(h))
		if badHcrc {
			c ^= 1
		}
		h = binary.LittleEndian.AppendUint16(h, c)
	}
	h = append(h, czDeflateGo(t, b, flate.DefaultCompression)...)
	h = binary.LittleEndian.AppendUint32(h, crc32.ChecksumIEEE(b))
	return binary.LittleEndian.AppendUint32(h, uint32(len(b)))
}

// TestSTDCOMPRESS_GzipInflaterStreams: Inflater.NewGzip over stdin agrees
// with Gunzip on every input, whatever the read sizes, and both agree with
// the Go model's verdict.
func TestSTDCOMPRESS_GzipInflaterStreams(t *testing.T) {
	exe := czBuild(t, czStreamProg)
	text := czInput(2, 1<<20, 21)
	small := czInput(2, 3000, 22)
	type tc struct {
		name string
		data []byte
	}
	var cases []tc
	for _, lv := range czLevels {
		cases = append(cases, tc{fmt.Sprintf("text level %d", lv), czGzipGo(t, text, lv)})
	}
	one := czGzipGo(t, small, 6)
	long := strings.Repeat("n", 40000)
	extra := bytes.Repeat([]byte{0xA5}, 65535)
	cases = append(cases,
		tc{"random level 6", czGzipGo(t, czInput(0, 300000, 23), 6)},
		tc{"real level 6", czGzipGo(t, czRealInput(t), 6)},
		tc{"empty member", czGzipGo(t, nil, 6)},
		tc{"two members", append(append([]byte{}, one...), czGzipGo(t, text[:200000], 1)...)},
		tc{"three members, one empty", append(append(append([]byte{}, one...), czGzipGo(t, nil, 6)...), one...)},
		tc{"every header field", czGzMember(t, small, []byte("xy"), "name.txt", "a comment", true, false)},
		tc{"FNAME of 40000 bytes, FHCRC", czGzMember(t, small, nil, long, "", true, false)},
		tc{"FEXTRA of 65535 bytes, FCOMMENT", czGzMember(t, small, extra, "", long, false, false)},
		tc{"bad FHCRC after a long header", czGzMember(t, small, extra, long, "", true, true)},
		tc{"empty input", nil},
		tc{"one byte 0x1f", []byte{0x1f}},
		tc{"bad magic", []byte{0x1f, 0x8c, 8, 0, 0, 0, 0, 0, 0, 0}},
		tc{"reserved flag", []byte{0x1f, 0x8b, 8, 0x20, 0, 0, 0, 0, 0, 0}},
		tc{"truncated in the fixed header", one[:7]},
		tc{"truncated in a long FNAME", czGzMember(t, small, nil, long, "", false, false)[:30000]},
		tc{"truncated in the trailer", one[:len(one)-3]},
		tc{"truncated in the body", one[:len(one)/2]},
		tc{"trailing garbage", append(append([]byte{}, one...), 'x')},
		tc{"trailing 0x1f", append(append([]byte{}, one...), 0x1f)},
		tc{"trailing zeros", append(append([]byte{}, one...), 0, 0, 0, 0)},
	)
	badCrc := append([]byte{}, one...)
	badCrc[len(badCrc)-8] ^= 1
	badSize := append([]byte{}, one...)
	badSize[len(badSize)-4] ^= 1
	cases = append(cases, tc{"bad CRC", badCrc}, tc{"bad ISIZE", badSize},
		tc{"bad CRC in the second member", append(append([]byte{}, one...), badCrc...)})
	r := czRng{31}
	for i := 0; i < 40; i++ {
		cases = append(cases, tc{fmt.Sprintf("mutation %d", i), czMutate(czGzipGo(t, czInput(i%5, 3000, 24), 6), &r)})
	}
	var recs []czRec
	for _, c := range cases {
		recs = append(recs, czRec{op: czGunzip, limit: czNoLimit, data: c.data, name: c.name})
	}
	oneShot := czRun(t, czOracleExe(t, ""), recs)
	// The hand-built vectors reach the verdicts they are named for.
	verdict := map[string]string{
		"three members, one empty": "ok", "every header field": "ok", "FNAME of 40000 bytes, FHCRC": "ok",
		"FEXTRA of 65535 bytes, FCOMMENT": "ok", "bad FHCRC after a long header": "corrupt",
		"empty input": "truncated", "one byte 0x1f": "truncated", "bad magic": "corrupt", "reserved flag": "corrupt",
		"truncated in the fixed header": "truncated", "truncated in a long FNAME": "truncated",
		"truncated in the trailer": "truncated", "truncated in the body": "truncated", "trailing garbage": "corrupt",
		"trailing 0x1f": "truncated", "trailing zeros": "corrupt", "bad CRC": "corrupt", "bad ISIZE": "corrupt",
		"bad CRC in the second member": "corrupt",
	}
	pieces := [][2]int{{1, 1}, {2, 7}, {3, 1000}, {4, 70000}, {5, 16384}}
	for i, c := range cases {
		want := oneShot[i]
		if v, ok := verdict[c.name]; ok && want.verdict != v {
			t.Errorf("%s: Gunzip says %v, want %s", c.name, want, v)
		}
		if model := czGunzipModel(recs[i]); !czSame(want, model) {
			t.Errorf("%s: Gunzip says %v, the Go model %v", c.name, want, model)
		}
		w := want.String()
		for j, pc := range pieces {
			if pc[1] == 1 && len(c.data) > 200000 {
				continue // a byte per call over a megabyte is slow in a test
			}
			if j > 2 && strings.HasPrefix(c.name, "mutation") {
				continue
			}
			got := runNative(t, exe, string(c.data), fmt.Sprint(pc[0]), fmt.Sprint(pc[1]), "gz")
			if line := strings.TrimSpace(got.stdout); line != w {
				t.Errorf("%s, pieces %v: got %q (exit %d, stderr %s), want Gunzip's %q", c.name, pc, line, got.code, got.stderr, w)
			}
		}
	}
}
