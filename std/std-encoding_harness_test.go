// Why: thousands of std.encoding cases need one compiled program, not one build each.
// What: the harness: one Mk driver over every function of std.encoding, and the Go helpers that feed it.
// How: the driver is built once per mode and answers one line per stdin record; Go models (and Go's
// encoding packages) give the expected lines. MK_ENCODING_BASE names a std snapshot to build over, with
// std/encoding from this tree, so a broken module elsewhere in a live std does not stop the run.

package std

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// encDriver is the Mk side. Every argument is lower-case hex or a short
// name; every answer is "ok ..." or "err <the error as {e} prints it>".
const encDriver = `import std.encoding
import std.simd

Nib(c: u8): u8 {
    if c >= b'a' {
        return c - 87
    }
    return c - 48
}

Unhex(a: &Arena, s: str): OutOfMemory![]u8 from a {
    b := s.AsBytes()
    out := try a.Slice[u8](b.Len() / 2)
    for i in 0..out.Len() {
        out[i] = (Nib(b[2 * i]) << 4) | Nib(b[2 * i + 1])
    }
    return out[..]
}

const Digits: str = "0123456789abcdef"

HexOf(a: &Arena, b: []u8): OutOfMemory!str from a {
    d := Digits.AsBytes()
    out := try a.Slice[u8](2 * b.Len())
    for i in 0..b.Len() {
        out[2 * i] = d[int(b[i] >> 4)]
        out[2 * i + 1] = d[int(b[i] & 15)]
    }
    return str.From(out[..]).Must()
}

TextOf(a: &Arena, s: str): OutOfMemory!str from a {
    b := try Unhex(a, s)
    return str.From(b) else { "<not utf-8>" }
}

Alpha(name: str): encoding.Alphabet {
    return match name {
        "url" { encoding.Base64Url }
        "raw" { encoding.Base64Raw }
        "urlraw" { encoding.Base64UrlRaw }
        "forgiving" { encoding.Base64Forgiving }
        _ { encoding.Base64 }
    }
}

SetOf(name: str): encoding.PercentSet {
    return match name {
        "fragment" { encoding.PercentSet.Fragment }
        "query" { encoding.PercentSet.Query }
        "specialquery" { encoding.PercentSet.SpecialQuery }
        "path" { encoding.PercentSet.Path }
        "userinfo" { encoding.PercentSet.Userinfo }
        "component" { encoding.PercentSet.Component }
        "form" { encoding.PercentSet.FormUrlencoded }
        _ { encoding.PercentSet.C0Control }
    }
}

// Dump writes the element tree of v in preorder: tag.class.c{children} for
// a constructed element, tag.class.p:content for a primitive one, with the
// typed reading of a universal INTEGER, BOOLEAN or BIT STRING after it.
Dump(a: &Arena, t: mut Text, v: &encoding.DerValue): encoding.Error|OutOfMemory!void {
    try t.Push(try a.Fmt("{t}.{c}", (t: v.tag, c: v.class)))
    if v.constructed {
        try t.Push(".c{")
        var it := v.Children()
        var first := true
        for {
            next := try it.Next()
            child := next else {
                break
            }
            if !first {
                try t.Push(",")
            }
            first = false
            try Dump(a, t, &child)
        }
        try t.Push("}")
        return
    }
    try t.Push(".p:")
    try t.Push(try HexOf(a, v.content))
    if v.class == 0 && v.tag == 2 {
        n := v.Integer() else e {
            try t.Push(try a.Fmt("[int {e}]", (e)))
            return
        }
        try t.Push(try a.Fmt("[int {h}]", (h: try HexOf(a, n))))
    }
    if v.class == 0 && v.tag == 1 {
        b := v.Boolean() else e {
            try t.Push(try a.Fmt("[bool {e}]", (e)))
            return
        }
        try t.Push(try a.Fmt("[bool {b}]", (b)))
    }
    if v.class == 0 && v.tag == 3 {
        bits := v.BitString() else e {
            try t.Push(try a.Fmt("[bits {e}]", (e)))
            return
        }
        try t.Push(try a.Fmt("[bits {h}]", (h: try HexOf(a, bits))))
    }
}

// Rewrite writes v again through a DerWriter.
Rewrite(w: mut encoding.DerWriter, v: &encoding.DerValue): encoding.Error|OutOfMemory!void {
    word := (u32(v.class) << 30) | v.tag
    if !v.constructed {
        try w.Primitive(word, v.content)
        return
    }
    try w.Begin(word)
    var it := v.Children()
    for {
        next := try it.Next()
        child := next else {
            break
        }
        try Rewrite(w, &child)
    }
    try w.End()
}

// FirstInteger and WriteSeq are the two consumers: a field parsed by
// a reader and an encoding written by a writer, each returned from the
// function that made the reader or the writer.
FirstInteger(data: []u8): encoding.Error![]u8 from data {
    (value, rest) := try encoding.DerRead(data)
    _ = rest
    var it := value.Children()
    child := try it.Next() else {
        fail encoding.BadInput(offset: 0)
    }
    return try child.Integer()
}

WriteSeq(a: &Arena, m: []u8): encoding.Error|OutOfMemory![]u8 from a {
    var w := encoding.DerWriter.New(a)
    try w.Begin(0x10)
    try w.Integer(m)
    try w.End()
    return try w.Bytes()
}

DerCase(a: &Arena, data: []u8): encoding.Error|OutOfMemory!str from a {
    (value, rest) := try encoding.DerRead(data)
    var t := Text.New(a)
    try Dump(a, mut t, &value)
    var w := encoding.DerWriter.New(a)
    try Rewrite(mut w, &value)
    again := try w.Bytes()
    same := again.Len() == data.Len() - rest.Len() && simd.Equal(again, data[0..again.Len()])
    return try a.Fmt("ok {d} rest {r} rewrite {s}", (d: t.Str(), r: rest.Len(), s: same))
}

// IntOf, BoolOf and BitsOf are v's typed readings, or their errors,
// whatever v's class, tag and form.
IntOf(a: &Arena, v: &encoding.DerValue): OutOfMemory!str from a {
    n := v.Integer() else e {
        return try a.Fmt("{e}", (e))
    }
    return try a.Fmt("={h}", (h: try HexOf(a, n)))
}

BoolOf(a: &Arena, v: &encoding.DerValue): OutOfMemory!str from a {
    b := v.Boolean() else e {
        return try a.Fmt("{e}", (e))
    }
    return try a.Fmt("={b}", (b))
}

BitsOf(a: &Arena, v: &encoding.DerValue): OutOfMemory!str from a {
    bits := v.BitString() else e {
        return try a.Fmt("{e}", (e))
    }
    return try a.Fmt("={h}", (h: try HexOf(a, bits)))
}

// Word is the u32 that lower-case hex s spells.
Word(s: str): u32 {
    var w: u32 = 0
    for c in s.AsBytes() {
        w = (w << 4) | u32(Nib(c))
    }
    return w
}

// WriterScript runs a DerWriter through script, commands joined by ",":
// b<tag> Begin, e End, p<tag>:<content> Primitive, i<magnitude> Integer
// (tags and bytes in hex), then Bytes. It answers each command's result
// ("-" or the error) and then Bytes'.
WriterScript(a: &Arena, script: str): OutOfMemory!str from a {
    var w := encoding.DerWriter.New(a)
    var t := Text.New(a)
    try t.Push("ok")
    for cmd in script.Split(",") {
        if cmd.Len() == 0 {
            continue
        }
        arg := cmd[1..]
        if cmd.StartsWith("b") {
            try w.Begin(Word(arg))
            try t.Push(" -")
        } else if cmd.StartsWith("e") {
            var r := " -"
            w.End() else e {
                r = try a.Fmt(" {e}", (e))
            }
            try t.Push(r)
        } else if cmd.StartsWith("p") {
            var parts := arg.Split(":")
            tag := parts.Next() else { "" }
            content := parts.Next() else { "" }
            try w.Primitive(Word(tag), try Unhex(a, content))
            try t.Push(" -")
        } else if cmd.StartsWith("i") {
            try w.Integer(try Unhex(a, arg))
            try t.Push(" -")
        }
    }
    out := w.Bytes() else e {
        try t.Push(try a.Fmt(" bytes {e}", (e)))
        return try a.Dup(t.Str())
    }
    try t.Push(try a.Fmt(" bytes ={h}", (h: try HexOf(a, out))))
    return try a.Dup(t.Str())
}

Case(a: &Arena into a, line: str): OutOfMemory!str from a {
    var parts := line.Split(" ")
    op := parts.Next() else { "" }
    x := parts.Next() else { "" }
    y := parts.Next() else { "" }
    z := parts.Next() else { "" }
    match op {
        "b64e" {
            data := try Unhex(a, y)
            s := try encoding.Base64Encode(a, data, Alpha(x))
            var dst := try a.Slice[u8](encoding.Base64Len(data.Len(), Alpha(x)) + 3)
            n := encoding.Base64EncodeTo(mut dst[..], data, Alpha(x))
            same := simd.Equal(s.AsBytes(), dst[0..n])
            return try a.Fmt("ok {s} {l} {same}", (s, l: encoding.Base64Len(data.Len(), Alpha(x)), same))
        }
        "b64d" {
            s := try TextOf(a, y)
            out := encoding.Base64Decode(a, s, Alpha(x)) else e {
                return try a.Fmt("err {e}", (e))
            }
            var dst := try a.Slice[u8](out.Len())
            n := encoding.Base64DecodeTo(mut dst[..], s, Alpha(x)) else e {
                return try a.Fmt("to {e}", (e))
            }
            var short := "-"
            if out.Len() > 0 {
                var small := try a.Slice[u8](out.Len() - 1)
                _ = encoding.Base64DecodeTo(mut small[..], s, Alpha(x)) else e {
                    short = try a.Fmt("{e}", (e))
                    0
                }
            }
            // An oversized dst: nothing past the count returned changes.
            var big := try a.Slice[u8](out.Len() + 20)
            big.Fill(0xA5)
            n2 := encoding.Base64DecodeTo(mut big[..], s, Alpha(x)) else e {
                return try a.Fmt("big {e}", (e))
            }
            var kept := true
            for k in n2..big.Len() {
                if big[k] != 0xA5 {
                    kept = false
                }
            }
            same := n == out.Len() && simd.Equal(dst[..], out) && n2 == n && simd.Equal(big[0..n2], out) && kept
            return try a.Fmt("ok {h} {same} {short}", (h: try HexOf(a, out), same, short))
        }
        "hexe" {
            return try a.Fmt("ok {s}", (s: try encoding.HexEncode(a, try Unhex(a, x))))
        }
        "hexd" {
            out := encoding.HexDecode(a, try TextOf(a, x)) else e {
                return try a.Fmt("err {e}", (e))
            }
            return try a.Fmt("ok {h}", (h: try HexOf(a, out)))
        }
        "pcte" {
            return try a.Fmt("ok {s}", (s: try encoding.PercentEncode(a, try Unhex(a, y), SetOf(x))))
        }
        "pctd" {
            out := try encoding.PercentDecode(a, try TextOf(a, x))
            return try a.Fmt("ok {h}", (h: try HexOf(a, out)))
        }
        "pcth" {
            // One 0 or 1 per byte of y, its membership in set x.
            set := SetOf(x)
            var t := Text.New(a)
            for c in try Unhex(a, y) {
                try t.Push(if set.Has(c) { "1" } else { "0" })
            }
            return try a.Fmt("ok {s}", (s: t.Str()))
        }
        "deck" {
            e := encoding.Lookup(x) else err {
                return try a.Fmt("lookup {e}", (e: err))
            }
            out := try e.DecodeKeepBom(a, try Unhex(a, y))
            return try a.Fmt("ok {h}", (h: try HexOf(a, out.AsBytes())))
        }
        "look" {
            e := encoding.Lookup(try TextOf(a, x)) else err {
                return try a.Fmt("err {e}", (e: err))
            }
            return try a.Fmt("ok {n}", (n: e.Name()))
        }
        "dec" {
            e := encoding.Lookup(x) else err {
                return try a.Fmt("lookup {e}", (e: err))
            }
            out := try e.Decode(a, try Unhex(a, y))
            return try a.Fmt("ok {h}", (h: try HexOf(a, out.AsBytes())))
        }
        "decs" {
            e := encoding.Lookup(x) else err {
                return try a.Fmt("lookup {e}", (e: err))
            }
            out := e.DecodeStrict(a, try Unhex(a, y)) else err {
                return try a.Fmt("err {e}", (e: err))
            }
            return try a.Fmt("ok {h}", (h: try HexOf(a, out.AsBytes())))
        }
        "enc" {
            e := encoding.Lookup(x) else err {
                return try a.Fmt("lookup {e}", (e: err))
            }
            out := e.Encode(a, try TextOf(a, y)) else err {
                return try a.Fmt("err {e}", (e: err))
            }
            return try a.Fmt("ok {h}", (h: try HexOf(a, out)))
        }
        "push" {
            e := encoding.Lookup(x) else err {
                return try a.Fmt("lookup {e}", (e: err))
            }
            data := try Unhex(a, z)
            var d := e.Decoder()
            var t := Text.New(a)
            var at := 0
            for cut in y.Split(",") {
                k := cut.ParseInt() else { 0 }
                end := if at + Cast[int](k) > data.Len() { data.Len() } else { at + Cast[int](k) }
                try t.Push(try d.Push(a, data[at..end], false))
                at = end
            }
            try t.Push(try d.Push(a, data[at..], true))
            again := try d.Push(a, data, true)
            return try a.Fmt("ok {h} {r}", (h: try HexOf(a, t.Str().AsBytes()), r: again == t.Str()))
        }
        "der" {
            return DerCase(a, try Unhex(a, x)) else e {
                match e {
                    OutOfMemory {
                        fail OutOfMemory
                    }
                    _ {
                        return try a.Fmt("err {e}", (e))
                    }
                }
            }
        }
        "derkids" {
            (value, rest) := encoding.DerRead(try Unhex(a, x)) else e {
                return try a.Fmt("read {e}", (e))
            }
            _ = rest
            var it := value.Children()
            var n := 0
            for {
                next := it.Next() else e {
                    return try a.Fmt("err {e} after {n}", (e, n))
                }
                child := next else {
                    break
                }
                _ = child
                n += 1
            }
            return try a.Fmt("ok {n}", (n))
        }
        "dertyped" {
            (value, rest) := encoding.DerRead(try Unhex(a, x)) else e {
                return try a.Fmt("read {e}", (e))
            }
            _ = rest
            return try a.Fmt("ok int {i} bool {b} bits {s}", (i: try IntOf(a, &value), b: try BoolOf(a, &value), s: try BitsOf(a, &value)))
        }
        "derw" {
            return try WriterScript(a, x)
        }
        "pctrt" {
            data := try Unhex(a, y)
            enc := try encoding.PercentEncode(a, data, SetOf(x))
            dec := try encoding.PercentDecode(a, enc)
            return try a.Fmt("ok {e} {same}", (e: enc, same: simd.Equal(dec, data)))
        }
        "deri" {
            var w := encoding.DerWriter.New(a)
            try w.Integer(try Unhex(a, x))
            out := w.Bytes() else e {
                return try a.Fmt("err {e}", (e))
            }
            return try a.Fmt("ok {h}", (h: try HexOf(a, out)))
        }
        "derseq" {
            seq := WriteSeq(a, try Unhex(a, x)) else e {
                return try a.Fmt("err {e}", (e))
            }
            n := FirstInteger(seq) else e {
                return try a.Fmt("err {e}", (e))
            }
            return try a.Fmt("ok {s} {n}", (s: try HexOf(a, seq), n: try HexOf(a, n)))
        }
        "pem" {
            blocks := encoding.ParsePem(a, try TextOf(a, x)) else e {
                match e {
                    OutOfMemory {
                        fail OutOfMemory
                    }
                    _ {
                        return try a.Fmt("err {e}", (e))
                    }
                }
            }
            var t := Text.New(a)
            try t.Push(try a.Fmt("ok {n}", (n: blocks.Len())))
            for i in 0..blocks.Len() {
                try t.Push(try a.Fmt(" {l}:{h}", (l: try HexOf(a, blocks[i].label.AsBytes()), h: try HexOf(a, blocks[i].der))))
            }
            return try a.Dup(t.Str())
        }
        "pemf" {
            var t := Text.New(a)
            var w := t.Writer()
            encoding.FormatPem(mut w, try TextOf(a, x), try Unhex(a, y)) else {
                return "err write"
            }
            return try a.Fmt("ok {h}", (h: try HexOf(a, t.Str().AsBytes())))
        }
        "vecd" {
            b := try Unhex(a, y)
            var dst := try a.Slice[u8](b.Len() / 4 * 3)
            (read, wrote) := encoding.FxBase64DecodeVec(mut dst[..], b, x == "url")
            return try a.Fmt("ok {read} {wrote} {h}", (read, wrote, h: try HexOf(a, dst[0..wrote])))
        }
        "vece" {
            data := try Unhex(a, y)
            var dst := try a.Slice[u8](encoding.Base64Len(data.Len(), encoding.Base64))
            (read, wrote) := encoding.FxBase64EncodeVec(mut dst[..], data, x == "url")
            return try a.Fmt("ok {read} {wrote} {h}", (read, wrote, h: try HexOf(a, dst[0..wrote])))
        }
        "hvece" {
            data := try Unhex(a, x)
            var dst := try a.Slice[u8](2 * data.Len())
            read := encoding.FxHexEncodeVec(mut dst[..], data)
            return try a.Fmt("ok {read} {h}", (read, h: try HexOf(a, dst[0..2 * read])))
        }
        "hvecd" {
            b := try Unhex(a, x)
            var dst := try a.Slice[u8](b.Len() / 2)
            wrote := encoding.FxHexDecodeVec(mut dst[..], b)
            return try a.Fmt("ok {wrote} {h}", (wrote, h: try HexOf(a, dst[0..wrote])))
        }
        _ {
            return "bad op"
        }
    }
}

export Main(sys: &System): ReadError|WriteError|OutOfMemory!void {
    var r := sys.Stdin()
    var out := sys.Stdout()
    var arena := try Arena.New(sys.heap)
    defer arena.Free()
    for {
        line := try r.Line(&arena) else {
            break
        }
        answer := try Case(&arena, line)
        try out.Str(answer)
        try out.Str("\n")
        arena.Reset()
    }
    try out.Flush()
}
`

