// Why: release builds add check-free versions of loops in codegen only;
// that rewrite must never change what a program does or what the session
// keeps.
// What: TestLoopVersion_SameProgram and TestLoopVersion_Generated, over
// lvCompare, lvCompareSwitch, lvSameRun and the program generator
// lvGenerated; TestLVSameRunReadsToBacktrace pins lvSameRun.
// How: a release build with the versions (loopversion.go) and one without
// give the same output, stderr up to a panic's backtrace and exit code on
// inputs that pass and inputs that trap (the trap's message and site
// included), and the bodies the session keeps (eval's, borrow's, the
// caches') print the same either way.

package driver

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

const loopVersionSrc = `// A matmul j loop, a stepped copy loop and a checked sum (items 11.2e
// and 11.2f): each traps when its argument says so.
Mat(a: []f64, b: []f64, c: mut []f64, n: int): i64 {
    for i in 0..n {
        for k in 0..n {
            aik := a[i * n + k]
            for j in 0..n {
                c[i * n + j] = c[i * n + j] + aik * b[k * n + j]
            }
        }
    }
    var t := 0.0
    for i in 0..n * n {
        t = t + c[i]
    }
    return Cast[i64](t)
}

Copy(src: []u8, dst: mut []u8, at: int, k: int): int {
    var pos := at
    for j in k..src.Len() {
        dst[pos] = src[j]
        pos += 1
    }
    return pos
}

Sum(xs: []i64, from: int): i64 {
    var s: i64 = 0
    for i in from..xs.Len() {
        s += xs[i]
    }
    return s
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    var out := sys.Stdout()
    n := Cast[int](try sys.args[1].ParseInt())
    short := Cast[int](try sys.args[2].ParseInt())
    big := Cast[i64](try sys.args[3].ParseInt())
    at := Cast[int](try sys.args[4].ParseInt())
    nn := n * n
    a := try sys.arena.Slice[f64](nn)
    b := try sys.arena.Slice[f64](nn)
    c := try sys.arena.Slice[f64](nn)
    for i in 0..nn {
        a[i] = Cast[f64](i % 7)
        b[i] = Cast[f64](i % 5)
    }
    m := Mat(a[..], b[..nn - short], mut c[..], n)
    src := try sys.arena.Slice[u8](40)
    dst := try sys.arena.Slice[u8](40)
    for i in 0..40 {
        src[i] = Cast[u8](i)
    }
    p := Copy(src[..], mut dst[..], at, 3)
    xs := try sys.arena.Slice[i64](1000)
    for i in 0..1000 {
        xs[i] = Cast[i64](i)
    }
    xs[500] = big
    s := Sum(xs[..], 1)
    try Print(mut out, "{m} {p} {s}\n", (m, p, s))
}
`

// lvCompare builds src release with the versions and without, checks that
// the executables differ (some loop was versioned) and that the bodies the
// session keeps print the same, then runs both on x86_64-windows with each
// argument list: stdout, stderr up to the backtrace and the exit code
// must agree, on x86_64-windows and (with MK_LINUX=1) x86_64-linux.
func lvCompare(t *testing.T, src string, argsets [][]string) {
	lvCompareSwitch(t, &noLoopVersions, src, argsets)
}

// lvCompareSwitch is lvCompare for the rewrite *off turns off: the versions
// (noLoopVersions) or the inlined leaves (noLeafInline).
func lvCompareSwitch(t *testing.T, off *bool, src string, argsets [][]string) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) { lvCompareOn(t, off, target, src, argsets) })
	}
}

