// Why: become promises a tail call that never grows the stack, and only a
// native run of long chains proves the promise holds in dev and release.
// What: TestBecome_Chains and its neighbours: the musttail IR, mk query's
// callers of a tail call, the borrow refusals at a become, and loops,
// narrowing and range patterns around it.
// How: the package testdata/become runs chains of 10,000,000 tail calls
// (direct, through a table of fn values, of a generic function, of a method,
// and one passing on its 24-byte parameter and returning through the
// caller's hidden result pointer) and prints each chain's count only when
// the lowest stack pointer at its end is the one at its start.
// x86_64-windows runs here; the x86_64-linux leg needs MK_LINUX=1 under
// tools/wsl-lock.ps1. The driver's TermTailCall sites are tested too: the
// freestanding report names a function a tail call reaches, and mk query's
// call sites list a tail call.

package driver

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mk/internal/types"
)

const becomeChainOut = "direct 10000000\nfn table 10000000\ngeneric 10000000\nmethod 10000000\nmemory 6 10000000\nstack 10000028\n"

func TestBecome_Chains(t *testing.T) {
	tools := needTools(t)
	dir := filepath.Join("testdata", "become")
	targets := []types.Target{types.X86_64Windows}
	if HostTarget().OS != types.OSWindows {
		targets = []types.Target{HostTarget()}
	}
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
				r, paths, err := s.Build(filepath.Join(t.TempDir(), "become"+exeSuffix(target)))
				if err != nil {
					t.Fatal(err)
				}
				if len(r.Report.Diags) > 0 || len(paths) != 1 {
					t.Fatalf("build:\n%s", r.Report.Text(r.Sources))
				}
				out, errOut, code := runExe(t, target, paths[0], "")
				if code != 0 || out != becomeChainOut {
					t.Fatalf("exit %d, stdout %q, stderr %q; want %q", code, out, errOut, becomeChainOut)
				}
			})
		}
	}
	if !linuxRun() {
		t.Logf("x86_64-linux: not run (%s)", linuxOff)
	}
}

// TestBecome_EmitsMustTail: the release IR of the chains holds a musttail
// call, followed by ret, for each become: direct, generic and method calls
// name their callee, the fn table's is indirect.
func TestBecome_EmitsMustTail(t *testing.T) {
	var mu sync.Mutex
	var ll strings.Builder
	hooks := TestHooks{OnLL: func(name, text string) {
		mu.Lock()
		ll.WriteString(text)
		mu.Unlock()
	}}
	s, err := NewSession(Config{Root: filepath.Join("testdata", "become"), Target: types.X86_64Windows, Profile: types.Release,
		Tools: needTools(t), CacheDir: t.TempDir(), TestHooks: hooks})
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Build(filepath.Join(t.TempDir(), "become.exe"))
	if err != nil || len(r.Report.Diags) != 0 {
		t.Fatalf("build: %v\n%s", err, r.Report.Text(r.Sources))
	}
	lines := strings.Split(ll.String(), "\n")
	direct, indirect := 0, 0
	for i, line := range lines {
		if !strings.Contains(line, "musttail call") {
			continue
		}
		if i+1 >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[i+1]), "ret ") {
			t.Errorf("a musttail call not followed by ret:\n%s\n%s", line, lines[min(i+1, len(lines)-1)])
		}
		if strings.Contains(line, " @") {
			direct++
		} else {
			indirect++
		}
	}
	if direct < 5 || indirect < 2 {
		t.Errorf("%d direct and %d indirect musttail calls, want at least 5 (Direct, Gen, Run, Carry, Many) and 2 (Even, Odd)", direct, indirect)
	}
}

// TestBecome_QueryCallers: mk query callers lists a tail call's caller, a
// direct one and one through a generic callee's instance (query_body.go's
// call sites read a TermTailCall); the freestanding report's site
// (free.go) reads it the same way, but its callees are intrinsics, which
// become cannot reach (E0314).
func TestBecome_QueryCallers(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"pkg.mk": fixBecomeManifest,
		"src/main.mk": `Count(n: int, acc: int): int {
    if n == 0 {
        return acc
    }
    become Count(n - 1, acc + 1)
}

Gen[T](x: T, n: int): T {
    if n == 0 {
        return x
    }
    become Gen[T](x, n - 1)
}

Hop(n: int, acc: int): int {
    become Count(n, acc)
}

export Main(sys: &System): WriteError!void {
    n := Hop(3, 0) + Gen(4, 2)
    try sys.Print("{n}\n", (n: n))
}
`,
	})
	s, err := NewSession(Config{Root: dir, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		fn   string
		want []string
	}{{"Count", []string{"Count", "Hop"}}, {"Gen", []string{"Gen", "Main"}}} {
		a, res, err := s.Query("callers", []string{c.fn})
		if err != nil || !a.Found {
			t.Fatalf("callers %s: %v %+v\n%s", c.fn, err, a, diagLines(res))
		}
		text := strings.Join(a.Answer, "\n")
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("callers %s lacks %s:\n%s", c.fn, w, text)
			}
		}
	}
}

