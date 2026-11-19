// Why: on x86_64-linux a `become` that passed on a System V byval
// parameter was a musttail call with a byval argument, which LLVM 21
// lowers through the bottom of the caller's frame: in a function with no
// frame that wrote over its return address. codegen now splits such a
// function into a C ABI wrapper and a $tail body (codegen/tailbody.go).
// What: TestBecome_ByvalChain, TestBecome_ByvalAcrossPacks and
// TestBecome_ByvalThroughFnValue.
// How: long chains of becomes passing on structs of 24 to 40 bytes, run in
// dev and release; a become of another pack's function, whose split is a
// fact of the caller's body, under the cache's verify build; and check's
// E0313 for a fn value, which cannot reach a $tail body.

package driver

import (
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

// becomeByvalChain: a frameless forward (G), mutual recursion of five
// million hops (Ping, Pong), a result returned through memory (Same), two
// byval parameters (Two), a struct with float fields (Al) and a 16-byte
// struct that is byval only because six integers took the registers, with
// a stack-passed integer that changes (Spill), each passing its struct on
// unchanged.
const becomeByvalChain = `struct Big {
    a: int
    b: int
    c: int
}

struct Pair {
    a: int
    b: int
}

struct Wide {
    x: f64
    y: int
    z: f64
    u: i32
    v: int
}

H(var b: Big, n: int): int {
    b.a = 99
    b.c = 77
    return b.a + n
}

G(b: Big, n: int): int {
    become H(b, n)
}

K(b: Big, n: int): int {
    r := G(b, n)
    return r * 1000 + b.a * 10 + b.c
}

Ping(b: Big, n: int): int {
    if n == 0 {
        return b.a + b.c
    }
    become Pong(b, n - 1)
}

Pong(b: Big, n: int): int {
    if n == 0 {
        return b.b
    }
    become Ping(b, n - 1)
}

Same(b: Big, n: int): Big {
    if n == 0 {
        return b
    }
    become Same(b, n - 1)
}

Two(p: Big, q: Big, n: int): int {
    if n == 0 {
        return p.a + q.c
    }
    become Two(p, q, n - 1)
}

Al(w: Wide, n: int): int {
    if n == 0 {
        return w.y + w.v
    }
    become Al(w, n - 1)
}

Spill(i1: int, i2: int, i3: int, i4: int, i5: int, i6: int, p: Pair, n: int): int {
    if n == 0 {
        return i1 + i6 + p.a + p.b
    }
    become Spill(i1, i2, i3, i4, i5, i6, p, n - 1)
}

export Main(sys: &System): WriteError!void {
    x := Big(a: 1, b: 2, c: 3)
    k := K(x, 0)
    p := Ping(x, 5000001)
    s := Same(x, 3000000)
    t := Two(x, Big(a: 10, b: 20, c: 30), 3000000)
    w := Al(Wide(x: 5.0, y: 6, z: 1.0, u: 2, v: 5), 3000000)
    q := Spill(1, 2, 3, 4, 5, 6, Pair(a: 7, b: 8), 3000000)
    try sys.Print("K {k} P {p} S {s} T {t} W {w} Q {q} x.a {a}\n", (k: k, p: p, s: s.c, t: t, w: w, q: q, a: x.a))
}
`

// TestBecome_ByvalChain: every chain runs in constant stack and keeps the
// caller's struct, on the host in dev and release (on Linux, the System V
// split; on Windows, Win64's pointers, which never were byval).
func TestBecome_ByvalChain(t *testing.T) {
	tools := needTools(t)
	dir := writeTree(t, map[string]string{"pkg.mk": fixBecomeManifest, "src/main.mk": becomeByvalChain})
	const want = "K 99013 P 2 S 3 T 31 W 11 Q 22 x.a 1\n"
	targets := []types.Target{HostTarget()}
	if linuxRun() && HostTarget().OS == types.OSWindows {
		targets = append(targets, types.X86_64Linux)
	}
	for _, target := range targets {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(target.Name+"/"+prof.String(), func(t *testing.T) {
				s, err := NewSession(Config{Root: dir, Target: target, Profile: prof, Tools: tools, CacheDir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				r, paths, err := s.Build(filepath.Join(t.TempDir(), "app"+exeSuffix(target)))
				if err != nil || len(r.Report.Diags) > 0 || len(paths) != 1 {
					t.Fatalf("build: %v\n%s", err, r.Report.Text(r.Sources))
				}
				out, errOut, code := runExe(t, target, paths[0], "")
				if code != 0 || out != want {
					t.Fatalf("exit %d, stdout %q, stderr %q; want %q", code, out, errOut, want)
				}
			})
		}
	}
}

// TestBecome_ByvalAcrossPacks: app's G turns its plain call of lib.H into a
// become that passes its Big on. lib's source is unchanged, but H is now
// on a become edge, so it is split and lib's pack key must move with app's
// body; the verify build compares every pack, and the program still
// exits 0 (H wrote only its own copy: K adds 6 to pkFiles' -6).
func TestBecome_ByvalAcrossPacks(t *testing.T) {
	tools := needTools(t)
	base := pkFiles()
	base["lib/lib.mk"] += "\nexport struct Big {\n    export a: int\n    export b: int\n    export c: int\n}\n\n" +
		"export H(var b: Big, n: int): int {\n    b.a = 99\n    return b.a + n\n}\n"
	app := base["app/app.mk"]
	app = strings.Replace(app, "import shape\n", "import shape\n\n"+
		"G(b: lib.Big, n: int): int {\n    return lib.H(b, n)\n}\n\n"+
		"K(b: lib.Big): int {\n    r := G(b, 1)\n    return r + b.a - 95\n}\n", 1)
	app = strings.Replace(app, "lib.Counter - 26", "lib.Counter + K(lib.Big(a: 1, b: 2, c: 3)) - 26", 1)
	base["app/app.mk"] = app
	dir := writeTree(t, base)
	out := t.TempDir()
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			pkWrite(t, dir, base)
			cfg := Config{Root: dir, Target: pkTarget(), Profile: prof, Tools: tools, CacheDir: filepath.Join(t.TempDir(), "cache")}
			pkBuild(t, cfg, filepath.Join(out, prof.String()+"-cold"+exeSuffix(pkTarget())))
			e := pkEdit{name: "become-across", file: "app/app.mk", old: "    return lib.H(b, n)\n", new: "    become lib.H(b, n)\n"}
			pkWrite(t, dir, e.apply(t, base))
			exe := filepath.Join(out, prof.String()+"-edit"+exeSuffix(pkTarget()))
			ed := pkBuild(t, cfg, exe)
			if got := strings.Join(ed.emittedMods(), " "); !strings.Contains(got, "app") || !strings.Contains(got, "lib") {
				t.Errorf("emitted %v, want app (G's body) and lib (H is on a become edge)", got)
			}
			pkVerify(t, cfg, filepath.Join(out, prof.String()+"-verify"+exeSuffix(pkTarget())), ed.exe)
			if o, errOut, code := runExe(t, pkTarget(), exe, ""); code != 0 {
				t.Fatalf("exit %d, stdout %q, stderr %q; want 0", code, o, errOut)
			}
		})
	}
}