// encFixture is written to std/encoding in the driver's copy of std: it
// exports the vector steps, so a test can see that they take blocks (a
// vector step that refused every block would pass every other test, the
// scalar path doing the work). Nothing here is part of std.
const encFixture = `// Fixture of the std-encoding tests: private functions, exported.

export FxBase64DecodeVec(dst: mut []u8, b: []u8, url: bool): (read: int, wrote: int) {
    return Base64DecodeVec(dst, b, url)
}

export FxBase64EncodeVec(dst: mut []u8, data: []u8, url: bool): (read: int, wrote: int) {
    return Base64EncodeVec(dst, data, url)
}

export FxHexEncodeVec(dst: mut []u8, data: []u8): int {
    return HexEncodeVec(dst, data)
}

export FxHexDecodeVec(dst: mut []u8, b: []u8): int {
    return HexDecodeVec(dst, b)
}
`

// encBuilt holds one driver executable per build mode.
var encBuilt struct {
	sync.Mutex
	exe map[string]string
	err map[string]string
}

// encStdDir builds the std directory a test's mk uses: the live tree, or
// MK_ENCODING_BASE with this tree's std/encoding; both with encFixture.
func encStdDir(t *testing.T, root string) string {
	t.Helper()
	base := os.Getenv("MK_ENCODING_BASE")
	if base == "" {
		return copyStd(t, root, map[string]string{"encoding/tests_encodingfx.mk": encFixture})
	}
	defer func() {
		if err := os.WriteFile(filepath.Join(root, "std", "encoding", "tests_encodingfx.mk"), []byte(encFixture), 0o644); err != nil {
			t.Fatal(err)
		}
	}()
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stdDir, "encoding")); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(stdDir, "encoding"), os.DirFS("encoding")); err != nil {
		t.Fatal(err)
	}
	return stdDir
}

