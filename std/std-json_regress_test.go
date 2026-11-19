// Why: past bugs in the JSON codec and formatter (f32 double rounding, u64
// round trips, empty structs, escape offsets, bad numbers) must stay fixed.
// What: regression tests TestSTDJSON_*, and
// the generator's error paths.
// How: the shipping gen json.Codec and std.json run through the native
// driver; the generator's failure paths run through mk check.

package std

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// jsonCheck writes src as main.mk of a package and runs mk check on it.
func jsonCheck(t *testing.T, src string) (string, int) {
	t.Helper()
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
	return mkCmd(t, pkgDir, stdDir, "check", "--target=x86_64-windows", ".")
}

// jsonLines splits a program's stdout into lines.
func jsonLines(s string) []string {
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n")
}

// jsonF32Main decodes each record into F and prints "ok <y>" with y's
// shortest f32 digits, or "mismatch".
const jsonF32Main = `import js = std.json

struct F {
    export y: f32
}

gen js.Codec(F)

export Main(sys: &System): js.Error|Utf8Error|ReadError|WriteError|ParseError!void {
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](1 << 22)
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
        v := try js.Parse(sys.arena, text)
        match F.FromJson(sys.arena, &v) {
            ok(x) {
                try Print(mut out, "ok {y}\n", (y: x.y))
            }
            err(_) {
                try out.Str("mismatch\n")
            }
        }
    }
}
`

// jsonF32Inputs is the f32 rounding corpus: every f32 midpoint class (exact, just
// above, just below) over random binades, subnormals, the smallest
// midpoint 2^-150 and the overflow midpoint 2^128 - 2^103, both signs; plus
// random decimals of 1 to 30 digits across f32's range.
func jsonF32Inputs(seed uint64, n int) []string {
	r := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
	exact := func(m float64) string {
		// 160 digits after the point is exact for every f32 midpoint (at
		// most 131 significant digits); trim the mantissa's zeros.
		s := strconv.FormatFloat(m, 'e', 160, 64)
		k := strings.IndexByte(s, 'e')
		mant := strings.TrimRight(s[:k], "0")
		mant = strings.TrimSuffix(mant, ".")
		return mant + s[k:]
	}
	variants := func(m float64) []string {
		s := exact(m)
		k := strings.IndexByte(s, 'e')
		mant, exp := s[:k], s[k:]
		above := mant + "1" + exp
		if !strings.Contains(mant, ".") {
			above = mant + ".1" + exp
		}
		// below: the last digit (nonzero after trimming) less one, then 9s.
		last := mant[len(mant)-1]
		below := mant[:len(mant)-1] + string(last-1) + "999" + exp
		if !strings.Contains(mant, ".") {
			below = mant[:len(mant)-1] + string(last-1) + ".999" + exp
		}
		return []string{s, above, below}
	}
	var out []string
	mid := func(b uint32) float64 {
		lo, hi := math.Float32frombits(b), math.Float32frombits(b+1)
		return (float64(lo) + float64(hi)) / 2
	}
	special := []float64{
		math.Ldexp(1, -150),                     // between 0 and the smallest subnormal
		math.Ldexp(1, 128) - math.Ldexp(1, 103), // f32's largest and 2^128
		mid(0x3F800000),                         // 1 + 2^-24, the first known failure
		mid(0x007FFFFF),                         // the largest subnormal and the smallest normal
		mid(0x00000001), mid(0x00800000 - 2),    // subnormal midpoints
	}
	for _, m := range special {
		out = append(out, variants(m)...)
	}
	for range n {
		var b uint32
		switch r.IntN(4) {
		case 0: // subnormal
			b = r.Uint32N(0x007FFFFF)
		case 1: // the top binade, below the largest
			b = 0x7F000000 + r.Uint32N(0x007FFFFF)
		default:
			b = r.Uint32N(0x7F7FFFFF)
		}
		for _, s := range variants(mid(b)) {
			if r.IntN(2) == 0 {
				s = "-" + s
			}
			out = append(out, s)
		}
	}
	for range n {
		var sb strings.Builder
		if r.IntN(2) == 0 {
			sb.WriteByte('-')
		}
		nd := 1 + r.IntN(30)
		sb.WriteByte(byte('1' + r.IntN(9)))
		if nd > 1 {
			sb.WriteByte('.')
			for range nd - 1 {
				sb.WriteByte(byte('0' + r.IntN(10)))
			}
		}
		fmt.Fprintf(&sb, "e%d", r.IntN(95)-50)
		out = append(out, sb.String())
	}
	return out
}

