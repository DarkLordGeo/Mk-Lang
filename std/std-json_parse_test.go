// Why: std.json's parser and formatter must give the model's answer, offsets
// included, on every input.
// What: differential tests of Parse, Format and the accessors against the Go
// model of std-json_model_test.go, and TestSTDJSON_JSONTestSuite.
// How: a native driver (dev and release) reads framed records: edge cases,
// seeded documents with one-byte mutations, the depth limit, JSONTestSuite.

package std

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// jsonDriver reads records "<length>\n<bytes>" from standard input and
// prints one line per record: "ok <Value.Format>", "syntax <offset>",
// "depth <offset>", "utf8" (str.From refused the bytes) or "other". After an
// ok top-level array it prints "acc" and, per element, its Int, Float and Str
// (or - for none), so the accessors run on every number form.
const jsonDriver = `import std.json

export Main(sys: &System): json.Error|Utf8Error|ReadError|WriteError|ParseError!void {
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](1 << 27)
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
        rec := data[j + 1..j + 1 + len]
        i = j + 1 + len
        match str.From(rec) {
            ok(text) {
                match json.Parse(sys.arena, text) {
                    ok(v) {
                        try out.Str("ok ")
                        try v.Format(mut out)
                        try out.Str("\n")
                        try Acc(mut out, &v)
                    }
                    err(json.Syntax(offset)) {
                        try Print(mut out, "syntax {offset}\n", (offset: offset))
                    }
                    err(json.Depth(offset)) {
                        try Print(mut out, "depth {offset}\n", (offset: offset))
                    }
                    err(_) {
                        try out.Str("other\n")
                    }
                }
            }
            err(_) {
                try out.Str("utf8\n")
            }
        }
    }
}

Acc(out: mut Writer, v: &json.Value): WriteError!void {
    match v {
        .Array(items) {
            try out.Str("acc")
            xs := items.Items()
            for k in 0..xs.Len() {
                x := &xs[k]
                match x.Int() {
                    some(n) {
                        try Print(out, " i={n}", (n: n))
                    }
                    none {
                        try out.Str(" i=-")
                    }
                }
                match x.Float() {
                    some(f) {
                        try Print(out, " f={f}", (f: f))
                    }
                    none {
                        try out.Str(" f=-")
                    }
                }
                match x.Str() {
                    some(s) {
                        try Print(out, " s={n}", (n: s.Len()))
                    }
                    none {
                        try out.Str(" s=-")
                    }
                }
            }
            try out.Str("\n")
        }
        _ {}
    }
}
`

