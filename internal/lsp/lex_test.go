// Why: every feature reads the scanner and the declaration scan.
// What: tests of the scanner, the declaration scan, the URI mapping and the
// position encodings.
// How: table tests on small texts.

package lsp

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestLexClasses(t *testing.T) {
	text := "export Id(x: &str): &str from x { // c\n    return r\"a\\\" + b'z' + 0x1F + 1.5 + 0..8 /// doc\n}\n"
	var got []string
	for _, tk := range scan(text) {
		if cl := class(text, tk); cl != classNone {
			got = append(got, fmt.Sprintf("%s:%d", text[tk.Start:tk.End], cl))
		}
	}
	want := `export:3 str:5 str:5 from:4 // c:0 return:3 r"a\":1 b'z':1 0x1F:2 1.5:2 0:2 8:2 /// doc:0`
	if strings.Join(got, " ") != want {
		t.Errorf("classes\n%s\nwant\n%s", strings.Join(got, " "), want)
	}
	// Doc comments are exactly ///.
	for src, doc := range map[string]bool{"/// a": true, "// a": false, "//// a": false, "///": true} {
		if toks := scan(src); len(toks) != 1 || toks[0].Doc != doc {
			t.Errorf("%q: doc %v, want %v", src, toks, doc)
		}
	}
	// An unterminated literal ends at its line; a """ text spans lines.
	toks := scan("\"abc\nx\n\"\"\"\n a\n\"\"\"\ny")
	var kinds []string
	for _, tk := range toks {
		kinds = append(kinds, fmt.Sprint(tk.Kind))
	}
	if strings.Join(kinds, ",") != "2,0,2,0" {
		t.Errorf("kinds %v", kinds)
	}
}

func TestDeclScan(t *testing.T) {
	text := `import util
import j = std.json

/// T doc.
export linear struct T[A] {
    export a: A
    b: int
    invariant b >= 0
}

packed(4) struct P {
    x: u8
}

enum E: u8 {
    One = 1
    Two(n: int)
}

export error Bad(code: int)
interface Shape {
    Area(&self): f64
}

impl Shape for T[int] {
    Area(&self): f64 {
        return 1.0
    }
}

type Alias = []int
const N: int = 4
var G: int = 0
extern Puts(s: *u8): i32

export T[A].Get(&self): A {
    if true {
        return self.a
    }
    return self.a
}

F[X](x: X): X {
    return x
}

test "adds" {
    _ = F(1)
}
gen derive.Eq(T)
`
	ds := decls(text, scan(text))
	var got []string
	for _, d := range ds {
		s := fmt.Sprintf("%s/%s/%d", d.Owner, d.Name, d.Kind)
		if d.Exported {
			s += "!"
		}
		for _, c := range d.Children {
			s += " " + c.Name
		}
		got = append(got, s)
	}
	want := []string{
		"/T/23! a b", "/P/23 x", "/E/10 One Two", "/Bad/23!", "/Shape/11 Area",
		"/impl Shape for T[int]/3 Area", "/Alias/26", "/N/14", "/G/13", "/Puts/12",
		"T/Get/6!", "/F/12", `/test "adds"/12`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("decls\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// The method's extent is its whole body.
	for _, d := range ds {
		if d.Name == "Get" && !strings.HasSuffix(text[d.Start:d.End], "return self.a\n}") {
			t.Errorf("Get's extent: %q", text[d.Start:d.End])
		}
	}
	// Imports bind their last segment or their alias.
	if imps := imports(text, code(scan(text))); imps["util"] != "util" || imps["j"] != "std.json" || len(imps) != 2 {
		t.Errorf("imports %v", imps)
	}
}

func TestLocalDecl(t *testing.T) {
	text := "F(a: int, const b: int): int {\n    c := a\n    var d := b\n    for i, e in xs {\n        _ = i + e\n    }\n    return c + d\n}\n"
	ct := code(scan(text))
	inLoop := strings.Index(text, "_ = i")
	for _, name := range []string{"a", "b", "c", "d", "i", "e"} {
		off := len(text)
		if name == "i" || name == "e" {
			// A loop's bindings are visible in its body only.
			if _, ok := localDecl(text, ct, 0, len(ct)-1, off, name); ok {
				t.Errorf("local %s is visible after its loop", name)
			}
			off = inLoop
		}
		at, ok := localDecl(text, ct, 0, len(ct)-1, off, name)
		if !ok || text[at:at+len(name)] != name {
			t.Errorf("local %s: %d %v", name, at, ok)
			continue
		}
		// The declaration is the first occurrence of the name as a word.
		if first := strings.Index(text, name+" "); first >= 0 && name == "c" && at != strings.Index(text, "c :=") {
			t.Errorf("local c at %d", at)
		}
	}
	if _, ok := localDecl(text, ct, 0, len(ct)-1, len(text), "xs"); ok {
		t.Error("xs is not declared here")
	}
}

func TestURIPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		for uri, want := range map[string]string{
			"file:///C:/Mk/a%20b.mk": `C:\Mk\a b.mk`,
			"file:///c%3A/Mk/x.mk":   `c:\Mk\x.mk`,
		} {
			if got, ok := uriPath(uri); !ok || got != want {
				t.Errorf("uriPath(%s) = %q, want %q", uri, got, want)
			}
		}
		if got := pathURI(`C:\Mk\a b.mk`); got != "file:///C:/Mk/a%20b.mk" {
			t.Errorf("pathURI: %s", got)
		}
	} else {
		if got, ok := uriPath("file:///tmp/a%20b.mk"); !ok || got != "/tmp/a b.mk" {
			t.Errorf("uriPath: %q", got)
		}
		if got := pathURI("/tmp/a b.mk"); got != "file:///tmp/a%20b.mk" {
			t.Errorf("pathURI: %s", got)
		}
	}
	if _, ok := uriPath("untitled:Untitled-1"); ok {
		t.Error("a non-file URI mapped to a path")
	}
}

func TestPositionEncodings(t *testing.T) {
	l := newLines("ab\né😀x\n")
	// x is byte 9 of the text: line 1, byte column 6, UTF-16 column 3.
	if p := l.pos(9, true); p != (Position{1, 6}) {
		t.Errorf("utf-8 pos %+v", p)
	}
	if p := l.pos(9, false); p != (Position{1, 3}) {
		t.Errorf("utf-16 pos %+v", p)
	}
	if off := l.off(Position{1, 3}, false); off != 9 {
		t.Errorf("utf-16 off %d", off)
	}
	if off := l.off(Position{1, 99}, false); off != 10 {
		t.Errorf("clamped off %d, want the line's end 10", off)
	}
	if off := l.off(Position{7, 0}, true); off != len(l.s) {
		t.Errorf("off past the last line %d", off)
	}
	if p := l.endPos(false); p != (Position{2, 0}) {
		t.Errorf("end %+v", p)
	}
}