// encExe builds the driver for mode ("dev" or "release") once per test
// binary and returns its path; a missing toolchain skips the test.
func encExe(t testing.TB, mode string) string {
	t.Helper()
	encBuilt.Lock()
	defer encBuilt.Unlock()
	if encBuilt.exe == nil {
		encBuilt.exe = map[string]string{}
		encBuilt.err = map[string]string{}
	}
	if exe, ok := encBuilt.exe[mode]; ok {
		return exe
	}
	if msg, ok := encBuilt.err[mode]; ok {
		t.Skip(msg)
	}
	tt, ok := t.(*testing.T)
	if !ok {
		t.Fatal("encExe: the first build needs a *testing.T")
	}
	// A driver lives as long as its test binary, which no t.TempDir does, so
	// each build leaves its std copy behind: remove those older than two
	// hours (a fuzz run's workers share none and last minutes).
	if old, _ := filepath.Glob(filepath.Join(os.TempDir(), "mk-encoding-*")); len(old) > 0 {
		for _, d := range old {
			if fi, err := os.Stat(d); err == nil && time.Since(fi.ModTime()) > 2*time.Hour {
				os.RemoveAll(d)
			}
		}
	}
	dir, err := packageTempDir("mk-encoding-" + mode)
	if err != nil {
		t.Fatal(err)
	}
	stdDir := encStdDir(tt, dir)
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(encDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"build"}
	if mode == "release" {
		args = append(args, "--release")
	}
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(tt, dir, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			encBuilt.err[mode] = "needs the LLVM toolchain: " + out
			t.Skip(encBuilt.err[mode])
		}
		t.Fatalf("mk build of the encoding driver (%s): exit %d\n%s", mode, status, out)
	}
	encBuilt.exe[mode] = exe
	return exe
}

