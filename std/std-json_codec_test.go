// Why: gen json.Codec writes code into the user's module, so its output
// shape, round trips and every error path need pinning.
// What: codec tests over structs, an enum with payloads, optionals, lists,
// slices and nested codec types; the emitted code against a golden file;
// round trips; every Mismatch path; std.json at compile time.
// How: every test runs the shipping generator through std.json, whose quotes
// name json.X (a generator's quote names its own module's items). The test
// programs import std.json as js, so the emitted json.X names resolve
// without the user's module having a name json.

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const jsonCodecTypes = `import js = std.json

enum Shape {
    Empty
    Circle(r: f64)
    Rect(w: i32, h: u8)
}

struct Point {
    export x: i64
    export y: f32
}

struct All {
    export name: str
    export ok: bool
    export small: u8
    export big: u64
    export neg: i16
    export shapes: List[Shape]
    export pts: []Point
    export ints: ?List[int]
    export maybe: ?Point
    export none_here: ?str
    export real: f64
}

gen js.Codec(Shape)
gen js.Codec(Point)
gen js.Codec(All)
`

// jsonCodecMain decodes each record (as jsonDriver frames them) into All,
// and prints "ok <FormatJson>" or "mismatch <offset>", "syntax <offset>".
const jsonCodecMain = `
export Main(sys: &System): js.Error|Utf8Error|ReadError|WriteError|ParseError!void {
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](1 << 20)
    n := try inp.ReadAll(buf)
    data := buf[0..n]
    var out := sys.Stdout()
    var i := 0
    for i < n {
        var j := i
        for data[j] != 0x0A {
            j += 1
        }
        head := try str.From(data[i..j])
        len := Cast[int](try head.ParseInt())
        text := try str.From(data[j + 1..j + 1 + len])
        i = j + 1 + len
        match js.Parse(sys.arena, text) {
            ok(v) {
                match All.FromJson(sys.arena, &v) {
                    ok(x) {
                        try out.Str("ok ")
                        try x.FormatJson(mut out)
                        try out.Str("\n")
                    }
                    err(js.Mismatch(offset)) {
                        try Print(mut out, "mismatch {offset}\n", (offset: offset))
                    }
                    err(_) {
                        try out.Str("other\n")
                    }
                }
            }
            err(js.Syntax(offset)) {
                try Print(mut out, "syntax {offset}\n", (offset: offset))
            }
            err(_) {
                try out.Str("other\n")
            }
        }
    }
}
`

// jsonCodecCase is one input and the line the program must print.
type jsonCodecCase struct{ in, want string }

const jsonCodecFull = `{"name":"a\"b","ok":true,"small":255,"big":9223372036854775807,"neg":-32768,"shapes":["Empty",{"Circle":{"r":1.5}},{"Rect":{"w":-3,"h":4}}],"pts":[{"x":1,"y":2.5}],"ints":[1,2,3],"maybe":{"x":-9,"y":0},"none_here":null,"real":1e300}`

