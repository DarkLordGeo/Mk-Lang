// Why: str.From's UTF-8 validation, str.ParseInt, Text and Reader.Line must
// work at compile time as they do natively.
// What: TestValidUtf8MatchesGo, TestParseIntMatchesGo,
// TestTextAtCompileTime and TestReaderLineAtCompileTime.
// How: std's Mk code runs in the evaluator through mk check and is compared
// with Go's unicode/utf8 and strconv.

package std

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// byteCases is a const byte buffer, its case offsets and the wanted bools,
// as Mk declarations named prefix + Buf, Off, Want.
func byteCases(prefix string, cases [][]byte, want func([]byte) bool) string {
	var buf, off, ok []string
	n := 0
	for _, c := range cases {
		off = append(off, strconv.Itoa(n))
		for _, b := range c {
			buf = append(buf, fmt.Sprintf("%#x", b))
		}
		n += len(c)
		ok = append(ok, strconv.FormatBool(want(c)))
	}
	off = append(off, strconv.Itoa(n))
	return fmt.Sprintf("const %sBuf: [%d]u8 = [%s]\nconst %sOff: [%d]int = [%s]\nconst %sWant: [%d]bool = [%s]\n",
		prefix, n, strings.Join(buf, ", "), prefix, len(off), strings.Join(off, ", "), prefix, len(ok), strings.Join(ok, ", "))
}

// TestValidUtf8MatchesGo checks str.From's validator against
// utf8.Valid at compile time: every lead byte followed by each boundary
// continuation byte and tails of 0 to 2 bytes (overlongs, surrogates, above
// U+10FFFF, truncations), each also after 1 to 9 ASCII bytes so the 8-byte
// fast path meets it at every offset, plus seeded random strings.
func TestValidUtf8MatchesGo(t *testing.T) {
	var cases [][]byte
	seconds := []byte{0x00, 0x7f, 0x80, 0x8f, 0x90, 0x9f, 0xa0, 0xbf, 0xc0}
	tails := [][]byte{{}, {0x80}, {0x80, 0x80}, {0xbf, 0xbf}, {0x41}}
	for b0 := 0; b0 < 256; b0++ {
		for _, b1 := range seconds {
			for _, tl := range tails {
				cases = append(cases, append([]byte{byte(b0), b1}, tl...))
			}
		}
		cases = append(cases, []byte{byte(b0)})
	}
	for pre := 1; pre <= 9; pre++ {
		for _, s := range []string{"é", "€", "\U0001F600", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\xe2\x82", "\xc0\xaf"} {
			cases = append(cases, []byte(strings.Repeat("a", pre)+s+"bcdefghij"))
		}
	}
	r := rand.New(rand.NewSource(1))
	alphabet := []byte{0x41, 0x7f, 0x80, 0xbf, 0xc2, 0xdf, 0xe0, 0xed, 0xef, 0xf0, 0xf4, 0xf5, 0xff, 0xa0, 0x9f, 0x90, 0x8f}
	for k := 0; k < 400; k++ {
		c := make([]byte, r.Intn(12))
		for i := range c {
			c[i] = alphabet[r.Intn(len(alphabet))]
		}
		cases = append(cases, c)
	}
	fixture := byteCases("Utf", cases, utf8.Valid) + `
UtfCheck(): int {
    var bad := 0
    buf := &UtfBuf
    off := &UtfOff
    want := &UtfWant
    for k in 0..want.Len() {
        if ValidUtf8(buf[off[k]..off[k + 1]]) != want[k] {
            bad += 1
        }
    }
    return bad
}

const UtfOk = [0; 1][UtfCheck()]
`
	expectClean(t, "s_g174_utf8", fixture)
}

