// Why: Inflate and Gunzip must also run in the compile-time evaluator, inside a const initialiser.
// What: a program whose consts decode Go-written streams of each block type over EvalArena().
// How: the program prints the lengths and CRC-32s the evaluator computed, compared with Go's.

package std

import (
	"compress/flate"
	"fmt"
	"hash/crc32"
	"strings"
	"testing"
)

// czConstLit is b as a Mk array literal of type [len(b)]u8.
func czConstLit(b []byte) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[%d]u8 = [", len(b))
	for i, c := range b {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "0x%02X", c)
	}
	sb.WriteString("]")
	return sb.String()
}

func TestSTDCOMPRESS_InflateAtCompileTime(t *testing.T) {
	text := czInput(2, 3000, 11)
	inputs := [][]byte{
		czDeflateGo(t, text, flate.NoCompression), // stored
		czDeflateGo(t, []byte("hello, hello, hello"), flate.BestSpeed),
		czDeflateGo(t, text, flate.DefaultCompression), // dynamic, with matches
		czGzipGo(t, text, flate.DefaultCompression),
	}
	plain := [][]byte{text, []byte("hello, hello, hello"), text, text}
	var src strings.Builder
	src.WriteString("import std.compress\nimport std.hash\n\n")
	for i, b := range inputs {
		fmt.Fprintf(&src, "const S%d: %s\n", i, czConstLit(b))
	}
	src.WriteString(`
Inf(a: &Arena, s: []u8): []u8 from a {
    r := compress.Inflate(a, s, 100000) else {
        Panic("Inflate failed")
    }
    return r
}

Gun(a: &Arena, s: []u8): []u8 from a {
    r := compress.Gunzip(a, s, 100000) else {
        Panic("Gunzip failed")
    }
    return r
}

const O0 = Inf(EvalArena(), S0[..])
const O1 = Inf(EvalArena(), S1[..])
const O2 = Inf(EvalArena(), S2[..])
const O3 = Gun(EvalArena(), S3[..])
const C0 = hash.Crc32(O0)
const C1 = hash.Crc32(O1)
const C2 = hash.Crc32(O2)
const C3 = hash.Crc32(O3)

export Main(sys: &System): WriteError!void {
    var w := sys.Stdout()
    try Print(mut w, "{a} {b:x}\n", (a: O0.Len(), b: C0))
    try Print(mut w, "{a} {b:x}\n", (a: O1.Len(), b: C1))
    try Print(mut w, "{a} {b:x}\n", (a: O2.Len(), b: C2))
    try Print(mut w, "{a} {b:x}\n", (a: O3.Len(), b: C3))
}
`)
	exe := czBuild(t, src.String())
	got := runNative(t, exe, "")
	var want strings.Builder
	for _, p := range plain {
		fmt.Fprintf(&want, "%d %x\n", len(p), crc32.ChecksumIEEE(p))
	}
	if strings.TrimSpace(got.stdout) != strings.TrimSpace(want.String()) {
		t.Errorf("got\n%s\nwant\n%s", got.stdout, want.String())
	}
}