// TestSTDJSON_F32CorrectlyRounded: gen json.Codec reads an f32 field
// as the text's correctly rounded f32, the same bits as Go's
// strconv.ParseFloat(s, 32), and Mismatch where Go reports a range error.
func TestSTDJSON_F32CorrectlyRounded(t *testing.T) {
	nums := jsonF32Inputs(11, 400)
	// The corpus must reach the double rounding: inputs where the f64's
	// rounding to f32 differs from the text's f32.
	twice := 0
	for _, s := range nums {
		d, err1 := strconv.ParseFloat(s, 64)
		f, err2 := strconv.ParseFloat(s, 32)
		if err1 == nil && err2 == nil && float32(d) != float32(f) {
			twice++
		}
	}
	if twice < 100 {
		t.Fatalf("only %d inputs round differently through f64", twice)
	}
	t.Logf("%d of %d inputs round differently through f64", twice, len(nums))
	var inputs []string
	for _, s := range nums {
		inputs = append(inputs, `{"y":`+s+`}`)
	}
	for _, mode := range []string{"dev", "release"} {
		t.Run(mode, func(t *testing.T) {
			var flags []string
			if mode == "release" {
				flags = []string{"--release"}
			}
			exe := jsonBuild(t, map[string]string{"main.mk": jsonF32Main}, nil, flags...)
			r := runNative(t, exe, jsonRecords(inputs))
			if r.code != 0 {
				t.Fatalf("exit %d\n%s", r.code, r.stderr)
			}
			got := jsonLines(r.stdout)
			if len(got) != len(nums) {
				t.Fatalf("%d lines for %d inputs", len(got), len(nums))
			}
			bad := 0
			for k, s := range nums {
				want := "mismatch"
				if f, err := strconv.ParseFloat(s, 32); err == nil {
					want = strconv.FormatUint(uint64(math.Float32bits(float32(f))), 16)
				}
				g := got[k]
				if y, ok := strings.CutPrefix(g, "ok "); ok {
					f, err := strconv.ParseFloat(y, 32)
					if err != nil {
						g = "unparsable " + y
					} else {
						g = strconv.FormatUint(uint64(math.Float32bits(float32(f))), 16)
					}
				}
				if g != want {
					bad++
					if bad <= 10 {
						t.Errorf("%s: got %s (%s), want %s", s, g, got[k], want)
					}
				}
			}
			if bad > 0 {
				t.Errorf("%d of %d inputs differ", bad, len(nums))
			}
		})
	}
}

// TestSTDJSON_U64RoundTrip: a u64 or uint FormatJson writes at full
// width reads back through FromJson (the u64 cases of jsonCodecCases run
// the same path inside All).
func TestSTDJSON_U64RoundTrip(t *testing.T) {
	src := `import js = std.json

struct U {
    export big: u64
    export n: uint
    export l: List[u64]
}

gen js.Codec(U)

Show(sys: &System, text: str into sys): js.Error|WriteError!void {
    var out := sys.Stdout()
    v := try js.Parse(sys.arena, text)
    match U.FromJson(sys.arena, &v) {
        ok(u) {
            var t := Text.New(sys.arena)
            var w := t.Writer()
            try u.FormatJson(mut w)
            // The parsed value keeps views of its text in sys.arena, so
            // the text is copied there, out of the local t.
            again := try js.Parse(sys.arena, try sys.arena.Dup(t.Str()))
            u2 := try U.FromJson(sys.arena, &again)
            try out.Str("ok ")
            try u2.FormatJson(mut out)
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

export Main(sys: &System): js.Error|WriteError!void {
    try Show(sys, "{\"big\":18446744073709551615,\"n\":18446744073709551615,\"l\":[0,9223372036854775808,18446744073709551614]}")
    try Show(sys, "{\"big\":18446744073709551616,\"n\":0,\"l\":[]}")
    try Show(sys, "{\"big\":0,\"n\":0,\"l\":[99999999999999999999]}")
    try Show(sys, "{\"big\":-1,\"n\":0,\"l\":[]}")
    try Show(sys, "{\"big\":1.0,\"n\":0,\"l\":[]}")
    try Show(sys, "{\"big\":1e2,\"n\":0,\"l\":[]}")
    try Show(sys, "{\"big\":\"1\",\"n\":0,\"l\":[]}")
}
`
	exe := jsonBuild(t, map[string]string{"main.mk": src}, nil)
	r := runNative(t, exe, "")
	wantRun(t, r, 0, `ok {"big":18446744073709551615,"n":18446744073709551615,"l":[0,9223372036854775808,18446744073709551614]}`+"\n"+
		strings.Repeat("mismatch -1\n", 6))
	// Go's encoding/json agrees on the accepted value.
	var g struct{ Big uint64 }
	if err := json.Unmarshal([]byte(`{"Big":18446744073709551615}`), &g); err != nil || g.Big != math.MaxUint64 {
		t.Errorf("Go reference: %v %d", err, g.Big)
	}
}