// TestParseIntMatchesGo checks str.ParseInt against
// strconv.ParseInt(s, 10, 64) at compile time: the value when Go accepts,
// ParseError exactly when Go refuses, over the limits, signs, empty and
// sign-only strings, spaces, underscores, prefixes and seeded numbers.
func TestParseIntMatchesGo(t *testing.T) {
	cases := []string{"", "+", "-", "0", "-0", "+0", "7", "-7", "+42", "00012", "9223372036854775807",
		"9223372036854775808", "-9223372036854775808", "-9223372036854775809", "99999999999999999999",
		" 1", "1 ", "1_0", "0x10", "1e3", "--1", "+-1", "12a", "١", "18446744073709551616"}
	r := rand.New(rand.NewSource(2))
	for k := 0; k < 200; k++ {
		cases = append(cases, strconv.FormatInt(r.Int63()>>uint(r.Intn(63)), 10), "-"+strconv.FormatInt(r.Int63()>>uint(r.Intn(63)), 10))
	}
	var lits, vals, oks []string
	for _, c := range cases {
		v, err := strconv.ParseInt(c, 10, 64)
		lits = append(lits, mkString(c))
		vals = append(vals, strconv.FormatInt(v, 10))
		oks = append(oks, strconv.FormatBool(err == nil))
	}
	fixture := fmt.Sprintf("const IntIn: [%d]str = [%s]\nconst IntVal: [%d]i64 = [%s]\nconst IntOk: [%d]bool = [%s]\n",
		len(cases), strings.Join(lits, ", "), len(cases), strings.Join(vals, ", "), len(cases), strings.Join(oks, ", ")) + `
IntOne(k: int): int {
    v := IntIn[k].ParseInt() else {
        if IntOk[k] {
            return 1
        }
        return 0
    }
    if !IntOk[k] || v != IntVal[k] {
        return 1
    }
    return 0
}

IntCheck(): int {
    var bad := 0
    for k in 0..IntIn.Len() {
        bad += IntOne(k)
    }
    return bad
}

const IntCheckOk = [0; 1][IntCheck()]
`
	expectClean(t, "s_g174_int", fixture)
}

// TestTextAtCompileTime runs Text at compile time over a probe heap:
// Push and PushChar with growth in place and by copy, Str, Drain on and off
// a scalar boundary (the off case through a check, not a panic), the
// Writer of Text with an int's Format and an invalid UTF-8 run refused, and
// Clear.
func TestTextAtCompileTime(t *testing.T) {
	t.Parallel()
	expectClean(t, "lib107", probeHeap+`
TextLife(): int {
    var mem: [16384]u8 = [0; 16384]
    unsafe "fixture" {
        heap := MakeHeap(raw.DataMut(mut mem[..]), raw.Of(&ProbeHeapTable))
        var a := Arena.New(&heap) else { return 90 }
        var bad := 0
        var t := Text.New(&a)
        t.Push("h\u{E9}llo") else {
            a.Free()
            return 91
        }
        t.PushChar('\u{20AC}') else {
            a.Free()
            return 92
        }
        if t.Str() != "h\u{E9}llo\u{20AC}" {
            bad += 1
        }
        t.Drain(1)
        if t.Str() != "\u{E9}llo\u{20AC}" {
            bad += 1
        }
        var w := t.Writer()
        w.Str(" x") else {
            a.Free()
            return 93
        }
        n: int = -42
        n.Format(mut w) else {
            a.Free()
            return 94
        }
        var junk: [2]u8 = [0x41, 0xFF]
        var refused := false
        w.Bytes(junk[..]) else {
            refused = true
        }
        if !refused {
            bad += 1
        }
        if t.Str() != "\u{E9}llo\u{20AC} x-42" {
            bad += 1
        }
        for _ in 0..300 {
            t.Push("ab") else {
                a.Free()
                return 95
            }
        }
        if t.Str().Len() != 613 || !t.Str().EndsWith("abab") || !t.Str().StartsWith("\u{E9}llo") {
            bad += 1
        }
        t.Clear()
        if t.Str() != "" {
            bad += 1
        }
        a.Free()
        return bad
    }
}

const TextOk = [0; 1][TextLife()]
`, ptrCastICE)
}