// TestBecome_ByvalThroughFnValue: a fn value is a function's C ABI entry,
// which a become cannot reach the $tail body through, so passing on a
// parameter that some target passes byval through one is E0313 on every
// target: a Big (System V MEMORY), and a 16-byte Pair after six integers
// (System V's registers run out). A Pair that fits in registers is
// accepted, though Win64 passes it in memory, and so is an 8-byte struct.
func TestBecome_ByvalThroughFnValue(t *testing.T) {
	const big = "struct T {\n    a: int\n    b: int\n    c: int\n}\n"
	const pair = "struct T {\n    a: int\n    b: int\n}\n"
	const word = "struct T {\n    a: i32\n    b: i32\n}\n"
	const six = "i1: int, i2: int, i3: int, i4: int, i5: int, i6: int, "
	const sixArgs = "i1, i2, i3, i4, i5, i6, "
	cases := []struct{ name, typ, lit, pre, preArgs, want string }{
		{"big", big, "T(a: 1, b: 2, c: 3)", "", "", "E0313"},
		{"pair", pair, "T(a: 1, b: 2)", "", "", ""},
		{"pair-spilled", pair, "T(a: 1, b: 2)", six, sixArgs, "E0313"},
		{"word-spilled", word, "T(a: 1, b: 2)", six, sixArgs, ""},
	}
	for _, c := range cases {
		for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
			t.Run(c.name+"/"+target.Name, func(t *testing.T) {
				t.Parallel()
				zeros := strings.Repeat("0, ", strings.Count(c.preArgs, ","))
				main := c.typ + "\nH(" + c.pre + "b: T, n: int): int {\n    return n\n}\n\n" +
					"const Tab: [1]fn(" + strings.Repeat("int, ", strings.Count(c.preArgs, ",")) + "T, int): int = [H]\n\n" +
					"G(" + c.pre + "b: T, n: int): int {\n    become Tab[0](" + c.preArgs + "b, n)\n}\n\n" +
					"export Main(sys: &System): WriteError!void {\n    g := G(" + zeros + c.lit + ", 0)\n" +
					"    try sys.Print(\"{g}\\n\", (g: g))\n}\n"
				dir := writeTree(t, map[string]string{"pkg.mk": fixBecomeManifest, "src/main.mk": main})
				s, err := NewSession(Config{Root: dir, Target: target, CacheDir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				r := s.Check()
				var got []string
				for _, e := range r.Report.Diags {
					got = append(got, e.Diag.Code.String())
				}
				if strings.Join(got, " ") != c.want {
					t.Fatalf("diagnostics %v, want %q\n%s", got, c.want, r.Report.Text(r.Sources))
				}
			})
		}
	}
}
