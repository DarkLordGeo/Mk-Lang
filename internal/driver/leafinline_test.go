// Why: release builds inline small leaf functions into loops in codegen
// only; that rewrite must never change what a program does, what the
// session keeps, or what a warm build rebuilds.
// What: TestLeafInline_SameProgram, TestLeafInline_InstanceBodies,
// TestLeafInline_OnlySamePackAndFile and TestLeafInline_CalleeEditMisses,
// over leafInlineSrc.
// How: a release build with the inlined leaves (loopversion.go's
// codegenBody and leafOf) and one without give the same output, stderr up
// to a panic's backtrace and exit code on inputs that pass and inputs that
// trap inside a leaf (the trap's message and site included), and the kept
// bodies print the same; a leaf of another pack or file stays out of line,
// and an edit of only an inlined leaf rebuilds its caller's pack through a
// warm cache.

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"mk/internal/types"
)

const leafInlineSrc = `// Leaves called in loops (item 11.2g): spectralnorm's A, a leaf whose
// add traps when its second argument says so, and rule 4's product.
A(i: int, j: int): int {
    return (i + j) * (i + j + 1) / 2 + i + 1
}

B(x: int, k: int): u8 {
    return Cast[u8](x) + Cast[u8](k)
}

M(x: int, y: int): int {
    return x * y / 2 + 4611686018427387904
}

// A generic caller: the inliner and the second range pass run on its
// mono instances (Walk[u8] and Walk[i64]), where Step's add traps when k
// is near MAX.
Step(x: int, k: int): int {
    return x * 2 + k
}

Walk[T](xs: []T, k: int): int {
    var r := 0
    for i in 0..xs.Len() {
        r = r +% Step(i, k)
    }
    return r
}

Sum(v: []f64): f64 {
    n := v.Len()
    var s := 0.0
    for i in 0..n {
        for j in 0..n {
            s = s + 1.0 / Cast[f64](A(i, j)) * v[j]
        }
    }
    return s
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    var out := sys.Stdout()
    n := Cast[int](try sys.args[1].ParseInt())
    k := Cast[int](try sys.args[2].ParseInt())
    x := Cast[int](try sys.args[3].ParseInt())
    y := Cast[int](try sys.args[4].ParseInt())
    v := try sys.arena.Slice[f64](n)
    for i in 0..n {
        v[i] = Cast[f64](i % 5 + 1)
    }
    s := Cast[i64](Sum(v[..]) * 1000000.0)
    var r := 0
    for i in 0..250 {
        r = r + int(B(i, k))
    }
    m := M(x & 3, y & 4611686018427387903)
    g := Cast[int](try sys.args[5].ParseInt())
    b8 := try sys.arena.Slice[u8](40)
    b64 := try sys.arena.Slice[i64](80)
    w := Walk[u8](b8[..], g) +% Walk[i64](b64[..], g)
    try Print(mut out, "{s} {r} {m} {w}\n", (s, r, m, w))
}
`

// TestLeafInline_SameProgram compares leafInlineSrc's builds with the
// inlined leaves and without.
func TestLeafInline_SameProgram(t *testing.T) {
	lvCompareSwitch(t, &noLeafInline, leafInlineSrc, [][]string{
		{"40", "6", "2", "4611686018427387903", "0"}, // passes: B's last sum is 255, M is MAX
		{"7", "0", "-1", "-1", "0"},                  // M's product 3 * (2^62 - 1) traps
		{"3", "7", "1", "5", "0"},                    // B's add traps at i = 249
		{"3", "300", "1", "5", "0"},                  // B's Cast[u8](k) traps
		{"3", "-1", "1", "5", "0"},                   // B's Cast[u8](k) traps below
		{"3", "0", "1", "5", "-9223372036854775808"}, // passes: Walk's sums wrap, Step's do not
		{"3", "0", "1", "5", "9223372036854775797"},  // Step's add traps in Walk[u8] at i = 6
		{"3", "0", "1", "5", "9223372036854775707"},  // Step's add traps in Walk[i64] at i = 51
	})
}

