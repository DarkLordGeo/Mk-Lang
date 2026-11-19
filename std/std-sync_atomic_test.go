// Why: atomic.Atomic[T] must keep each memory order it promises, and refuse at
// compile time an order or operation that does not apply.
// What: TestSTDSYNC_AtomicBlocks, its Linux twin, and the E0706/E0701 tests.
// How: mk test --std=std.atomic (std/atomic/tests_atomic_t.mk), dev and release,
// 1 and 8 carriers, jitter seeds; Linux with MK_LINUX=1 under tools/wsl-lock.ps1.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sync2AtomicCount is the number of AtomicT blocks in tests_atomic_t.mk.
func sync2AtomicCount(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash("atomic/tests_atomic_t.mk"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\ntest \"AtomicT ")
}

func sync2AtomicTest(t *testing.T, root, stdDir string, extra ...string) {
	t.Helper()
	args := append([]string{"test", "--std=std.atomic", "-run", "AtomicT "}, extra...)
	out, status := mkCmd(t, root, stdDir, args...)
	if strings.Contains(out, "no usable LLVM") {
		t.Skipf("needs the LLVM toolchain: %s", out)
	}
	if status != 0 || !strings.Contains(out, " 0 failed") || !strings.Contains(out, fmt.Sprintf("%d passed", sync2AtomicCount(t))) {
		t.Errorf("mk %s, MK_CARRIERS=%s, MK_TEST_SEED=%s: status %d\n%s", strings.Join(args, " "), os.Getenv("MK_CARRIERS"), os.Getenv("MK_TEST_SEED"), status, out)
	}
}

// TestSTDSYNC_AtomicBlocks: every Atomic[T] block, dev and release, 1 and
// 8 carriers, jitter seeds 1 to sync2Seeds(). The store-buffering block
// fails at 8 carriers when a SeqCst store or load is lowered as a release
// or acquire one (checked by mutating the lowering).
func TestSTDSYNC_AtomicBlocks(t *testing.T) {
	root, stdDir := missStd(t)
	for _, profile := range []string{"dev", "release"} {
		for _, n := range syncCarriers {
			t.Run(fmt.Sprintf("%s/carriers=%d", profile, n), func(t *testing.T) {
				t.Setenv("MK_CARRIERS", fmt.Sprint(n))
				for seed := 1; seed <= sync2Seeds(); seed++ {
					t.Setenv("MK_TEST_SEED", fmt.Sprint(seed))
					if profile == "release" {
						sync2AtomicTest(t, root, stdDir, "-release")
					} else {
						sync2AtomicTest(t, root, stdDir)
					}
				}
			})
		}
	}
}

// TestSTDSYNC_LinuxAtomicBlocks: the blocks on x86_64-linux, dev at 8
// carriers and release at 1, 8 and 16. Only with MK_LINUX=1, under
// tools/wsl-lock.ps1, go test -p 1.
func TestSTDSYNC_LinuxAtomicBlocks(t *testing.T) {
	stdioNeedWSL(t)
	root, stdDir := missStd(t)
	t.Setenv("WSLENV", "MK_CARRIERS/u:MK_TEST_SEED/u")
	for _, c := range []struct {
		release  bool
		carriers int
	}{{false, 8}, {true, 1}, {true, 8}, {true, 16}} {
		t.Setenv("MK_CARRIERS", fmt.Sprint(c.carriers))
		for seed := 1; seed <= 2; seed++ {
			t.Setenv("MK_TEST_SEED", fmt.Sprint(seed))
			extra := []string{"--target", "x86_64-linux"}
			if c.release {
				extra = append(extra, "-release")
			}
			sync2AtomicTest(t, root, stdDir, extra...)
		}
	}
}