// TestSTDJSON_EmptyStruct: gen json.Codec on a struct with no fields
// compiles, writes {}, reads {} (members ignored), and refuses a non-object.
func TestSTDJSON_EmptyStruct(t *testing.T) {
	src := `import js = std.json

struct E {}

struct Holder {
    export e: E
    export es: List[E]
}

gen js.Codec(E)
gen js.Codec(Holder)

Try(sys: &System, text: str into sys): js.Error|WriteError!void {
    var out := sys.Stdout()
    v := try js.Parse(sys.arena, text)
    match Holder.FromJson(sys.arena, &v) {
        ok(h) {
            try h.FormatJson(mut out)
            try out.Str("\n")
        }
        err(_) {
            try out.Str("mismatch\n")
        }
    }
}

export Main(sys: &System): js.Error|WriteError!void {
    var out := sys.Stdout()
    v := try js.Parse(sys.arena, "{}")
    e := try E.FromJson(sys.arena, &v)
    try e.FormatJson(mut out)
    try out.Str("\n")
    w := try js.Parse(sys.arena, "[]")
    match E.FromJson(sys.arena, &w) {
        ok(_) {
            try out.Str("ok\n")
        }
        err(_) {
            try out.Str("mismatch\n")
        }
    }
    try Try(sys, "{\"e\":{\"x\":1},\"es\":[{},{}]}")
    try Try(sys, "{\"e\":null,\"es\":[]}")
}
`
	exe := jsonBuild(t, map[string]string{"main.mk": src}, nil)
	r := runNative(t, exe, "")
	wantRun(t, r, 0, "{}\nmismatch\n"+`{"e":{},"es":[{},{}]}`+"\nmismatch\n")
}