var jsonCodecCases = []jsonCodecCase{
	// Floats print their shortest round-trip digits:
	// 1e300 comes back as 1e+300.
	{jsonCodecFull, "ok " + strings.Replace(jsonCodecFull, "1e300", "1e+300", 1)},
	// Member order, whitespace, unknown members, duplicates (last wins), a
	// missing optional and null for one.
	{` { "real" : 0.5 , "pts" : [ ] , "x" : [1, {"y": 2}], "shapes" : [ ], "neg" : 1, "big" : 0, "small" : 0, "ok" : false, "name" : "first", "name" : "last" , "ints": null} `,
		`ok {"name":"last","ok":false,"small":0,"big":0,"neg":1,"shapes":[],"pts":[],"ints":null,"maybe":null,"none_here":null,"real":0.5}`},
	// Missing required field.
	{`{"ok":true,"small":1,"big":1,"neg":1,"shapes":[],"pts":[],"real":1}`, "mismatch -1"},
	// Wrong kinds.
	{`[]`, "mismatch -1"},
	{`{"name":1,"ok":true,"small":1,"big":1,"neg":1,"shapes":[],"pts":[],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":"true","small":1,"big":1,"neg":1,"shapes":[],"pts":[],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":{},"pts":[],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":[],"pts":[],"real":"1"}`, "mismatch -1"},
	// Integer ranges: u8 past 255, i16 past -32768, u64 negative, a fraction.
	{`{"name":"n","ok":true,"small":256,"big":1,"neg":1,"shapes":[],"pts":[],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":true,"small":-1,"big":1,"neg":1,"shapes":[],"pts":[],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":true,"small":1,"big":1,"neg":-32769,"shapes":[],"pts":[],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":true,"small":1,"big":-1,"neg":1,"shapes":[],"pts":[],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":true,"small":1.5,"big":1,"neg":1,"shapes":[],"pts":[],"real":1}`, "mismatch -1"},
	// u64 reads through u64, so every value FormatJson writes reads back:
	// 2^63 and 2^64 - 1 are ok, 2^64, -0 and 1e0 are Mismatch.
	{`{"name":"n","ok":true,"small":1,"big":9223372036854775808,"neg":1,"shapes":[],"pts":[],"real":1}`,
		`ok {"name":"n","ok":true,"small":1,"big":9223372036854775808,"neg":1,"shapes":[],"pts":[],"ints":null,"maybe":null,"none_here":null,"real":1}`},
	{`{"name":"n","ok":true,"small":1,"big":18446744073709551615,"neg":1,"shapes":[],"pts":[],"real":1}`,
		`ok {"name":"n","ok":true,"small":1,"big":18446744073709551615,"neg":1,"shapes":[],"pts":[],"ints":null,"maybe":null,"none_here":null,"real":1}`},
	{`{"name":"n","ok":true,"small":1,"big":18446744073709551616,"neg":1,"shapes":[],"pts":[],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":true,"small":1,"big":-0,"neg":1,"shapes":[],"pts":[],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":true,"small":1,"big":1e0,"neg":1,"shapes":[],"pts":[],"real":1}`, "mismatch -1"},
	// f32 is the text's correctly rounded f32, not the f64's rounding
	// (no double rounding): just above the midpoint 1 + 2^-24 is 1.0000001, the
	// midpoint itself is 1 (ties to even), and just below f32's overflow
	// midpoint 2^128 - 2^103 is f32's largest value.
	{`{"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":[],"pts":[{"x":1,"y":1.0000000596046447753906251},{"x":2,"y":1.000000059604644775390625},{"x":3,"y":-1.0000000596046447753906251},{"x":4,"y":3.40282356779733661637539395458142568447e38}],"real":1}`,
		`ok {"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":[],"pts":[{"x":1,"y":1.0000001},{"x":2,"y":1},{"x":3,"y":-1.0000001},{"x":4,"y":3.4028235e+38}],"ints":null,"maybe":null,"none_here":null,"real":1}`},
	{`{"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":[],"pts":[{"x":1,"y":340282356779733661637539395458142568448}],"real":1}`, "mismatch -1"},
	// f32 past its range (Cast would panic); f64 past its range.
	{`{"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":[],"pts":[{"x":1,"y":3.5e38}],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":[],"pts":[{"x":1,"y":3.4028234663852886e38}],"real":1}`,
		`ok {"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":[],"pts":[{"x":1,"y":3.4028235e+38}],"ints":null,"maybe":null,"none_here":null,"real":1}`},
	{`{"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":[],"pts":[],"real":1e400}`, "mismatch -1"},
	// Enum: unknown variant, payload variant as a string, two members, a
	// missing payload field, a unit variant as an object.
	{`{"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":["Square"],"pts":[],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":["Circle"],"pts":[],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":[{"Circle":{"r":1},"Rect":{"w":1,"h":1}}],"pts":[],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":[{"Rect":{"w":1}}],"pts":[],"real":1}`, "mismatch -1"},
	{`{"name":"n","ok":true,"small":1,"big":1,"neg":1,"shapes":[{"Empty":{}}],"pts":[],"real":1}`, "mismatch -1"},
	// Parse errors still come from Parse.
	{`{"name":`, "syntax 8"},
}