// TestSTDSYNC_AtomicOrderRejected: every order an operation can
// have compiles; one it cannot have is E0706 at the call, naming the
// clause; a computing read-modify-write on a pointer and an Atomic of a
// float are E0701.
func TestSTDSYNC_AtomicOrderRejected(t *testing.T) {
	root, stdDir := missStd(t)
	write := func(name, body string) {
		// A raw pointer comes in as a parameter: std.raw is E0202 here.
		src := "import std.atomic\n\nexport Probe(q: *u8) {\n    a := atomic.Atomic[u32].New(1)\n    p := atomic.Atomic[*u8].New(q)\n" + body + "    _ = p.Load(atomic.Relaxed)\n}\n\nexport Main(sys: &System) {\n}\n"
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var good strings.Builder
	for _, o := range []string{"Relaxed", "Acquire", "SeqCst"} {
		fmt.Fprintf(&good, "    _ = a.Load(atomic.%s)\n", o)
	}
	for _, o := range []string{"Relaxed", "Release", "SeqCst"} {
		fmt.Fprintf(&good, "    a.Store(2, atomic.%s)\n", o)
	}
	all := []string{"Relaxed", "Acquire", "Release", "AcqRel", "SeqCst"}
	for _, o := range all {
		fmt.Fprintf(&good, "    _ = a.Swap(3, atomic.%s)\n    _ = a.FetchAdd(1, atomic.%s)\n    _ = p.Swap(p.Load(atomic.Relaxed), atomic.%s)\n", o, o, o)
		for _, f := range []string{"Relaxed", "Acquire", "SeqCst"} {
			fmt.Fprintf(&good, "    _ = a.CompareExchange(1, 2, atomic.%s, atomic.%s)\n    _ = a.CompareExchangeWeak(1, 2, atomic.%s, atomic.%s)\n", o, f, o, f)
		}
	}
	write("good.mk", good.String())
	if out, status := mkCmd(t, root, stdDir, "check", "good.mk"); status != 0 {
		t.Fatalf("mk check good.mk: status %d\n%s", status, out)
	}
	for _, c := range []struct{ name, line, code, clause string }{
		{"load release", "    _ = a.Load(atomic.Release)\n", "E0706", "o.CanLoad()"},
		{"load acqrel", "    _ = a.Load(atomic.AcqRel)\n", "E0706", "o.CanLoad()"},
		{"store acquire", "    a.Store(1, atomic.Acquire)\n", "E0706", "o.CanStore()"},
		{"store acqrel", "    a.Store(1, atomic.AcqRel)\n", "E0706", "o.CanStore()"},
		{"cas failure release", "    _ = a.CompareExchange(1, 2, atomic.SeqCst, atomic.Release)\n", "E0706", "failure.CanLoad()"},
		{"weak cas failure acqrel", "    _ = a.CompareExchangeWeak(1, 2, atomic.AcqRel, atomic.AcqRel)\n", "E0706", "failure.CanLoad()"},
		{"fetch add on a pointer", "    _ = p.FetchAdd(q, atomic.Relaxed)\n", "E0701", "integer"},
		{"fetch xor on a pointer", "    _ = p.FetchXor(q, atomic.Relaxed)\n", "E0701", "integer"},
		{"atomic of a float", "    f := atomic.Atomic[f64].New(1.0)\n    _ = f.Load(atomic.Relaxed)\n", "E0701", "raw pointer"},
	} {
		t.Run(c.name, func(t *testing.T) {
			name := strings.ReplaceAll(c.name, " ", "_") + ".mk"
			write(name, c.line)
			out, status := mkCmd(t, root, stdDir, "check", name)
			if status == 0 || !strings.Contains(out, name+":6:") || !strings.Contains(out, c.code) || !strings.Contains(out, c.clause) {
				t.Errorf("mk check %s: status %d, want %s at line 6 naming %q\n%s", name, status, c.code, c.clause, out)
			}
		})
	}
}

// TestSTDSYNC_ForwardedOrderRejected: a function
// that takes an order as a `const` parameter and passes it on to Load,
// Store, a compare-exchange's failure order or Fence must state the
// operation's clause (E0706 at the inner call, with the clause), and with
// the clause an order that does not apply is E0706 at the wrapper's call.
// Each of these once reached codegen and was an internal compiler
// error (E0000 "intrinsic ... with order N"), and `mk check` accepted it.
// With the clause and orders that apply, the wrappers run, dev and release.
func TestSTDSYNC_ForwardedOrderRejected(t *testing.T) {
	root, stdDir := missStd(t)
	type wrapper struct{ name, decl, clause, call string }
	ws := []wrapper{
		{"load", "Ld(a: &atomic.Atomic[i64], const o: atomic.Order): i64%s {\n    return a.Load(o)\n}\n", "o.CanLoad()", "_ = Ld(&a, atomic.%s)"},
		{"store", "St(a: &atomic.Atomic[i64], const o: atomic.Order)%s {\n    a.Store(3, o)\n}\n", "o.CanStore()", "St(&a, atomic.%s)"},
		{"cas failure", "Cx(a: &atomic.Atomic[i64], const f: atomic.Order): bool%s {\n    r := a.CompareExchange(5, 6, atomic.SeqCst, f)\n    return r.ok\n}\n", "f.CanLoad() && f.CanModify()", "_ = Cx(&a, atomic.%s)"},
		{"fence", "Fn(const o: atomic.Order)%s {\n    atomic.Fence(o)\n}\n", "o.CanFence()", "Fn(atomic.%s)"},
	}
	bad := map[string][]string{"load": {"Release", "AcqRel"}, "store": {"Acquire", "AcqRel"}, "cas failure": {"Release", "AcqRel"}, "fence": {"Relaxed"}}
	good := map[string][]string{"load": {"Relaxed", "Acquire", "SeqCst"}, "store": {"Relaxed", "Release", "SeqCst"}, "cas failure": {"Relaxed", "Acquire", "SeqCst"}, "fence": {"Acquire", "Release", "AcqRel", "SeqCst"}}
	prog := func(w wrapper, clause string, calls []string) string {
		var b strings.Builder
		b.WriteString("import std.atomic\n\n")
		b.WriteString(fmt.Sprintf(w.decl, clause))
		b.WriteString("\nexport Main(sys: &System): WriteError!void {\n    a := atomic.Atomic[i64].New(5)\n")
		for _, c := range calls {
			b.WriteString("    " + c + "\n")
		}
		b.WriteString("    x := a.Load(atomic.SeqCst)\n    var out := sys.Stdout()\n    try Print(mut out, \"{x}\\n\", (x: x))\n}\n")
		return b.String()
	}
	check := func(t *testing.T, name, src string) (string, int) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		out, status := mkCmd(t, root, stdDir, "check", name)
		if strings.Contains(out, "E0000") || strings.Contains(out, "internal compiler error") {
			t.Fatalf("mk check %s: an internal compiler error\n%s\n%s", name, src, out)
		}
		return out, status
	}
	for _, w := range ws {
		tag := strings.ReplaceAll(w.name, " ", "_")
		t.Run(w.name+"/not restated", func(t *testing.T) {
			name := "fwd_" + tag + ".mk"
			out, status := check(t, name, prog(w, "", []string{fmt.Sprintf(w.call, good[w.name][0])}))
			if status == 0 || !strings.Contains(out, name+":4:") || !strings.Contains(out, "E0706") ||
				!strings.Contains(out, "clause: "+w.clause) || !strings.Contains(out, "state it on this function") {
				t.Errorf("mk check %s: status %d, want E0706 at the inner call naming %q\n%s", name, status, w.clause, out)
			}
		})
		for _, o := range bad[w.name] {
			t.Run(w.name+"/restated/"+o, func(t *testing.T) {
				name := "bad_" + tag + "_" + o + ".mk"
				out, status := check(t, name, prog(w, "\n    requires "+w.clause, []string{fmt.Sprintf(w.call, o)}))
				line := strings.Count(fmt.Sprintf(w.decl, "\n    requires "+w.clause), "\n") + 6
				if status == 0 || !strings.Contains(out, fmt.Sprintf("%s:%d:", name, line)) || !strings.Contains(out, "E0706") || !strings.Contains(out, "false for these arguments") {
					t.Errorf("mk check %s: status %d, want E0706 at line %d (the wrapper's call)\n%s", name, status, line, out)
				}
			})
		}
		t.Run(w.name+"/restated/good", func(t *testing.T) {
			var calls []string
			for _, o := range good[w.name] {
				calls = append(calls, fmt.Sprintf(w.call, o))
			}
			name := "good_" + tag + ".mk"
			src := prog(w, "\n    requires "+w.clause, calls)
			if out, status := check(t, name, src); status != 0 {
				t.Fatalf("mk check %s: status %d\n%s", name, status, out)
			}
			want := map[string]string{"load": "5\n", "store": "3\n", "cas failure": "6\n", "fence": "5\n"}[w.name]
			for _, extra := range [][]string{nil, {"--release"}} {
				out, status := mkCmd(t, root, stdDir, append([]string{"run", name}, extra...)...)
				if strings.Contains(out, "no usable LLVM") {
					t.Skipf("needs the LLVM toolchain: %s", out)
				}
				if status != 0 || !strings.HasSuffix(out, want) {
					t.Errorf("mk run %s %v: status %d, want output %q\n%s", name, extra, status, want, out)
				}
			}
		})
	}
}