// leafLL builds root (a file or a package directory) release on
// x86_64-windows, with the leaf inliner on or off, and returns the
// functions the packs' IR defines: symbol to its pack's name and its body.
func leafLL(t *testing.T, root string, off bool) map[string][2]string {
	t.Helper()
	noLeafInline = off
	defer func() { noLeafInline = false }()
	var mu sync.Mutex
	lls := map[string]string{}
	s, err := NewSession(Config{Root: root, Target: types.X86_64Windows, Profile: types.Release, Tools: needTools(t), CacheDir: t.TempDir(),
		TestHooks: TestHooks{OnLL: func(name, ll string) { mu.Lock(); lls[name] = ll; mu.Unlock() }}})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "li"+exeSuffix(types.X86_64Windows)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
	}
	fns := map[string][2]string{}
	for pack, ll := range lls {
		for _, def := range strings.Split(ll, "\ndefine ")[1:] {
			at := strings.IndexByte(def, '@')
			if at < 0 {
				continue
			}
			sym := def[at+1:]
			sym = strings.TrimPrefix(sym[:strings.IndexByte(sym, '(')], `"`)
			sym = strings.TrimSuffix(sym, `"`)
			body := def
			if end := strings.Index(def, "\n}\n"); end >= 0 {
				body = def[:end]
			}
			fns[sym] = [2]string{pack, body}
		}
	}
	return fns
}

// leafCalls reports whether body holds a call of the function sym.
func leafCalls(body, sym string) bool {
	return strings.Contains(body, "@"+sym+"(") || strings.Contains(body, `@"`+sym+`"(`)
}

