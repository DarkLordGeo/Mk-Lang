// Why: the zlib decoders take untrusted input, so any bytes must give the Go model's verdict.
// What: FuzzCompress_Unzlib (one-shot, with and without a limit) and FuzzCompress_ZlibStream (the streaming decoder).
// How: each input goes to a long-running dev-build oracle; a panic, a hang (c2Hang) or a differing verdict fails.
// Run with go test ./std -run '^$' -fuzz FuzzCompress_Unzlib -fuzztime 5m -parallel 2 (likewise ZlibStream).
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"hash/crc32"
	"os/exec"
	"strings"
	"testing"
)

var c2Srv c2Pool

// c2Ask sends recs to the running zlib oracle (starting one if needed).
func c2Ask(t *testing.T, recs []czRec) []czRes {
	return c2Srv.ask(t, "oracle", func() *exec.Cmd {
		return exec.Command(c2Exe(t, "oracle", c2Oracle))
	}, recs)
}

// c2FuzzSeeds adds Go's zlib streams of every kind and the edge vectors.
func c2FuzzSeeds(f *testing.F) {
	for kind := 0; kind < 5; kind++ {
		b := czInput(kind, 200, 9)
		f.Add(c2ZlibGo(f, b, 6))
		f.Add(c2ZlibGo(f, b, 1))
		f.Add(c2ZlibGo(f, b, 0))
	}
	for _, e := range c2ZlibEdges(f) {
		f.Add(e.data)
	}
}

func FuzzCompress_Unzlib(f *testing.F) {
	c2FuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		recs := []czRec{
			{op: c2Unzlib, limit: czNoLimit, data: data},
			{op: c2Unzlib, limit: int64(len(data)), data: data},
		}
		got := c2Ask(t, recs)
		for i, r := range recs {
			want := c2UnzlibModel(data, r.limit)
			if i == 1 && got[i].verdict == "toolarge" && want.verdict != "ok" {
				continue // the output passed the limit before the error
			}
			if !c2Exact(got[i], want) && !strings.HasPrefix(want.verdict, "error") {
				t.Errorf("limit %d (% x): got %v, want %v", r.limit, data[:min(len(data), 64)], got[i], want)
			}
		}
	})
}