// encModes are the builds every differential runs.
var encModes = []string{"dev", "release"}

// encCase is one record and the line the driver must answer.
type encCase struct {
	in, want string
}

// encRun sends cases to the mode's driver in batches and reports every
// answer that differs, up to ten.
func encRun(t *testing.T, mode string, cases []encCase) {
	t.Helper()
	exe := encExe(t, mode)
	bad := 0
	const batch = 4000
	for start := 0; start < len(cases); start += batch {
		end := min(start+batch, len(cases))
		var in strings.Builder
		for _, c := range cases[start:end] {
			in.WriteString(c.in)
			in.WriteString("\n")
		}
		r := runNative(t, exe, in.String())
		if r.code != 0 {
			t.Fatalf("driver (%s) exit %d\nstderr: %s\nlast input lines near the failure: %s", mode, r.code, r.stderr, encTail(r.stdout))
		}
		got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
		if len(got) != end-start {
			t.Fatalf("driver (%s): %d answers for %d records\nstderr: %s", mode, len(got), end-start, r.stderr)
		}
		for i, c := range cases[start:end] {
			if got[i] != c.want {
				t.Errorf("%s: %s\n got  %s\n want %s", mode, encShort(c.in), encShort(got[i]), encShort(c.want))
				bad++
				if bad == 10 {
					t.Fatalf("%s: stopping after 10 differences", mode)
				}
			}
		}
	}
}

// encRunAll runs cases in every build mode.
func encRunAll(t *testing.T, cases []encCase) {
	t.Helper()
	for _, mode := range encModes {
		encRun(t, mode, cases)
	}
}

func encShort(s string) string {
	if len(s) > 400 {
		return s[:400] + fmt.Sprintf("... (%d bytes)", len(s))
	}
	return s
}

func encTail(s string) string {
	if len(s) > 1000 {
		return "..." + s[len(s)-1000:]
	}
	return s
}

// encHex is the driver's hex of b.
func encHex(b []byte) string { return hex.EncodeToString(b) }

// encRng is splitmix64, so a failing case can be named by its seed.
type encRng struct{ s uint64 }

func (r *encRng) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	return z ^ z>>31
}

func (r *encRng) intn(n int) int { return int(r.next() % uint64(n)) }

func (r *encRng) bytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.next())
	}
	return b
}