// TestLeafInline_InstanceBodies: the inliner runs on mono instance bodies:
// both of leafInlineSrc's Walk instances call Step with the inliner off and
// not with it on, and the two builds run the same
// (TestLeafInline_SameProgram, including traps inside Step in each
// instance).
func TestLeafInline_InstanceBodies(t *testing.T) {
	file := filepath.Join(t.TempDir(), "li.mk")
	if err := os.WriteFile(file, []byte(leafInlineSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, off := range []bool{false, true} {
		fns := leafLL(t, file, off)
		n := 0
		for sym, f := range fns {
			if !strings.HasPrefix(sym, "mk.main.Walk") {
				continue
			}
			n++
			if got := leafCalls(f[1], "mk.main.Step"); got != off {
				t.Errorf("inliner off %v: instance %s calls Step: %v", off, sym, got)
			}
		}
		if n != 2 {
			t.Errorf("inliner off %v: %d Walk instances defined, want 2", off, n)
		}
	}
}

// TestLeafInline_OnlySamePackAndFile: a leaf is inlined only from a
// caller of its own pack (leafOf, so an edit of the leaf rebuilds the
// caller's pack) and its own file (callLeaf). A two-file package has pairs of a
// caller Ck and its leaf Lk in main.mk and pairs Dk, Fk with the leaf in
// leaf.mk; the module's packs are cut by instance name (mono.Partition),
// so some pairs fall in two packs. Each pair's call is gone exactly when
// both rules hold, and the three cases that test a rule each occur.
func TestLeafInline_OnlySamePackAndFile(t *testing.T) {
	const pairs = 24
	leaf := func(name string) string {
		return name + "(i: int, k: int): int {\n    return i + k\n}\n\n"
	}
	caller := func(name, leaf string) string {
		return name + "(n: int, k: int): int {\n    var r := 0\n    for i in 0..n {\n        r = r +% " + leaf + "(i, k)\n    }\n    return r\n}\n\n"
	}
	var mainMk, leafMk, sum strings.Builder
	for k := range pairs {
		c, l, d, f := fmt.Sprintf("C%02d", k), fmt.Sprintf("L%02d", k), fmt.Sprintf("D%02d", k), fmt.Sprintf("F%02d", k)
		mainMk.WriteString(caller(c, l) + leaf(l) + caller(d, f))
		leafMk.WriteString(leaf(f))
		fmt.Fprintf(&sum, "    r = r +%% %s(n, k) +%% %s(n, k)\n", c, d)
	}
	mainMk.WriteString("export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {\n    var out := sys.Stdout()\n" +
		"    n := Cast[int](try sys.args[1].ParseInt())\n    k := Cast[int](try sys.args[2].ParseInt())\n    var r := 0\n" +
		sum.String() + "    try Print(mut out, \"{r}\\n\", (r,))\n}\n")
	dir := writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"li"`,
			"bins": `[pkg.Bin(name: "li", root: ".", link_script: "", cpu: "", subsystem: "console")]`}),
		"main.mk": mainMk.String(),
		"leaf.mk": leafMk.String(),
	})
	fns := leafLL(t, dir, false)
	var cases [2][2]int // [same pack][same file]
	for k := range pairs {
		for _, p := range [][2]string{{fmt.Sprintf("C%02d", k), fmt.Sprintf("L%02d", k)}, {fmt.Sprintf("D%02d", k), fmt.Sprintf("F%02d", k)}} {
			c, l := fns["mk.main."+p[0]], fns["mk.main."+p[1]]
			if c[0] == "" || l[0] == "" {
				t.Fatalf("%s or %s is not defined in any pack", p[0], p[1])
			}
			samePack, sameFile := c[0] == l[0], p[0][0] == 'C'
			cases[leafB2i(samePack)][leafB2i(sameFile)]++
			if want, got := samePack && sameFile, !leafCalls(c[1], "mk.main."+p[1]); got != want {
				t.Errorf("%s calls %s: inlined %v, want %v (same pack %v, same file %v)", p[0], p[1], got, want, samePack, sameFile)
			}
		}
	}
	t.Logf("pairs [same pack][same file]: %v", cases)
	if cases[1][1] == 0 || cases[0][1] == 0 || cases[1][0] == 0 {
		t.Errorf("the pairs [same pack][same file] are %v: a case is missing, so a rule goes untested (rename the functions)", cases)
	}
}

func leafB2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestLeafInline_CalleeEditMisses: building, then editing only the leaf A
// and building again on the same cache, gives the edited program's output,
// the same as a build of the edit on an empty cache.
func TestLeafInline_CalleeEditMisses(t *testing.T) {
	tools := needTools(t)
	dir := t.TempDir()
	cache := t.TempDir()
	file := filepath.Join(dir, "li.mk")
	build := func(src, cacheDir, name string) string {
		if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := NewSession(Config{Root: file, Target: types.X86_64Windows, Profile: types.Release, Tools: tools, CacheDir: cacheDir})
		if err != nil {
			t.Fatal(err)
		}
		r, paths, err := s.Build(filepath.Join(dir, name+exeSuffix(types.X86_64Windows)))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Report.Diags) > 0 || len(paths) != 1 {
			t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
		}
		o, e, c := runExe(t, types.X86_64Windows, paths[0], "", "9", "6", "2", "5", "0")
		return o + "|" + e + "|" + strconv.Itoa(c)
	}
	edited := strings.Replace(leafInlineSrc, "/ 2 + i + 1\n", "/ 2 + i + 2\n", 1)
	if edited == leafInlineSrc {
		t.Fatal("the edit did not apply")
	}
	before := build(leafInlineSrc, cache, "before")
	warm := build(edited, cache, "warm")
	cold := build(edited, t.TempDir(), "cold")
	if warm != cold {
		t.Errorf("the edit of A through a warm cache gives %q, an empty cache %q", warm, cold)
	}
	if warm == before {
		t.Errorf("the edit of A did not change the output: %q", warm)
	}
	t.Logf("before %q, after %q", before, warm)
}
