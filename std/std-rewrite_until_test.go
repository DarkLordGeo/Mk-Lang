// Why: Reader.Until's result must depend only on the stream, never on how
// the source chunks it, and a reader with no inline buffer must not over-read.
// What: a differential test against a Go model over seeded sources, buffer
// sizes, chunkings and delimiters.
// How: a fixture in std.core, reached through one in std.time as in
// std-io_rw_test.go, also prints how many bytes the table gave.

package std

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// untilProbe is the fixture core/io_zuntil.mk: a source of seeded bytes
// served in chunks, and a loop of Until calls.
const untilProbe = `// Test fixture (std/std-rewrite_until_test.go): Reader.Until over a
// chunked source of seeded bytes: total bytes (the chunks' sum), at most
// the next chunk's size per call, cycling.

import std.raw

struct UntilSrc {
    seed: u64
    pos: int
    chunks: *int
    nc: int
    next: int
    total: int
}

// UntilByte is the source's byte at position i: one of "abcdefg\n", from a
// SplitMix64 of seed and i.
UntilByte(seed: u64, i: int): u8 {
    var z := seed +% Trunc[u64](i) *% 0x9E3779B97F4A7C15
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    z = z ^ (z >> 31)
    k := Trunc[int](z & 7)
    if k == 7 {
        return 0x0A
    }
    return Trunc[u8](0x61 + k)
}

UntilSrcOf(ctx: uint): *UntilSrc {
    unsafe "the probe's ctx is the address of its live UntilSrc" {
        return raw.FromAddr[UntilSrc](ctx)
    }
}

UntilR(r: mut Reader, buf: mut []u8): ReadError!int {
    p := UntilSrcOf(ReaderCtx(&*r))
    unsafe "the live UntilSrc" {
        if (*p).pos == (*p).total || buf.Len() == 0 {
            return 0
        }
        var n := raw.Read(raw.Offset((*p).chunks, (*p).next % (*p).nc))
        (*p).next += 1
        if n > buf.Len() {
            n = buf.Len()
        }
        if n > (*p).total - (*p).pos {
            n = (*p).total - (*p).pos
        }
        for i in 0..n {
            buf[i] = UntilByte((*p).seed, (*p).pos + i)
        }
        (*p).pos += n
        return n
    }
}

const UntilRT = ReaderTable(read: UntilR, line: ReadLineBytewise)

// args: cap seed delim c1 c2 ... x q1 q2 ...: a source of chunks c, then
// Until(delim) into buffers of sizes q; prints each count and the bytes
// (newline as '~'), then the bytes the source gave.
export UntilRun(sys: &System): ParseError|WriteError|ReadError!void {
    var out := sys.Stdout()
    cap := int(try sys.args[1].ParseInt())
    seed := Trunc[u64](try sys.args[2].ParseInt())
    delim := Trunc[u8](try sys.args[3].ParseInt())
    var chunks: [512]int = [0; 512]
    var nc := 0
    var total := 0
    var k := 4
    for k < sys.args.Len() && sys.args[k] != "x" {
        chunks[nc] = int(try sys.args[k].ParseInt())
        total += chunks[nc]
        nc += 1
        k += 1
    }
    k += 1
    var src := UntilSrc(seed: seed, pos: 0, chunks: raw.Null[int](), nc: nc, next: 0, total: total)
    src.chunks = raw.OfMut(mut chunks[0])
    var buf: [4096]u8 = [0; 4096]
    var got: [4096]u8 = [0; 4096]
    unsafe "the probe's own source, alive for the reader" {
        var r := NewReader(sys.io, raw.Of(&UntilRT), raw.Addr(raw.OfMut(mut src)), mut buf[0..cap])
        for k < sys.args.Len() {
            q := int(try sys.args[k].ParseInt())
            n := try r.Until(delim, mut got[0..q])
            for i in 0..n {
                if got[i] == 0x0A {
                    got[i] = 0x7E
                }
            }
            try Print(mut out, "{n} ", (n))
            try out.Bytes(got[0..n])
            try out.Str("\n")
            k += 1
        }
    }
    pulled := src.pos
    try Print(mut out, "pulled {pulled}\n", (pulled))
}
`

const untilProbeTime = `// Test fixture: lets a program reach std.core's Until probe.
import std.core

export UntilRun(sys: &System): ParseError|WriteError|ReadError!void {
    try core.UntilRun(sys)
}
`

const untilProbeMain = `import std.time

export Main(sys: &System): ParseError|WriteError|ReadError!void {
    try time.UntilRun(sys)
}
`