// lvCompareOn is lvCompare for one target; x86_64-linux runs only with
// MK_LINUX=1 (runExe's runner skips it otherwise).
func lvCompareOn(t *testing.T, sw *bool, target types.Target, src string, argsets [][]string) {
	t.Helper()
	runner(t, target)
	tools := needTools(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "lv.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	type built struct {
		exe    string
		bodies map[string]string
	}
	build := func(off bool) built {
		*sw = off
		defer func() { *sw = false }()
		s, err := NewSession(Config{Root: file, Target: target, Profile: types.Release, Tools: tools, CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		name := "on" + exeSuffix(target)
		if off {
			name = "off" + exeSuffix(target)
		}
		r, paths, err := s.Build(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Report.Diags) > 0 || len(paths) != 1 {
			t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
		}
		out := built{exe: paths[0], bodies: map[string]string{}}
		w := s.w
		p := &mir.Printer{Types: w.tab, Items: w.items}
		for id := 1; id < w.items.NumItems(); id++ {
			key := w.items.Get(types.ItemID(id)).Key.String()
			if !strings.HasPrefix(key, "main.") {
				continue
			}
			w.sem5.mu.RLock()
			e := w.sem5.bodies[types.ItemID(id)]
			w.sem5.mu.RUnlock()
			if e != nil && e.mir != nil {
				out.bodies[key] = p.Print(e.mir)
			}
		}
		return out
	}
	on, off := build(false), build(true)
	if x, y := readFile(t, on.exe), readFile(t, off.exe); x == y {
		t.Error("the executables are the same with the versions on and off: no loop was versioned, or no leaf inlined")
	}
	if len(on.bodies) < 2 {
		t.Fatalf("found %d main bodies", len(on.bodies))
	}
	for k, b := range on.bodies {
		if off.bodies[k] != b {
			t.Errorf("%s: the kept body differs with the versions on:\n%s\noff:\n%s", k, b, off.bodies[k])
		}
	}
	for _, args := range argsets {
		o1, e1, c1 := runExe(t, target, on.exe, "", args...)
		o2, e2, c2 := runExe(t, target, off.exe, "", args...)
		if !lvSameRun(o1, e1, c1, o2, e2, c2) {
			t.Errorf("%v: versions on: %q %q exit %d; off: %q %q exit %d", args, o1, e1, c1, o2, e2, c2)
		} else {
			t.Logf("%v: %q %q exit %d", args, strings.TrimSpace(o1), strings.TrimSpace(e1), c1)
		}
	}
}

// lvSameRun reports whether two runs agree as lvCompareOn requires: the
// same stdout and exit code, and stderr the same up to a panic report's
// "backtrace:" line, as every reader that compares stderr reads it: a
// panic's message and site are compared, its frames are not.
// Their addresses move with ASLR and the image, and the two builds may
// inline differently, so a frame list that differs is no on/off
// difference.
func lvSameRun(o1, e1 string, c1 int, o2, e2 string, c2 int) bool {
	return o1 == o2 && c1 == c2 && dropBacktrace(e1) == dropBacktrace(e2)
}

// TestLVSameRunReadsToBacktrace pins lvSameRun: frames and addresses after
// "backtrace:" are not compared; stdout, the exit code, the panic's message
// and site, and stderr before the report are.
func TestLVSameRunReadsToBacktrace(t *testing.T) {
	const site = "panic: bounds at lv.mk:8:53\n"
	bt1 := "backtrace:\n  Mat (lv.mk)\n  Main (lv.mk)\n  0x7ff6641bda00\n"
	bt2 := "backtrace:\n  Main (lv.mk)\n  0x7ff671ded600\n"
	for _, c := range []struct {
		name           string
		o1, e1, o2, e2 string
		c1, c2         int
		same           bool
	}{
		{"addresses and frames", "", site + bt1, "", site + bt2, 101, 101, true},
		{"no backtrace on one side", "", site, "", site + bt1, 101, 101, true},
		{"clean runs", "7\n", "", "7\n", "", 0, 0, true},
		{"another site", "", site + bt1, "", "panic: bounds at lv.mk:8:54\n" + bt1, 101, 101, false},
		{"another message", "", site + bt1, "", "panic: overflow at lv.mk:8:53\n" + bt1, 101, 101, false},
		{"stderr before the report", "", "note\n" + site + bt1, "", site + bt1, 101, 101, false},
		{"stdout", "1\n", "", "2\n", "", 0, 0, false},
		{"exit code", "", site + bt1, "", site + bt1, 101, 1, false},
		{"a panic on one side only", "7\n", "", "7\n", site + bt1, 0, 0, false},
	} {
		if got := lvSameRun(c.o1, c.e1, c.c1, c.o2, c.e2, c.c2); got != c.same {
			t.Errorf("%s: lvSameRun = %v, want %v", c.name, got, c.same)
		}
	}
}

// TestLoopVersion_SameProgram compares loopVersionSrc's builds.
func TestLoopVersion_SameProgram(t *testing.T) {
	lvCompare(t, loopVersionSrc, [][]string{
		{"16", "0", "7", "0"},                   // passes
		{"5", "0", "7", "3"},                    // passes: pos ends at 40, the length
		{"16", "1", "7", "0"},                   // b is one short: Mat's b check traps
		{"4", "0", "7", "4"},                    // Copy's dst check traps at its last index
		{"4", "0", "7", "-1"},                   // Copy's first index is negative
		{"4", "0", "9223372036854775000", "0"},  // Sum overflows above
		{"3", "0", "-9223372036854775000", "0"}, // a large negative term: the fallback, no overflow
		{"1", "0", "4611686018427387904", "0"},  // Sum's fallback, no overflow
	})
}

// lvGenerated is a program of n generated functions G0..G(n-1), each one
// loop over an array whose values, slice length, bounds and bases come
// from the command line, so the range analysis proves nothing and the
// versions see every shape: the counter, the counter plus or minus a
// base, a product base, a stepped counter, and checked sums with + and -.
// Main runs the function its first argument names.
func lvGenerated(n int) string {
	r := rand.New(rand.NewSource(768))
	var sb strings.Builder
	sb.WriteString("A(sys: &System, i: int): int {\n    return Cast[int](sys.args[i].ParseInt() else { 0 })\n}\n\n")
	bodies := []string{
		"    for i in lo..hi {\n        r = r +% s[i]\n    }\n",
		"    for i in lo..hi {\n        r = r +% s[i + k]\n    }\n",
		"    for i in lo..hi {\n        r = r +% s[k * m + i] +% s[i - m]\n    }\n",
		"    var p := k\n    for _ in lo..hi {\n        r = r +% s[p]\n        p += 1\n    }\n",
		"    for i in lo..hi {\n        r += s[i]\n    }\n",
		"    for i in lo..hi {\n        r -= s[i]\n    }\n",
	}
	for g := 0; g < n; g++ {
		fmt.Fprintf(&sb, "G%d(s: []int, lo: int, hi: int, k: int, m: int, r0: int): int {\n    var r := r0\n%s    return r\n}\n\n", g, bodies[r.Intn(len(bodies))])
	}
	sb.WriteString("export Main(sys: &System): OutOfMemory|WriteError!void {\n    var out := sys.Stdout()\n")
	sb.WriteString("    g := A(sys, 1)\n    cut := A(sys, 2)\n    fill := A(sys, 7)\n    a := try sys.arena.Slice[int](16)\n")
	sb.WriteString("    for i in 0..16 {\n        a[i] = fill +% i\n    }\n    s := a[0..cut]\n    var v := 0\n")
	for g := 0; g < n; g++ {
		fmt.Fprintf(&sb, "    if g == %d {\n        v = G%d(s, A(sys, 3), A(sys, 4), A(sys, 5), A(sys, 6), A(sys, 8))\n    }\n", g, g)
	}
	sb.WriteString("    try Print(mut out, \"{v}\\n\", (v,))\n}\n")
	return sb.String()
}

// TestLoopVersion_Generated compares the builds of lvGenerated(24) on
// argument lists that pass, read out of bounds at either end, and
// overflow, for every function.
func TestLoopVersion_Generated(t *testing.T) {
	const n = 24
	big := []string{"1", "-3", "4611686018427387904", "-4611686018427387904", "9223372036854775000"}
	r := rand.New(rand.NewSource(767))
	var argsets [][]string
	for g := 0; g < n; g++ {
		for q := 0; q < 6; q++ {
			cut := 1 + r.Intn(16)
			lo := r.Intn(5) - 2
			hi := lo + r.Intn(cut+3)
			args := []string{fmt.Sprint(g), fmt.Sprint(cut), fmt.Sprint(lo), fmt.Sprint(hi),
				fmt.Sprint(r.Intn(5) - 1), fmt.Sprint(r.Intn(3)), big[r.Intn(len(big))], big[r.Intn(len(big))]}
			argsets = append(argsets, args)
		}
	}
	lvCompare(t, lvGenerated(n), argsets)
}
