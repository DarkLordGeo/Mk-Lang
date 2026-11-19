// Why: the front-end speed budget is measured on a large plain program, and
// the same text must check under the test harness and the real std.
// What: TestWriteBenchProgram and the generator of the program.
// How: writes about 100,000 lines of plain Mk in a shallow import DAG, with
// the language core only (structs, enums, match, optionals, user error
// sets, try and else, records, slices, loops, methods, calls across
// modules) and no std call. A line is one newline of the text, blank lines
// included, as the other front-end benchmarks count it.

package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// genModule is one module of the generated program.
type genModule struct {
	path    string   // import path: "base", "lib3", "app"
	imports []string // import paths, in order
	files   []string // file texts
	depth   int      // import depth: base 0, libs 1, app 2
}

// genProgram is the generated program, modules in dependency order.
type genProgram struct {
	mods  []*genModule
	lines int // newlines over every file
}

// genLibs is the number of library modules between base and app, all at
// depth 1, so stage 5 has that many modules to run at once.
const genLibs = 8

// genBlocksPerFile keeps each file near 2,700 lines.
const genBlocksPerFile = 24

// baseBlock is base's block j: a shared struct, an enum and errors that
// every library uses.
func baseBlock(b *strings.Builder, j int) {
	fmt.Fprintf(b, `export struct Pair%[1]d {
    export lo: int
    export hi: int
}

export enum Kind%[1]d {
    Small(n: int)
    Big(n: int, m: int)
    Zero
}

export error Over%[1]d(by: int)
export error Under%[1]d

export Pair%[1]d.Width(&self): int {
    return self.hi - self.lo
}

export Pair%[1]d.Widen(mut self, k: int) {
    self.lo -= k
    self.hi += k
}

export Classify%[1]d(n: int): Kind%[1]d {
    if n == 0 {
        return Kind%[1]d.Zero
    }
    if n < 100 {
        return Kind%[1]d.Small(n: n)
    }
    return Kind%[1]d.Big(n: n / 100, m: n %% 100)
}

export Bound%[1]d(n: int, p: &Pair%[1]d): Over%[1]d|Under%[1]d!int {
    if n > p.hi {
        fail Over%[1]d(by: n - p.hi)
    }
    if n < p.lo {
        fail Under%[1]d
    }
    return n - p.lo
}

`, j)
}

// libBlock is library module l's block i, over base's block j.
func libBlock(b *strings.Builder, l, i, j int) {
	fmt.Fprintf(b, `export struct P%[1]d {
    export x: int
    export y: int
    export tag: str
}

export enum Shape%[1]d {
    Circle(r: int)
    Rect(w: int, h: int)
    Empty
}

export error Bad%[1]d(code: int)
export error Gone%[1]d

export P%[1]d.Sum(&self): int {
    return self.x + self.y
}

export P%[1]d.Scale(mut self, k: int) {
    self.x *= k
    self.y *= k
}

Area%[1]d(s: Shape%[1]d): int {
    match s {
        .Circle(r) {
            return 3 * r * r
        }
        .Rect(w, h) {
            return w * h
        }
        .Empty {
            return 0
        }
    }
}

Find%[1]d(xs: []int, want: int): ?int {
    for k, x in xs {
        if x == want {
            return some(k)
        }
    }
    return none
}

Spread%[1]d(xs: []int): (lo: int, hi: int) {
    var lo := %[2]d
    var hi := %[2]d
    for x in xs {
        if x < lo {
            lo = x
        }
        if x > hi {
            hi = x
        }
    }
    return (lo, hi)
}

Step%[1]d(n: int): Bad%[1]d|Gone%[1]d!int {
    if n < 0 {
        fail Bad%[1]d(code: n)
    }
    if n == 0 {
        fail Gone%[1]d
    }
    return n * %[3]d + 1
}

Weigh%[1]d(k: base.Kind%[4]d): int {
    match k {
        .Small(n) {
            return n
        }
        .Big(n, m) {
            return n * 100 + m
        }
        .Zero {
            return 0
        }
    }
}

export Use%[1]d(xs: []int, p: &P%[1]d, n: int): Bad%[1]d|Gone%[1]d|base.Over%[4]d|base.Under%[4]d!int {
    var total := 0
    for x in xs {
        if x > p.x && x < p.y {
            total += x
        } else if x == p.x {
            total -= 1
        }
    }
    var q := P%[1]d(x: p.x, y: p.y, tag: p.tag)
    q.Scale(%[3]d)
    total += q.Sum()
    if at := Find%[1]d(xs, p.x) {
        total += at
    }
    (lo, hi) := Spread%[1]d(xs)
    var r := base.Pair%[4]d(lo: lo, hi: hi)
    r.Widen(1)
    total += r.Width()
    v := try Step%[1]d(total)
    w := Step%[1]d(v - n) else e {
        match e {
            Bad%[1]d(code: c) {
                return c
            }
            Gone%[1]d {
                return 0
            }
        }
    }
    total += Area%[1]d(Shape%[1]d.Rect(w: w, h: 2)) + Area%[1]d(Shape%[1]d.Empty)
    total += Weigh%[1]d(base.Classify%[4]d(total))
    var m := 0
    for m < n {
        m += 1
        if m == %[2]d {
            continue
        }
        total += m
    }
    for k in 0..n {
        total += xs[k] * k
    }
    return try base.Bound%[4]d(total, &r)
}

`, i, (i*7+l)%50, l+2, j)
}