// c2StreamBatch decodes records with the Inflater over a PieceReader:
// op 1, 2, 3 decode raw, gzip, zlib through Read, op 4, 5, 6 the same
// through the Reader; the limit field is the seed, which also picks the
// record's largest input piece (1 to args[1]) and output piece (1 to
// args[2]). Answers czParse's lines; the Reader's failure answers Cause's
// verdict after a second read fails too ("notsticky" else, "readerror"
// when Cause is none), and an end with Cause set answers "causeonok", both
// of which czParse refuses.
const c2StreamBatch = `import std.compress
import std.hash

Word(b: []u8, i: int, k: int): int {
    var x := 0
    for j in 0..k {
        x = x | (int(b[i + j]) << (8 * j))
    }
    return x
}

Verdict(out: mut Writer, e: compress.Error): WriteError!void {
    match e {
        compress.Corrupt(offset: o) {
            try Print(out, "corrupt {o}\n", (o: o))
        }
        compress.Truncated {
            try Print(out, "truncated\n")
        }
        compress.TooLarge {
            try Print(out, "toolarge\n")
        }
        OutOfMemory {
            try Print(out, "oom\n")
        }
        _ {
            Panic("compress.Error gained a member this match does not handle")
        }
    }
}

Stream(out: mut Writer, a: &Arena, io: Io, op: int, data: []u8, seed: u64, maxp: int, maxo: int): WriteError|OutOfMemory!void {
    mode := (op - 1) % 3
    mp := 1 + Trunc[int]((seed >> 40) % Cast[u64](maxp))
    mo := 1 + Trunc[int]((seed >> 20) % Cast[u64](maxo))
    var src := try compress.PieceReader(a, io, data, seed, mp)
    var z := if mode == 1 { compress.Inflater.NewGzip(a) } else if mode == 2 { compress.Inflater.NewZlib(a) } else { compress.Inflater.New(a) }
    buf := try a.Slice[u8](mo)
    var s := seed ^ 0x2545F4914F6CDD1D
    var h := hash.Crc32State.New()
    var total := 0
    if op > 3 {
        var rb: [300]u8 = [0; 300]
        var r := z.Reader(mut src, mut rb[..])
        for {
            s = s +% 0x9E3779B97F4A7C15
            k := 1 + Trunc[int]((s >> 33) % Cast[u64](mo))
            got := r.Read(mut buf[0..k]) else {
                again := r.Read(mut buf[0..k]) else {
                    -1
                }
                if again != -1 {
                    try Print(out, "notsticky\n")
                    return
                }
                if e := z.Cause() {
                    try Verdict(out, e)
                } else {
                    try Print(out, "readerror\n")
                }
                return
            }
            if got == 0 {
                break
            }
            if got > k {
                try Print(out, "overrun\n")
                return
            }
            h.Bytes(buf[0..got])
            total += got
        }
        if e := z.Cause() {
            try Print(out, "causeonok ")
            try Verdict(out, e)
            return
        }
        c := h.Sum()
        try Print(out, "ok {total} {c:x}\n", (total: total, c: c))
        return
    }
    for {
        s = s +% 0x9E3779B97F4A7C15
        k := 1 + Trunc[int]((s >> 33) % Cast[u64](mo))
        got := z.Read(mut src, mut buf[0..k]) else e {
            match e {
                compress.Corrupt(offset: o) {
                    try Print(out, "corrupt {o}\n", (o: o))
                }
                compress.Truncated {
                    try Print(out, "truncated\n")
                }
                compress.TooLarge {
                    try Print(out, "toolarge\n")
                }
                OutOfMemory {
                    try Print(out, "oom\n")
                }
                ReadError {
                    try Print(out, "readerror\n")
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
            try Print(out, "overrun\n")
            return
        }
        h.Bytes(buf[0..got])
        total += got
    }
    c := h.Sum()
    try Print(out, "ok {total} {c:x}\n", (total: total, c: c))
}

// Main names on stderr the error Batch returns: an error of the program
// itself, not a decoder's verdict (those are printed as answers).
export Main(sys: &System): WriteError|ReadError|OutOfMemory|ParseError!void {
    Batch(sys) else e {
        var errw := sys.Stderr()
        match e {
            WriteError {
                try Print(mut errw, "batch: WriteError\n")
            }
            ReadError {
                try Print(mut errw, "batch: ReadError\n")
            }
            OutOfMemory {
                try Print(mut errw, "batch: OutOfMemory\n")
            }
            ParseError {
                try Print(mut errw, "batch: ParseError\n")
            }
        }
        try errw.Flush()
        fail e
    }
}

Batch(sys: &System): WriteError|ReadError|OutOfMemory|ParseError!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    maxp := Cast[int](try sys.args[1].ParseInt())
    maxo := Cast[int](try sys.args[2].ParseInt())
    var hdr: [13]u8 = [0; 13]
    var a := try Arena.New(sys.heap)
    defer a.Free()
    for {
        got := try inp.ReadAll(mut hdr[..])
        if got < 13 {
            return
        }
        n := Word(hdr[..], 9, 4)
        data := try a.Slice[u8](n)
        _ = try inp.ReadAll(data)
        try Stream(mut out, &a, sys.io, int(hdr[0]), data[..], Cast[u64](Word(hdr[..], 1, 7)), maxp, maxo)
        try out.Flush()
        a.Reset()
    }
}
`

var c2StreamSrv c2Pool

// c2StreamAsk sends recs to the running c2StreamBatch program (input
// pieces up to 64 bytes, output pieces up to 16).
func c2StreamAsk(t *testing.T, recs []czRec) []czRes {
	return c2StreamSrv.ask(t, "stream program", func() *exec.Cmd {
		return exec.Command(c2PieceExe(t, "streambatch", c2StreamBatch, nil), "64", "16")
	}, recs)
}

// FuzzCompress_ZlibStream: any input, through one long-running
// dev build of c2StreamBatch (every check on): as a zlib stream through
// Read and the Reader, equal to one-shot Unzlib's answer (a byte after the
// trailer is no error for a stream), and as a raw and a gzip stream
// through the Reader, equal to Read's answer. Input and output pieces are
// seeded by the input, so a failure replays.
func FuzzCompress_ZlibStream(f *testing.F) {
	c2FuzzSeeds(f)
	for kind := 0; kind < 5; kind++ {
		b := czInput(kind, 300, 11)
		f.Add(czGzipGo(f, b, 6))
		f.Add(czDeflateGo(f, b, 6))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		want := c2Ask(t, []czRec{{op: c2Unzlib, limit: czNoLimit, data: data}})[0]
		if want.verdict == "corrupt" {
			if p := c2StreamWant(data); p.verdict == "ok" {
				want = p
			}
		}
		seed := int64(crc32.ChecksumIEEE(data))<<24 | int64(len(data)&0xFFFFFF)
		var recs []czRec
		for op := byte(1); op <= 6; op++ {
			recs = append(recs, czRec{op: op, limit: seed + int64(op)*0x1000003, data: data})
		}
		got := c2StreamAsk(t, recs)
		for i, name := range []string{"raw", "gzip", "zlib"} {
			if got[i+3].String() != got[i].String() {
				t.Errorf("%s (% x): Read %v, Reader %v", name, data[:min(len(data), 64)], got[i], got[i+3])
			}
		}
		if got[2].String() != want.String() {
			t.Errorf("zlib Read (% x): got %v, want %v", data[:min(len(data), 64)], got[2], want)
		}
	})
}
