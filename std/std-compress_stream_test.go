// Why: a streaming decoder must give the same bytes whatever output sizes its caller reads into.
// What: compress.Inflater over stdin, read into seeded output sizes from 1 byte to more than the window.
// How: against Go's compress/flate and Mk's one-shot Inflate; seeded input pieces are in std-compress_pieces_test.go.

package std

import (
	"bytes"
	"compress/flate"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
	"testing"
)

// czStreamProg reads a raw deflate stream (or, given a third argument,
// gzip members through Inflater.NewGzip) from stdin through an Inflater,
// into output buffers whose sizes come from splitmix64 seeded by args[1],
// between 1 and args[2] bytes, and prints "ok <len> <crc32 hex>" or the
// error. The input pieces are the pipe's (czPieceProg seeds them).
const czStreamProg = `import std.compress
import std.hash

export Main(sys: &System): WriteError|OutOfMemory|ParseError!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    var s := Cast[u64](try sys.args[1].ParseInt())
    maxc := Cast[int](try sys.args[2].ParseInt())
    buf := try sys.arena.Slice[u8](maxc)
    var z := compress.Inflater.New(sys.arena)
    if sys.args.Len() > 3 {
        z = compress.Inflater.NewGzip(sys.arena)
    }
    var h := hash.Crc32State.New()
    var total := 0
    for {
        s = s +% 0x9E3779B97F4A7C15
        k := 1 + Trunc[int]((s >> 33) % Cast[u64](maxc))
        got := z.Read(mut inp, mut buf[0..k]) else e {
            match e {
                compress.Corrupt(offset: o) {
                    try Print(mut out, "corrupt {o}\n", (o: o))
                }
                compress.Truncated {
                    try Print(mut out, "truncated\n")
                }
                compress.TooLarge {
                    try Print(mut out, "toolarge\n")
                }
                OutOfMemory {
                    try Print(mut out, "oom\n")
                }
                ReadError {
                    try Print(mut out, "readerror\n")
                }
                _ {
                    Panic("compress.Error gained a member this match does not handle")
                }
            }
            return
        }
        if got == 0 {
            break
        }
        if got > k {
            try Print(mut out, "overrun\n")
            return
        }
        h.Bytes(buf[0..got])
        total += got
    }
    c := h.Sum()
    try Print(mut out, "ok {total} {c:x}\n", (total: total, c: c))
}
`

// TestSTDCOMPRESS_InflaterStreams: the Inflater gives Go's output for Go's
// streams at every level, whatever the read sizes, and Mk's one-shot
// answer, offsets included, for truncated and corrupt streams. The input
// arrives in whatever pieces the pipe makes; the seeded input pieces are
// TestSTDCOMPRESS_InflaterInputPieces's.
func TestSTDCOMPRESS_InflaterStreams(t *testing.T) {
	exe := czBuild(t, czStreamProg)
	text := czInput(2, 1<<20, 11)
	rnd := czInput(0, 300000, 12)
	type tc struct {
		name string
		data []byte
	}
	var cases []tc
	for _, lv := range czLevels {
		cases = append(cases, tc{fmt.Sprintf("text level %d", lv), czDeflateGo(t, text, lv)})
	}
	cases = append(cases,
		tc{"random level 6", czDeflateGo(t, rnd, 6)},
		tc{"runs level 6", czDeflateGo(t, czInput(3, 500000, 13), 6)},
		tc{"pattern level 9", czDeflateGo(t, czInput(4, 400000, 14), 9)},
		tc{"empty", czDeflateGo(t, nil, 6)},
		tc{"real level 6", czDeflateGo(t, czRealInput(t), 6)})
	full := czDeflateGo(t, text, 6)
	cases = append(cases, tc{"truncated", full[:len(full)/2]})
	bad := append([]byte{}, full...)
	bad[len(bad)/3] ^= 0x10
	cases = append(cases, tc{"corrupt", bad})
	r := czRng{5}
	for i := 0; i < 6; i++ {
		cases = append(cases, tc{fmt.Sprintf("mutation %d", i), czMutate(czDeflateGo(t, czInput(i%5, 3000, 15), 6), &r)})
	}
	// Each answer must be Mk's one-shot answer exactly, offsets included
	// (czStreamWant), and that answer's verdict the Go model's.
	recs := make([]czRec, len(cases))
	for i, c := range cases {
		recs[i] = czRec{op: czInflate, limit: czNoLimit, data: c.data, name: c.name}
	}
	oracle := czOracleExe(t, "")
	wants := czStreamWant(recs, func(r []czRec) []czRes { return czRun(t, oracle, r) })
	pieces := [][2]int{{1, 1}, {2, 7}, {3, 1000}, {4, 70000}, {5, 65536}}
	for ci, c := range cases {
		want := wants[ci]
		if model := czStreamModel(c.data); !czSame(want, model) {
			t.Errorf("%s: one-shot %v, the Go model %v", c.name, want, model)
		}
		for i, pc := range pieces {
			if pc[1] == 1 && len(c.data) > 200000 {
				continue // a byte per call over a megabyte is slow in a test
			}
			if i > 2 && c.name[0] == 'm' {
				continue
			}
			got := runNative(t, exe, string(c.data), fmt.Sprint(pc[0]), fmt.Sprint(pc[1]))
			line := strings.TrimSpace(got.stdout)
			if w := want.String(); line != w {
				t.Errorf("%s, pieces %v: got %q (exit %d, stderr %s), want %q", c.name, pc, line, got.code, got.stderr, w)
			}
		}
	}
}

// TestSTDCOMPRESS_InflaterIgnoresTrailing: the Inflater stops at the
// final block; bytes after it are not read as data.
func TestSTDCOMPRESS_InflaterIgnoresTrailing(t *testing.T) {
	exe := czBuild(t, czStreamProg)
	b := czInput(2, 5000, 3)
	data := append(czDeflateGo(t, b, 6), "trailing bytes"...)
	got := runNative(t, exe, string(data), "1", "4096")
	want := fmt.Sprintf("ok %d %x", len(b), crc32.ChecksumIEEE(b))
	if line := strings.TrimSpace(got.stdout); line != want {
		t.Errorf("got %q, want %q", line, want)
	}
}

// czStreamModel is the Inflater's model: compress/flate read to the end of
// the stream, where bytes after the final block are not the stream's.
func czStreamModel(data []byte) czRes {
	r := czModel(czRec{op: czInflate, limit: czNoLimit, data: data})
	if r.verdict != "corrupt" {
		return r
	}
	out, err := io.ReadAll(flate.NewReader(bytes.NewReader(data)))
	if err != nil {
		return r
	}
	return czRes{verdict: "ok", n: len(out), crc: crc32.ChecksumIEEE(out)}
}