// untilByte is the fixture's UntilByte.
func untilByte(seed uint64, i int) byte {
	z := seed + uint64(i)*0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	z ^= z >> 31
	k := int(z & 7)
	if k == 7 {
		return '\n'
	}
	return byte('a' + k)
}

// untilModel is the reference: the lines the probe prints. pulled is the
// source's total for a buffered reader (whatever it read ahead) and the
// consumed count for cap 0, so for cap > 0 the caller compares all but the
// last line.
func untilModel(seed uint64, total int, delim byte, reads []int) []string {
	stream := make([]byte, total)
	for i := range stream {
		stream[i] = untilByte(seed, i)
	}
	var out []string
	pos := 0
	for _, q := range reads {
		n := 0
		for n < q && pos+n < total {
			n++
			if stream[pos+n-1] == delim {
				break
			}
		}
		out = append(out, fmt.Sprintf("%d %s", n, strings.ReplaceAll(string(stream[pos:pos+n]), "\n", "~")))
		pos += n
	}
	return append(out, fmt.Sprintf("pulled %d", pos))
}

// TestSTDRW_ReaderUntilMatchesModel runs Reader.Until against the Go model.
//
// Until returns, from the reader's position, the bytes up to and including
// the first delim, or buf.Len() bytes, or what is left at end of input. A
// reader with no inline buffer (cap 0) must pull exactly the bytes it
// returned, never one past delim.
func TestSTDRW_ReaderUntilMatchesModel(t *testing.T) {
	h := hostIo
	exe := h.build(t, map[string]string{"core/io_zuntil.mk": untilProbe, "time/zuntil.mk": untilProbeTime}, untilProbeMain)
	type tc struct {
		cap           int
		seed          uint64
		delim         byte
		chunks, reads []int
	}
	cases := []tc{
		{16, 1, 'c', []int{5, 100, 3}, []int{4, 4, 20, 30, 100, 0, 1}},
		{0, 2, '\n', []int{1, 1, 1, 50, 7}, []int{10, 10, 10, 100}},
		{0, 3, 'a', []int{3, 9, 1}, []int{5, 5, 5, 5}},
		{1, 4, 'b', []int{1, 2, 3}, []int{3, 3, 3}},
		{16, 5, 'z', []int{16, 16, 16}, []int{20, 40, 4}}, // no delim at all
	}
	rng := rand.New(rand.NewSource(331))
	caps := []int{0, 1, 2, 7, 16, 64, 1024}
	delims := []byte("abcdefgz\n")
	for seed := 0; seed < 80; seed++ {
		c := caps[rng.Intn(len(caps))]
		var chunks, reads []int
		for k := 0; k < 20; k++ {
			chunks = append(chunks, 1+rng.Intn(2*c+20))
		}
		for k := 0; k < 30; k++ {
			reads = append(reads, rng.Intn(3*c+30))
		}
		cases = append(cases, tc{c, uint64(seed)*7919 + 11, delims[rng.Intn(len(delims))], chunks, reads})
	}
	calls := make([]ioCall, len(cases))
	for i, c := range cases {
		args := []string{strconv.Itoa(c.cap), strconv.FormatUint(c.seed, 10), strconv.Itoa(int(c.delim))}
		for _, n := range c.chunks {
			args = append(args, strconv.Itoa(n))
		}
		args = append(args, "x")
		for _, n := range c.reads {
			args = append(args, strconv.Itoa(n))
		}
		calls[i] = ioCall{args: args}
	}
	runs := h.runMany(t, exe, calls)
	for i, c := range cases {
		r := runs[i]
		if r.code != 0 {
			t.Fatalf("case %d: exit %d\n%s\n%s", i, r.code, r.stdout, r.stderr)
		}
		got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
		total := 0
		for _, n := range c.chunks {
			total += n
		}
		// The source is the chunks' sum of bytes, served chunks[k] (cycling,
		// cut to the reader's buffer) per call, so the stream does not
		// depend on how the reader asks for it.
		want := untilModel(c.seed, total, c.delim, c.reads)
		n := len(want)
		if c.cap > 0 {
			n-- // a buffered reader reads ahead; only the cap 0 count is exact
			if len(got) == len(want) {
				got = got[:n]
			}
		}
		if strings.Join(got, "|") != strings.Join(want[:n], "|") {
			t.Errorf("case %d cap %d delim %q chunks %v reads %v:\n got  %q\n want %q", i, c.cap, c.delim, c.chunks, c.reads, got, want[:n])
		}
	}
}