// TestBecome_BorrowRefusals: the borrow checker's rules at the
// TermTailCall: an argument borrowing the frame is E0603, a linear
// local not consumed is E0402, and the result's loans meet the
// signature's sources as a return's do (E0602).
func TestBecome_BorrowRefusals(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"argument borrows a local", "Sum(x: &int, n: int): int {\n    if n == 0 {\n        return *x\n    }\n    v := *x + n\n    become Sum(&v, n - 1)\n}\n", "E0603"},
		{"linear local", "linear struct Tok {\n    id: int\n}\n\nSpend(t: Tok): int {\n    Tok(id) := t\n    return id\n}\n\nHold(n: int): int {\n    t := Tok(id: n)\n    if n == 0 {\n        return Spend(t)\n    }\n    become Hold(n - 1)\n}\n", "E0402"},
		{"result from the other source", "Swap(a: &int, b: &int): &int from a {\n    become Pick(b, a)\n}\n\nPick(a: &int, b: &int): &int from a {\n    return a\n}\n", "E0602"},
		{"ends p through a tail call that ends p", "Wipe(a: mut Arena, n: int) ends a {\n    if n == 0 {\n        a.Reset()\n        return\n    }\n    become Wipe(a, n - 1)\n}\n", ""},
		{"ends p through a tail call that ends another", "Wipe(a: mut Arena, b: mut Arena, n: int) ends a {\n    if n == 0 {\n        a.Reset()\n        return\n    }\n    become Wipe(b, a, n - 1)\n}\n", "E1102"},
		{"parameter's own reference is fine", "Walk(x: &int, n: int): int {\n    if n == 0 {\n        return *x\n    }\n    become Walk(x, n - 1)\n}\n", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := writeTree(t, map[string]string{"pkg.mk": fixBecomeManifest,
				"src/main.mk": c.src + "\nexport Main(sys: &System): WriteError!void {\n    try sys.Print(\"x\\n\")\n}\n"})
			s, err := NewSession(Config{Root: dir, CacheDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			r := s.Check()
			got := strings.TrimSpace(diagLines(r))
			if c.want == "" && got != "" || c.want != "" && (len(r.Report.Diags) != 1 || !strings.HasPrefix(got, c.want+" ")) {
				t.Errorf("diagnostics:\n%s\nwant one %s", got, c.want)
			}
		})
	}
}

const becomeMiscSrc = `F(n: int): int {
    for i in 0..n {
        if i == 3 {
            become F(n - 1)
        }
    }
    return n
}

H[T](n: int): int {
    if T is int {
        if n > 0 {
            become H[T](n - 1)
        }
    }
    return n + 100
}

S8(x: i8): int {
    return match x {
        -128..-100 { 1 }
        -100..0 { 2 }
        0..100 { 3 }
        100.. { 4 }
    }
}

S32(x: i32): int {
    return match x {
        ..-70000 { 1 }
        -70000..-1 { 2 }
        -1 { 3 }
        0.. { 4 }
    }
}

S64(x: i64): int {
    return match x {
        ..-5000000000 { 1 }
        -5000000000..5000000000 { 2 }
        5000000000.. { 3 }
    }
}

C(c: char): int {
    return match c {
        'a'..'{' { 1 }
        '\u{E000}'.. { 2 }
        _ { 0 }
    }
}

export Main(sys: &System): WriteError!void {
    a := F(10)
    b := H[int](1000) + H[bool](5)
    x := S8(-128) * 1000 + S8(-101) * 100 + S8(-100) * 10 + S8(127)
    y := S32(-70001) * 1000 + S32(-70000) * 100 + S32(-1) * 10 + S32(2147483647)
    z := S64(-9223372036854775808) * 100 + S64(4999999999) * 10 + S64(5000000000)
    w := C('a') * 100 + C('\u{10FFFF}') * 10 + C('{')
    try sys.Print("{a} {b} {x} {y} {z} {w}\n", (a: a, b: b, x: x, y: y, z: z, w: w))
}
`

// TestBecome_LoopsNarrowingAndRanges: a become inside a for loop and inside
// an `if T is X` branch, and range patterns with negative ends on i8, i32
// and i64 and char ends, natively in dev and release; each profile builds
// twice with one cache, so the second build decodes the cached bodies that
// hold TermTailCall and the range tests.
func TestBecome_LoopsNarrowingAndRanges(t *testing.T) {
	tools := needTools(t)
	const want = "3 205 1124 1234 123 120\n"
	dir := writeTree(t, map[string]string{"pkg.mk": fixBecomeManifest, "src/main.mk": becomeMiscSrc})
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		cache := t.TempDir()
		for round := 0; round < 2; round++ {
			s, err := NewSession(Config{Root: dir, Profile: prof, Tools: tools, CacheDir: cache})
			if err != nil {
				t.Fatal(err)
			}
			r, paths, err := s.Build(filepath.Join(t.TempDir(), "app"+exeSuffix(HostTarget())))
			if err != nil || len(r.Report.Diags) > 0 || len(paths) != 1 {
				t.Fatalf("%v round %d: %v\n%s", prof, round, err, r.Report.Text(r.Sources))
			}
			out, errOut, code := runExe(t, HostTarget(), paths[0], "")
			if code != 0 || out != want {
				t.Errorf("%v round %d: exit %d, %q %q; want %q", prof, round, code, out, errOut, want)
			}
		}
	}
}