// jsonStd returns a std directory for the Mk programs: a copy of
// MK_JSON_BASE (a snapshot of a whole std, for runs while other std files
// are mid-edit) with this tree's std/json, or this tree's
// std with fixtures.
func jsonStd(t *testing.T, root string, fixtures map[string]string) string {
	t.Helper()
	base := os.Getenv("MK_JSON_BASE")
	if base == "" {
		return copyStd(t, root, fixtures)
	}
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stdDir, "json")); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(stdDir, "json"), os.DirFS("json")); err != nil {
		t.Fatal(err)
	}
	for p, text := range fixtures {
		dst := filepath.Join(stdDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return stdDir
}

// jsonBuild writes files (paths relative to a package root; main.mk is the
// bin's root module) and builds the package; it returns the executable.
func jsonBuild(t *testing.T, files map[string]string, fixtures map[string]string, flags ...string) string {
	t.Helper()
	root := t.TempDir()
	stdDir := jsonStd(t, root, fixtures)
	pkgDir := filepath.Join(root, "p")
	files["pkg.mk"] = "const Package = pkg.Package(\n    name: \"p\",\n    bins: [pkg.Bin(name: \"p\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n" +
		"    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n"
	for p, text := range files {
		dst := filepath.Join(pkgDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(root, "p")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, ".")
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	out, status := mkCmd(t, pkgDir, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// jsonRecords encodes inputs for jsonDriver.
func jsonRecords(inputs []string) string {
	var sb strings.Builder
	for _, in := range inputs {
		sb.WriteString(strconv.Itoa(len(in)))
		sb.WriteByte('\n')
		sb.WriteString(in)
	}
	return sb.String()
}

// jsonWant is the driver's expected output for inputs, from the model.
func jsonWant(inputs []string) []string {
	var lines []string
	for _, in := range inputs {
		o := jmOutcome([]byte(in))
		lines = append(lines, o)
		if !strings.HasPrefix(o, "ok ") {
			continue
		}
		v, _ := jmParse([]byte(in))
		if v.kind != jmArray {
			continue
		}
		var sb strings.Builder
		sb.WriteString("acc")
		for _, x := range v.items {
			sb.WriteString(jmAccessors(x))
		}
		lines = append(lines, sb.String())
	}
	return lines
}

// jmAccessors is what the driver prints for one element. Float is checked
// by value, not text, in jsonCompare (Mk prints shortest round-trip digits).
func jmAccessors(x *jmValue) string {
	s := ""
	if x.kind == jmNumber {
		if n, err := strconv.ParseInt(x.text, 10, 64); err == nil {
			s += fmt.Sprintf(" i=%d", n)
		} else {
			s += " i=-"
		}
		if f, err := strconv.ParseFloat(x.text, 64); err == nil {
			s += fmt.Sprintf(" f=%s", strconv.FormatUint(math.Float64bits(f), 16))
		} else {
			s += " f=-"
		}
	} else {
		s += " i=- f=-"
	}
	if x.kind == jmText {
		s += fmt.Sprintf(" s=%d", len(x.text))
	} else {
		s += " s=-"
	}
	return s
}

// jsonCompare checks the driver's output against the model, line by line;
// an acc line's f= fields compare by the bits of the parsed float.
func jsonCompare(t *testing.T, inputs []string, got string) {
	t.Helper()
	want := jsonWant(inputs)
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(got, "\r\n", "\n"), "\n"), "\n")
	bad := 0
	for k := range max(len(want), len(lines)) {
		var w, g string
		if k < len(want) {
			w = want[k]
		}
		if k < len(lines) {
			g = lines[k]
		}
		if strings.HasPrefix(g, "acc") {
			g = jsonFloatBits(g)
		}
		if w != g {
			bad++
			if bad <= 10 {
				t.Errorf("line %d:\n got %q\nwant %q", k, g, w)
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d lines differ", bad, len(want))
	}
}

// jsonFloatBits rewrites each f=<decimal> of an acc line as the hex bits of
// the f64 it parses to.
func jsonFloatBits(line string) string {
	fs := strings.Fields(line)
	for k, f := range fs {
		if strings.HasPrefix(f, "f=") && f != "f=-" {
			x, err := strconv.ParseFloat(f[2:], 64)
			if err != nil {
				fs[k] = "f=?" + f[2:]
				continue
			}
			fs[k] = "f=" + strconv.FormatUint(math.Float64bits(x), 16)
		}
	}
	return strings.Join(fs, " ")
}

// jsonInputs is the differential corpus.
func jsonInputs() []string {
	in := append([]string{}, jmEdgeCases...)
	in = append(in, jmRandomInputs(7, 1500)...)
	in = append(in, jmNested("[", "]", 512), jmNested("[", "]", 513), jmNested(`{"a":`, "}", 512), jmNested(`{"a":`, "}", 513),
		`[0,-0,1,-1,9223372036854775807,9223372036854775808,-9223372036854775808,-9223372036854775809,1.5,1e2,1E400,-1e-400,4.9e-324,"x",null,true,[],{}]`,
		`[1.7976931348623157e308,1.7976931348623159e308,0.1,123456789012345678,2.2250738585072011e-308,1e-7,100000000000000000000000]`)
	return in
}

func TestSTDJSON_ParseMatchesModel(t *testing.T) {
	inputs := jsonInputs()
	for _, mode := range []string{"dev", "release"} {
		t.Run(mode, func(t *testing.T) {
			var flags []string
			if mode == "release" {
				flags = []string{"--release"}
			}
			exe := jsonBuild(t, map[string]string{"main.mk": jsonDriver}, nil, flags...)
			r := runNative(t, exe, jsonRecords(inputs))
			if r.code != 0 {
				t.Fatalf("exit %d\n%s", r.code, r.stderr)
			}
			jsonCompare(t, inputs, r.stdout)
		})
	}
}

// TestSTDJSON_JSONTestSuite runs nst/JSONTestSuite's test_parsing: every
// y_ file parses, every n_ file fails, and the i_ files are logged. The
// suite is kept under $MK_EVIDENCE (or Mk-evidence beside the repository);
// the test skips when it is absent.
func TestSTDJSON_JSONTestSuite(t *testing.T) {
	ev := os.Getenv("MK_EVIDENCE")
	if ev == "" {
		ev = filepath.Join("..", "..", "Mk-evidence")
	}
	dir := filepath.Join(ev, "jsontestsuite", "test_parsing")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("JSONTestSuite not found at %s: %v", dir, err)
	}
	var names, inputs []string
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, e.Name())
		inputs = append(inputs, string(b))
	}
	exe := jsonBuild(t, map[string]string{"main.mk": jsonDriver}, nil)
	r := runNative(t, exe, jsonRecords(inputs))
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	var outcomes []string
	for _, l := range strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n") {
		if !strings.HasPrefix(l, "acc") {
			outcomes = append(outcomes, l)
		}
	}
	if len(outcomes) != len(names) {
		t.Fatalf("%d outcomes for %d files", len(outcomes), len(names))
	}
	var y, n, iAcc, iRej int
	for k, name := range names {
		got := outcomes[k]
		accepted := strings.HasPrefix(got, "ok ")
		if want := jmOutcome([]byte(inputs[k])); want != got {
			t.Errorf("%s: got %q, model %q", name, got, want)
		}
		switch name[0] {
		case 'y':
			y++
			if !accepted {
				t.Errorf("%s must parse: %s", name, got)
			}
		case 'n':
			n++
			if accepted {
				t.Errorf("%s must fail: %s", name, got)
			}
		default:
			if accepted {
				iAcc++
			} else {
				iRej++
			}
			t.Logf("%s: %s", name, firstN(got, 60))
		}
	}
	t.Logf("JSONTestSuite: %d y_ parsed, %d n_ refused, i_: %d accepted, %d refused", y, n, iAcc, iRej)
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