// TestSTDJSON_EscapeOffsets: Parse reports the first fault in a
// string, so a bad escape is Syntax at its backslash even when a control
// byte or the end of the text follows. The offsets are written by
// hand; the model must agree with them too.
func TestSTDJSON_EscapeOffsets(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"\q` + "\x01" + `"`, "syntax 1"},
		{`"\q`, "syntax 1"},
		{`"\x"`, "syntax 1"},
		{`"\U0041"`, "syntax 1"},
		{`"\`, "syntax 2"},
		{`"\u12`, "syntax 5"},
		{`"\u12"`, "syntax 1"},
		{`"\u12G4"`, "syntax 1"},
		{`"\ud800"`, "syntax 1"},
		{`"\ud800`, "syntax 7"},
		{`"\ud800\`, "syntax 8"},
		{`"\ud800\n"`, "syntax 1"},
		{`"\ud800%u0041"`, "syntax 1"},
		{`"\udc00\ud800"`, "syntax 1"},
		{`"\ud800\udbff"`, "syntax 1"},
		{`"\ud800\udc0`, "syntax 12"},
		{`"ab%ud800%udc00`, "syntax 15"},
		{`"a\nb` + "\x01" + `"`, "syntax 5"},
		{`["\"",1,"\t` + "\x1f" + `"]`, "syntax 11"},
		{`"%u00e9\q"`, "syntax 7"},
		{`{"k\q":1}`, "syntax 3"},
		{`{"a":"\q",` + "\x01", "syntax 6"},
		{`"%ud83d%ude00\/%u00E9"`, `ok "😀/é"`},
		{`"%u0001\"\\\b\f\n\r\t"`, `ok "%u0001\"\\\b\f\n\r\t"`},
	}
	// %u stands for a backslash and a u: a tool on the way to this file
	// rewrote such escapes into their characters, which tested nothing.
	for k := range cases {
		cases[k].in = strings.ReplaceAll(cases[k].in, "%u", string(rune(0x5C))+"u")
		cases[k].want = strings.ReplaceAll(cases[k].want, "%u", string(rune(0x5C))+"u")
	}
	var inputs []string
	for _, c := range cases {
		inputs = append(inputs, c.in)
		if got := jmOutcome([]byte(c.in)); got != c.want {
			t.Errorf("model %q: got %s, want %s", c.in, got, c.want)
		}
	}
	exe := jsonBuild(t, map[string]string{"main.mk": jsonDriver}, nil)
	r := runNative(t, exe, jsonRecords(inputs))
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	got := jsonLines(r.stdout)
	if len(got) != len(cases) {
		t.Fatalf("%d lines for %d cases:\n%s", len(got), len(cases), r.stdout)
	}
	for k, c := range cases {
		if got[k] != c.want {
			t.Errorf("Mk %q: got %s, want %s", c.in, got[k], c.want)
		}
	}
}

// TestSTDJSON_FormatRefusesBadNumber: Value.Format fails with
// WriteError on a hand-built Number whose text is not a JSON number, alone
// or deep in a tree, and writes every valid one as it is; what it writes is
// JSON.
func TestSTDJSON_FormatRefusesBadNumber(t *testing.T) {
	src := `import js = std.json

Show(sys: &System, v: &js.Value): WriteError!void {
    var t := Text.New(sys.arena)
    var w := t.Writer()
    var out := sys.Stdout()
    match v.Format(mut w) {
        ok(_) {
            try out.Str(t.Str())
            try out.Str("\n")
        }
        err(_) {
            try out.Str("writeerror\n")
        }
    }
}

Arr(a: &Arena, x: str into a, y: str into a): OutOfMemory!js.Value from a, x, y {
    var l := List[js.Value].New(a)
    try l.Push(js.Value.Number(text: x))
    try l.Push(js.Value.Number(text: y))
    return js.Value.Array(items: l)
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    bad := ["", "nan", "01", "1.", ".5", "+1", "1e", "1e+", "-", " 1", "1 ", "0x1", "inf", "1_0", "--1"]
    for k in 0..bad.Len() {
        n := js.Value.Number(text: bad[k])
        try Show(sys, &n)
        a := try Arr(sys.arena, "1", bad[k])
        try Show(sys, &a)
    }
    good := ["0", "-0", "1", "-0.5e+3", "1E400", "123456789012345678901234567890", "4.9e-324", "0.0e0"]
    for k in 0..good.Len() {
        n := js.Value.Number(text: good[k])
        try Show(sys, &n)
        a := try Arr(sys.arena, good[k], "2")
        try Show(sys, &a)
    }
    var ms := List[js.Member].New(sys.arena)
    inner := try Arr(sys.arena, "3", "x")
    try ms.Push(js.Member(key: "k", value: inner))
    o := js.Value.Object(members: ms)
    try Show(sys, &o)
}
`
	exe := jsonBuild(t, map[string]string{"main.mk": src}, nil)
	r := runNative(t, exe, "")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
	}
	got := jsonLines(r.stdout)
	bad := []string{"", "nan", "01", "1.", ".5", "+1", "1e", "1e+", "-", " 1", "1 ", "0x1", "inf", "1_0", "--1"}
	good := []string{"0", "-0", "1", "-0.5e+3", "1E400", "123456789012345678901234567890", "4.9e-324", "0.0e0"}
	var want []string
	for range bad {
		want = append(want, "writeerror", "writeerror")
	}
	for _, g := range good {
		want = append(want, g, "["+g+",2]")
	}
	want = append(want, "writeerror")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, line := range got {
		if line != "writeerror" && !json.Valid([]byte(line)) {
			t.Errorf("Format wrote %q, which is not JSON", line)
		}
	}
	// Go refuses the same: json.Marshal of a json.Number that is not one.
	for _, b := range []string{"nan", "01", "1."} {
		if _, err := json.Marshal(json.Number(b)); err == nil {
			t.Errorf("Go reference: Marshal(Number(%q)) succeeded", b)
		}
	}
}