// appBlock is app's block i: it drives library l's block k.
func appBlock(b *strings.Builder, i, l, k int) {
	fmt.Fprintf(b, `Drive%[1]d(xs: []int, n: int): int {
    p := lib%[2]d.P%[3]d(x: n, y: n * 3, tag: "t%[1]d")
    v := lib%[2]d.Use%[3]d(xs, &p, n) else {
        return -1
    }
    var s := lib%[2]d.Shape%[3]d.Circle(r: v)
    match s {
        .Circle(r) {
            return r + p.Sum()
        }
        .Rect(w, h) {
            return w - h
        }
        .Empty {
            return 0
        }
    }
}

`, i, l, k)
}

// generateProgram builds the program of about target lines.
func generateProgram(target int) *genProgram {
	// Line costs of one block, counted on the templates.
	var sb strings.Builder
	baseBlock(&sb, 0)
	baseLines := strings.Count(sb.String(), "\n")
	sb.Reset()
	libBlock(&sb, 0, 0, 0)
	libLines := strings.Count(sb.String(), "\n")
	sb.Reset()
	appBlock(&sb, 0, 0, 0)
	appLines := strings.Count(sb.String(), "\n")

	// base takes about 5%, app about 6%, the libraries the rest.
	nBase := max(1, target/20/baseLines)
	// app files each use every library: whole files of blocks only.
	nApp := max(1, target*6/100/appLines/genBlocksPerFile) * genBlocksPerFile
	perLib := max(1, (target-nBase*baseLines-nApp*appLines)/genLibs/libLines)

	p := &genProgram{}
	files := func(n int, header string, block func(*strings.Builder, int)) []string {
		var out []string
		for start := 0; start < n; start += genBlocksPerFile {
			var fb strings.Builder
			fb.WriteString(header)
			for i := start; i < min(n, start+genBlocksPerFile); i++ {
				block(&fb, i)
			}
			out = append(out, fb.String())
		}
		return out
	}
	p.mods = append(p.mods, &genModule{path: "base", files: files(nBase, "", func(b *strings.Builder, j int) { baseBlock(b, j) })})
	for l := 0; l < genLibs; l++ {
		p.mods = append(p.mods, &genModule{path: fmt.Sprintf("lib%d", l), imports: []string{"base"}, depth: 1,
			files: files(perLib, "import base\n\n", func(b *strings.Builder, i int) { libBlock(b, l, i, i%nBase) })})
	}
	var hdr strings.Builder
	var imps []string
	for l := 0; l < genLibs; l++ {
		fmt.Fprintf(&hdr, "import lib%d\n", l)
		imps = append(imps, fmt.Sprintf("lib%d", l))
	}
	hdr.WriteString("\n")
	app := &genModule{path: "app", imports: imps, depth: 2,
		files: files(nApp, hdr.String(), func(b *strings.Builder, i int) { appBlock(b, i, i%genLibs, (i/genLibs)%perLib) })}
	p.mods = append(p.mods, app)
	for _, m := range p.mods {
		for _, f := range m.files {
			p.lines += strings.Count(f, "\n")
		}
	}
	return p
}

// writeProgram writes the program as a package for `mk check`: pkg.mk, the
// app module as the root (with a Main), each other module in its own
// directory.
func writeProgram(dir string, p *genProgram) error {
	manifest := `const Package = pkg.Package(
    name: "gen100k",
    bins: [pkg.Bin(name: "gen100k", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
`
	if err := os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(manifest), 0o644); err != nil {
		return err
	}
	for _, m := range p.mods {
		sub := dir
		if m.path != "app" {
			sub = filepath.Join(dir, m.path)
		}
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return err
		}
		for i, f := range m.files {
			if m.path == "app" && i == 0 {
				f += "export Main(sys: &System) {\n    xs := [1, 2, 3, 4, 5, 6, 7, 8]\n    _ = Drive0(xs[0..8], 8)\n}\n"
			}
			name := filepath.Join(sub, fmt.Sprintf("%s_%02d.mk", m.path, i))
			if err := os.WriteFile(name, []byte(f), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// TestWriteBenchProgram writes the generated program to $MK_GEN100K_DIR
// (which must not exist yet) for `mk check --timings`; skipped otherwise.
func TestWriteBenchProgram(t *testing.T) {
	dir := os.Getenv("MK_GEN100K_DIR")
	if dir == "" {
		t.Skip("set MK_GEN100K_DIR to write the generated program")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("%s exists", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := generateProgram(100_000)
	if err := writeProgram(dir, p); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d lines in %d modules written to %s", p.lines, len(p.mods), dir)
}
