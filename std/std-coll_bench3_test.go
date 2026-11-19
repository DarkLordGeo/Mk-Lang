// Why: Scalars.Next and Words.Next decode text scalar by scalar, so their
// speed needs a comparison with Go's.
// What: TestStdColl_BenchScalars over 8 MB of ASCII, mixed and CJK text, and
// of short and long words, against Go's range and strings.Fields.
// How: a --release program times itself (IoTable.now, best of 5) beside Go on
// the same machine; the sums must agree. Skips unless MK_BENCH=1.

package std

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

const bench3Program = `import std.collfix

Run(sys: &System, out: mut Writer, tag: str, piece: str): WriteError|OutOfMemory!void {
    var a := try sys.arena.Sub()
    defer a.Free()
    var t := Text.New(&a)
    for _ in 0..(8000000 / piece.Len()) {
        try t.Push(piece)
    }
    s := t.Str()
    var best: i64 = 1000000000000
    var sum: u64 = 0
    for _ in 0..5 {
        t1 := collfix.Now(sys.io)
        var it := s.Scalars()
        for c := it.Next() {
            sum = sum +% u64(u32(c))
        }
        t2 := collfix.Now(sys.io)
        if t2 - t1 < best {
            best = t2 - t1
        }
    }
    total := Trunc[i64](s.Len())
    ps := best * 1000 / total
    try Print(out, "scalars {tag} ps_per_byte {ps} {sum}\n", (tag, ps, sum))
}

WordsRun(sys: &System, out: mut Writer, tag: str, piece: str): WriteError|OutOfMemory!void {
    var a := try sys.arena.Sub()
    defer a.Free()
    var t := Text.New(&a)
    for _ in 0..(8000000 / piece.Len()) {
        try t.Push(piece)
    }
    s := t.Str()
    var best: i64 = 1000000000000
    var sum := 0
    for _ in 0..5 {
        t1 := collfix.Now(sys.io)
        var it := s.Words()
        for w := it.Next() {
            sum += w.Len()
        }
        t2 := collfix.Now(sys.io)
        if t2 - t1 < best {
            best = t2 - t1
        }
    }
    total := Trunc[i64](s.Len())
    ps := best * 1000 / total
    try Print(out, "words {tag} ps_per_byte {ps} {sum}\n", (tag, ps, sum))
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    for p in Pieces {
        try Run(sys, mut out, p.tag, p.text)
    }
    for p in WordPieces {
        try WordsRun(sys, mut out, p.tag, p.text)
    }
}
`

// bench3Pieces are the repeated texts.
var bench3Pieces = []struct{ tag, text string }{
	{"ascii", "abcdefghijklmnopqrstuvwxyz012345"},
	{"mixed", "abcdé€fgh　ijk"},
	{"cjk", "日本語文"},
}

// bench3WordPieces are the repeated texts for Words.
var bench3WordPieces = []struct{ tag, text string }{
	{"short", "the quick brown fox jumps over a lazy dog "},
	{"long", "internationalization characteristically\n"},
}

// goWords is a Go loop over Words' rule: maximal runs of scalars that are
// not White_Space.
func goWords(s string, f func(string)) {
	i := 0
	for i < len(s) {
		for i < len(s) {
			r, n := utf8.DecodeRuneInString(s[i:])
			if !unicode.IsSpace(r) {
				break
			}
			i += n
		}
		if i >= len(s) {
			return
		}
		st := i
		for i < len(s) {
			r, n := utf8.DecodeRuneInString(s[i:])
			if unicode.IsSpace(r) {
				break
			}
			i += n
		}
		f(s[st:i])
	}
}

func goBench3() string {
	var b strings.Builder
	for _, p := range bench3Pieces {
		s := strings.Repeat(p.text, 8000000/len(p.text))
		best := time.Hour
		var sum uint64
		for k := 0; k < 5; k++ {
			t0 := time.Now()
			for _, r := range s {
				sum += uint64(r)
			}
			if d := time.Since(t0); d < best {
				best = d
			}
		}
		fmt.Fprintf(&b, "scalars %s ps_per_byte %d %d (range)\n", p.tag, best.Nanoseconds()*1000/int64(len(s)), sum)
	}
	for _, p := range bench3WordPieces {
		s := strings.Repeat(p.text, 8000000/len(p.text))
		best, bestLoop := time.Hour, time.Hour
		sum, sumLoop := 0, 0
		for k := 0; k < 5; k++ {
			t0 := time.Now()
			for _, w := range strings.Fields(s) {
				sum += len(w)
			}
			if d := time.Since(t0); d < best {
				best = d
			}
			t0 = time.Now()
			goWords(s, func(w string) { sumLoop += len(w) })
			if d := time.Since(t0); d < bestLoop {
				bestLoop = d
			}
		}
		fmt.Fprintf(&b, "words %s ps_per_byte %d %d (strings.Fields; a DecodeRuneInString and IsSpace loop: %d ps per byte)\n", p.tag, best.Nanoseconds()*1000/int64(len(s)), sumLoop, bestLoop.Nanoseconds()*1000/int64(len(s)))
		_ = sum
	}
	return b.String()
}

// TestStdColl_BenchScalars logs std's numbers and Go's for the same work;
// the sums must agree.
func TestStdColl_BenchScalars(t *testing.T) {
	if os.Getenv("MK_BENCH") != "1" {
		t.Skip("set MK_BENCH=1 to run the std-coll benchmarks")
	}
	var src strings.Builder
	src.WriteString("import std.collfix\n\nstruct Piece {\n    tag: str\n    text: str\n}\n\n")
	fmt.Fprintf(&src, "const Pieces: [%d]Piece = [", len(bench3Pieces))
	for i, p := range bench3Pieces {
		if i > 0 {
			src.WriteString(", ")
		}
		fmt.Fprintf(&src, "Piece(tag: %s, text: %s)", mkString(p.tag), mkString(p.text))
	}
	src.WriteString("]\n\n")
	fmt.Fprintf(&src, "const WordPieces: [%d]Piece = [", len(bench3WordPieces))
	for i, p := range bench3WordPieces {
		if i > 0 {
			src.WriteString(", ")
		}
		fmt.Fprintf(&src, "Piece(tag: %s, text: %s)", mkString(p.tag), mkString(p.text))
	}
	src.WriteString("]\n")
	src.WriteString(strings.TrimPrefix(bench3Program, "import std.collfix\n"))
	mk := collExec(t, collBuildWith(t, nil, src.String(), "--release"))
	gos := goBench3()
	t.Logf("machine: %s/%s, %d CPUs, %s", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version())
	t.Logf("mk build --release:\n%s", mk)
	t.Logf("Go:\n%s", gos)
	ml, gl := strings.Split(strings.TrimSpace(mk), "\n"), strings.Split(strings.TrimSpace(gos), "\n")
	if len(ml) != len(gl) {
		t.Fatalf("%d lines from mk, %d from Go", len(ml), len(gl))
	}
	for i := range ml {
		if strings.Fields(ml[i])[4] != strings.Fields(gl[i])[4] {
			t.Errorf("sums differ: %q against %q", ml[i], gl[i])
		}
	}
}