func TestSTDJSON_CodecRoundTrip(t *testing.T) {
	var inputs []string
	for _, c := range jsonCodecCases {
		inputs = append(inputs, c.in)
	}
	for _, mode := range []string{"dev", "release"} {
		t.Run(mode, func(t *testing.T) {
			var flags []string
			if mode == "release" {
				flags = []string{"--release"}
			}
			exe := jsonBuild(t, map[string]string{"main.mk": jsonCodecTypes + jsonCodecMain}, nil, flags...)
			r := runNative(t, exe, jsonRecords(inputs))
			if r.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
			if len(got) != len(jsonCodecCases) {
				t.Fatalf("%d lines for %d cases:\n%s", len(got), len(jsonCodecCases), r.stdout)
			}
			for k, c := range jsonCodecCases {
				if got[k] != c.want {
					t.Errorf("case %d %q:\n got %s\nwant %s", k, c.in, got[k], c.want)
				}
			}
		})
	}
}

// TestSTDJSON_CodecGolden pins the emitted code of the round-trip types
// (the gen view mk writes beside the module) to std/json/testdata/codec.golden.
// MK_JSON_UPDATE=1 rewrites the golden file.
func TestSTDJSON_CodecGolden(t *testing.T) {
	root := t.TempDir()
	stdDir := jsonStd(t, root, nil)
	pkgDir := filepath.Join(root, "p")
	files := map[string]string{
		"main.mk": jsonCodecTypes + "\nexport Main(sys: &System) {\n}\n",
		"pkg.mk": "const Package = pkg.Package(\n    name: \"p\",\n    bins: [pkg.Bin(name: \"p\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n" +
			"    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n",
	}
	for p, text := range files {
		dst := filepath.Join(pkgDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	out, status := mkCmd(t, pkgDir, stdDir, "check", "--target=x86_64-windows", ".")
	if status != 0 {
		t.Fatalf("mk check: exit %d\n%s", status, out)
	}
	b, err := os.ReadFile(filepath.Join(pkgDir, "gen", "x86_64-windows", "gen.mk"))
	if err != nil {
		t.Fatal(err)
	}
	// The view's last line carries a digest of the output; the golden pins
	// the code, so the digest line is dropped.
	got := string(b)
	if k := strings.LastIndex(got, "\n// mk gen view "); k >= 0 {
		got = got[:k+1]
	}
	golden := filepath.Join("json", "testdata", "codec.golden")
	if os.Getenv("MK_JSON_UPDATE") == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (MK_JSON_UPDATE=1 writes it)", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("emitted code differs from %s (MK_JSON_UPDATE=1 rewrites it after reading the diff):\n%s", golden, got)
	}
}

// TestSTDJSON_CodecListOfStr is the shape the codec emits for List[str],
// []str and ?List[str] fields. The codec reads a str with a match on
// .Text, not Value.Str: the borrow checker refuses Value.Str's result
// pushed into a List inside a loop (E0604 "stores a loan of xs into xs").
func TestSTDJSON_CodecListOfStr(t *testing.T) {
	src := `import js = std.json

struct Tags {
    export list: List[str]
    export slice: []str
    export maybe: ?List[str]
}

gen js.Codec(Tags)

export Main(sys: &System): js.Error|WriteError!void {
    v := try js.Parse(sys.arena, "{\"list\":[\"a\",\"b\"],\"slice\":[\"c\"],\"maybe\":[]}")
    x := try Tags.FromJson(sys.arena, &v)
    var out := sys.Stdout()
    try x.FormatJson(mut out)
}
`
	root := t.TempDir()
	stdDir := jsonStd(t, root, nil)
	pkgDir := filepath.Join(root, "p")
	files := map[string]string{
		"main.mk": src,
		"pkg.mk": "const Package = pkg.Package(\n    name: \"p\",\n    bins: [pkg.Bin(name: \"p\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n" +
			"    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n",
	}
	for p, text := range files {
		dst := filepath.Join(pkgDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	exe := filepath.Join(root, "p.exe")
	out, status := mkCmd(t, pkgDir, stdDir, "build", "-o", exe, ".")
	if status != 0 {
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	r := runNative(t, exe, "")
	wantRun(t, r, 0, `{"list":["a","b"],"slice":["c"],"maybe":[]}`)
}

// TestSTDJSON_CompileTime runs Parse, Get, Int, Float, Str and Format in
// the evaluator: S is computed by mk at compile time (in EvalArena), and the
// program only prints it.
func TestSTDJSON_CompileTime(t *testing.T) {
	src := `import std.json

Summary(a: &Arena): str from a {
    v := json.Parse(a, " {\"a\" : [1, -2.5e1, \"x\\ny\\u00e9\\ud83d\\ude00\"], \"b\" : null, \"a\" : true} ") else {
        return "parse failed"
    }
    var t := Text.New(a)
    var w := t.Writer()
    v.Format(mut w) else {
        return "format failed"
    }
    var last := 0
    if v.Get("a").Must().Str().IsNone() {
        last = 1
    }
    var off := -1
    match json.Parse(a, "[1,2") {
        ok(_) {}
        err(json.Syntax(offset)) {
            off = offset
        }
        err(_) {}
    }
    n := json.Parse(a, "[12, 2.5, \"s\"]") else {
        return "parse 2 failed"
    }
    var acc := 0
    match n {
        .Array(items) {
            xs := items.Items()
            acc = Cast[int](xs[0].Int().Must()) + Cast[int](xs[1].Float().Must() * 2.0) + xs[2].Str().Must().Len()
        }
        _ {}
    }
    return a.Fmt("{f} {last} {off} {acc}\n", (f: t.Str(), last: last, off: off, acc: acc)) else {
        return "fmt failed"
    }
}

const S = Summary(EvalArena())

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try out.Str(S)
}
`
	exe := jsonBuild(t, map[string]string{"main.mk": src}, nil)
	r := runNative(t, exe, "")
	wantRun(t, r, 0, `{"a":[1,-2.5e1,"x\nyé😀"],"b":null,"a":true} 1 4 18`+"\n")
}

// TestSTDJSON_CodecThroughStd runs the shipping generator, gen json.Codec
// through std.json itself, on the cookbook's Config shape. Its output names
// json.Json, json.Value and json.Mismatch, which resolve because a
// generator's quote names its own module, and the user's module also
// imports std.json as json: the pinned names must not collide with it.
func TestSTDJSON_CodecThroughStd(t *testing.T) {
	src := `import std.json

struct Config {
    export name: str
    export port: int
    export debug: ?bool
}

gen json.Codec(Config)

export Main(sys: &System): json.Error|WriteError!void {
    v := try json.Parse(sys.arena, "{\"port\":8080,\"name\":\"api\",\"extra\":[1]}")
    c := try Config.FromJson(sys.arena, &v)
    var out := sys.Stdout()
    try Print(mut out, "{name} {port} ", (name: c.name, port: c.port))
    try c.FormatJson(mut out)
}
`
	root := t.TempDir()
	stdDir := jsonStd(t, root, nil)
	pkgDir := filepath.Join(root, "p")
	files := map[string]string{
		"main.mk": src,
		"pkg.mk": "const Package = pkg.Package(\n    name: \"p\",\n    bins: [pkg.Bin(name: \"p\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n" +
			"    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n",
	}
	for p, text := range files {
		dst := filepath.Join(pkgDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	exe := filepath.Join(root, "p.exe")
	out, status := mkCmd(t, pkgDir, stdDir, "build", "-o", exe, ".")
	if status != 0 {
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	r := runNative(t, exe, "")
	wantRun(t, r, 0, `api 8080 {"name":"api","port":8080,"debug":null}`)
}