// TestReaderLineAtCompileTime runs Reader.Line at compile time over
// a 16-byte inline buffer and a table that serves 5 bytes per call: \n and
// \r\n stripped, an empty line kept, a last line with no newline, none at
// the end; then a line longer than the buffer is ReadError and invalid
// UTF-8 is ReadError.
func TestReaderLineAtCompileTime(t *testing.T) {
	t.Parallel()
	expectClean(t, "lib110", probeHeap+`
struct LineSrc {
    data: str
    pos: int
}

const LineTable = ReaderTable(read: LineRead, line: ReadLineBytewise)

LineRead(r: mut Reader, buf: mut []u8): ReadError!int {
    unsafe "fixture: ctx is the LineSrc" {
        src := mut *raw.PtrCast[u8, LineSrc](r.ctx)
        var n := src.data.Len() - src.pos
        if n > 5 {
            n = 5
        }
        if n > buf.Len() {
            n = buf.Len()
        }
        for i in 0..n {
            buf[i] = src.data.Byte(src.pos + i)
        }
        src.pos += n
        return n
    }
}

LineReader(src: mut LineSrc, buf: mut []u8): Reader from src, buf {
    unsafe "fixture" {
        return Reader(io: InertIoValue(), table: &LineTable, ctx: raw.PtrCast[LineSrc, u8](raw.OfMut(src)),
            buf: raw.DataMut(buf), start: 0, end: 0, cap: buf.Len())
    }
}

LineWant(r: mut Reader, a: &Arena, want: str): int {
    l := r.Line(a) else { return 1 }
    s := l else { return 1 }
    if s != want {
        return 1
    }
    return 0
}

LineFails(r: mut Reader, a: &Arena): int {
    l := r.Line(a) else { return 0 }
    if l.IsSome() {
        return 1
    }
    return 1
}

LineLife(): int {
    var mem: [16384]u8 = [0; 16384]
    unsafe "fixture" {
        heap := MakeHeap(raw.DataMut(mut mem[..]), raw.Of(&ProbeHeapTable))
        var a := Arena.New(&heap) else { return 90 }
        var bad := 0
        var src := LineSrc(data: "a\r\nbb\n\nccc\u{E9}", pos: 0)
        var buf: [16]u8 = [0; 16]
        var r := LineReader(mut src, mut buf[..])
        bad += LineWant(mut r, &a, "a")
        bad += LineWant(mut r, &a, "bb")
        bad += LineWant(mut r, &a, "")
        bad += LineWant(mut r, &a, "ccc\u{E9}")
        end := r.Line(&a) else {
            a.Free()
            return 96
        }
        if end.IsSome() {
            bad += 1
        }
        var long := LineSrc(data: "0123456789abcdefg\n", pos: 0)
        var buf2: [16]u8 = [0; 16]
        var r2 := LineReader(mut long, mut buf2[..])
        bad += LineFails(mut r2, &a)
        // A lone \r ending a last line with no \n is kept,
        // buffered and through a cap-0 reader (the table's line entry).
        var cr := LineSrc(data: "ab\r\ncd\r", pos: 0)
        var buf3: [16]u8 = [0; 16]
        var r3 := LineReader(mut cr, mut buf3[..])
        bad += LineWant(mut r3, &a, "ab")
        bad += LineWant(mut r3, &a, "cd\r")
        var cr0 := LineSrc(data: "ab\r\ncd\r", pos: 0)
        var buf4: [0]u8 = []
        var r4 := LineReader(mut cr0, mut buf4[..])
        bad += LineWant(mut r4, &a, "ab")
        bad += LineWant(mut r4, &a, "cd\r")
        a.Free()
        return bad
    }
}

const LineOk = [0; 1][LineLife()]
`, ptrCastICE)
}