// TestSTDJSON_CodecGenErrors runs the generator's failure paths:
// each one-type program must
// check clean, or give E0903 at the offending field (at the type for an
// enum with no variants, at the gen line for a T that is no struct or
// enum) with the codec's message. The one case the
// Gen API cannot catch, a field whose struct type has no gen line, is
// pinned as E0905 until the generator has a way to ask.
func TestSTDJSON_CodecGenErrors(t *testing.T) {
	cases := []struct {
		name, decls, at, code, msg string
	}{
		{"empty struct", "struct E {}\n\ngen js.Codec(E)\n", "", "", ""},
		{"scalar", "gen js.Codec(i32)\n", "gen js.Codec(i32)", "E0903", "only a struct or an enum"},
		{"enum with no variants", "enum Z {}\n\ngen js.Codec(Z)\n", "enum Z {}", "E0903", "no variants"},
		{"enum f32 and char", "enum Q {\n    A(x: f32)\n    B(c: char)\n}\n\ngen js.Codec(Q)\n", "    B(c: char)", "E0903", "no JSON form"},
		{"unexported field", "struct H {\n    export a: i32\n    b: i32\n}\n\ngen js.Codec(H)\n", "    b: i32", "E0903", "every field must be exported"},
		{"char", "struct C {\n    export c: char\n}\n\ngen js.Codec(C)\n", "    export c: char", "E0903", "no JSON form"},
		{"array", "struct A {\n    export c: [2]i32\n}\n\ngen js.Codec(A)\n", "    export c: [2]i32", "E0903", "no JSON form"},
		{"optional char", "struct O {\n    export c: ?List[char]\n}\n\ngen js.Codec(O)\n", "    export c: ?List[char]", "E0903", "no JSON form"},
		{"json.Value", "struct V {\n    export v: js.Value\n}\n\ngen js.Codec(V)\n", "    export v: js.Value", "E0903", "a json.Value field has no codec"},
		{"list of json.Value", "struct L {\n    export v: List[js.Value]\n}\n\ngen js.Codec(L)\n", "    export v: List[js.Value]", "E0903", "a json.Value field has no codec"},
		{"enum payload char", "enum P {\n    A\n    B(c: char)\n}\n\ngen js.Codec(P)\n", "    B(c: char)", "E0903", "no JSON form"},
		{"user enum named Value", "enum Value {\n    X\n    Y(n: i32)\n}\n\nstruct W {\n    export v: Value\n}\n\ngen js.Codec(Value)\ngen js.Codec(W)\n", "", "", ""},
		{"struct field without a gen line (R-J9)", "struct In {\n    export x: i32\n}\n\nstruct Out {\n    export i: In\n}\n\ngen js.Codec(Out)\n", "", "E0905", "FromJson"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "import js = std.json\n\n" + c.decls + "\nexport Main(sys: &System) {\n}\n"
			out, status := jsonCheck(t, src)
			if c.code == "" {
				if status != 0 {
					t.Fatalf("mk check: exit %d\n%s", status, out)
				}
				return
			}
			if status == 0 {
				t.Fatalf("mk check passed, want %s", c.code)
			}
			first := strings.SplitN(out, "\n", 2)[0]
			if !strings.Contains(first, c.code) || !strings.Contains(out, c.msg) {
				t.Fatalf("want %s with %q, got:\n%s", c.code, c.msg, out)
			}
			if c.at != "" {
				line := 1 + strings.Count(src[:strings.Index(src, c.at)], "\n")
				if !strings.HasPrefix(first, fmt.Sprintf("main.mk:%d:", line)) {
					t.Errorf("want the error at main.mk:%d, got:\n%s", line, out)
				}
			}
			if c.code == "E0903" && strings.Contains(out, "E0905") {
				t.Errorf("E0905 beside E0903:\n%s", out)
			}
		})
	}
}
