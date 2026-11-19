// Why: no input may crash the checker or make it report an internal error.
// What: FuzzCheck, its hand seeds, the known and fixed crasher tables, and
// TestFuzzCheckSeedsCover.
// How: every input runs through the corpus adapter on a shared session; the
// property is no E0000, no panic, and a return within 5 s.
//
// FuzzCheck feeds the input, as a block source with the test prelude, to
// CorpusSession.CheckUnit. Seeds: every doc block, every file of every
// conformance unit, and adversarial inputs (lone CR, BOM, unterminated
// strings and comments, deep nesting, habit tokens). Inputs over 64 KiB are
// skipped. A crasher Go saves under testdata/fuzz/FuzzCheck/
// is kept as a regression seed, with its fix.

package corpus

import (
	"fmt"
	"os"

	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/source"
)

// fuzzCheckLimit is the per-input time bound, fuzzMaxInput its size cut.
const (
	fuzzCheckLimit = 5 * time.Second
	fuzzMaxInput   = 64 << 10
)

// fuzzAdversarial are the hand seeds: inputs that reach the lexer's and
// parser's error paths and the habits of other languages, each then checked.
var fuzzAdversarial = []string{
	"", "\n", "\r", "x\ry\r", "F() {\r    return\r}\r", "\xef\xbb\xbfF() {\n}\n", "\xef\xbb\xbf",
	"F() {\n    s := \"unterminated\n}\n", "F() {\n    s := \"\"\"\n", "/* never closed\nF() {\n}\n",
	"F() {\n    c := 'a\n}\n", "F() {\n    x := 1 /* a /* nested */ comment\n}\n",
	"F(): int {\n    return " + strings.Repeat("(", 400) + "1" + strings.Repeat(")", 400) + "\n}\n",
	"F() {\n" + strings.Repeat("    if true {\n", 200) + strings.Repeat("    }\n", 200) + "}\n",
	"F(): int {\n    return 1" + strings.Repeat(" + 1", 2000) + "\n}\n",
	"fn add(a: int, b: int) -> int {\n    return a + b;\n}\n", "F() {\n    let mut x = 1;\n}\n",
	"impl Point {\n    fn len(&self) -> f64 { }\n}\n", "F() {\n    for i := 0; i < 3; i++ {\n    }\n}\n",
	"F() {\n    x := a?.b ?? c\n}\n", "F() {\n    v := |x| x + 1\n}\n", "F() {\n    y := n as f64\n}\n",
	"use std::io;\n", "#include <stdio.h>\n", "F() {\n    x := null\n    y := nil\n}\n",
	"class A {\n    public int x;\n}\n", "F() {\n    switch x {\n    case 1:\n    }\n}\n",
	"F() {\n    while true {\n    }\n}\n", "F() {\n    x := new Point()\n}\n", "@inline\nF() {\n}\n",
	"#[derive(Eq)]\nstruct P {\n}\n", "F() {\n    try {\n    } catch (e) {\n    }\n}\n",
	"struct S {\n    s: S\n}\n", "Loop(n: int): int {\n    return Loop(n)\n}\n",
	"gen derive.Eq(Point)\n", "import std.derive\ngen derive.Eq(Nope)\n", "const X = X\n",
	"const T = Spin() fuel 1\n\nSpin(): int {\n    var n := 0\n    for {\n        n += 1\n    }\n}\n",
	"F() {\n    x := quote { $y }\n}\n", "F[T](x: T): T {\n    return F[[]T](x)\n}\n",
	// An untyped const whose initializer failed to type,
	// read by a const, a function, a generic, a test and a fn literal held
	// in a const, gave lower's E0000 ("a signature whose result is Poison
	// with no diagnostic", "*ast.Ident is Poison with no diagnostic") and the
	// walk's ("iface: no body for F#fn0, which a hash requires").
	"const A = 1 + true\nconst B = A\n\nexport Main(sys: &System): int {\n\treturn B\n}\n",
	"const A = 1 + true\n\nG[T](x: T): int {\n\treturn A\n}\n\ntest \"t\" {\n\tAssert(A == 2)\n}\n\nexport Main(sys: &System): int {\n\treturn G(1)\n}\n",
	"const A = 1 + true\nconst F = fn(x: int): int { return x + A }\nconst G = F\n\nexport Main(sys: &System): int {\n\treturn G(1)\n}\n",
	"const F = fn(x: int): int { return x + true }\n\nexport Main(sys: &System): int {\n\treturn F(1)\n}\n",
	// A repeat count or a const argument naming an early const was
	// Poison with no diagnostic while typeConsts ran, and its readers reached
	// lower; a read of such a const in a branch lower folds; a cycle through
	// such a count; and N reading J, declared after it.
	"import std.math\n\nconst N = math.OnesCount64(7)\nconst B = [7; N]\n\nexport Main(sys: &System): int {\n\treturn B[2] + N\n}\n",
	"import std.math\n\nconst Debug = false\nconst B = [7; N]\nconst N = 1 + math.OnesCount64(7)\nconst K: int = [if Debug { B[0] } else { 3 }][0]\n\nexport Main(sys: &System): int {\n\tvar a: [K]int = [2; K]\n\treturn a[0] + K + 10\n}\n",
	"const C = [1; N][0]\nconst N = C\n\nexport Main(sys: &System): int {\n\treturn C\n}\n",
	"import std.math\n\nconst B = [7; N]\nconst N = J + math.OnesCount64(7)\nconst J = 1\n\nexport Main(sys: &System): int {\n\treturn B[2] + N\n}\n",
	// `mut *xs` on a mut []T, which check types as the reborrow of the
	// slice, reached lower's deref and gave E0000 "lower: deref of mut
	// []i32": returned, through a field, passed as an argument and bound.
	"F(xs: mut []i32): mut []i32 {\n    return mut *xs\n}\n\nexport Main(sys: &System): int {\n    var a: [3]i32 = [1, 2, 3]\n    r := F(mut a[..])\n    r[0] = 7\n    return int(a[0])\n}\n",
	"struct Box2 {\n    xs: mut []str\n}\n\nField(b: Box2): mut []str from b {\n    return mut *b.xs\n}\n",
	"G(ys: mut []i32) {\n    ys[0] = 1\n}\n\nF(xs: mut []i32) {\n    G(mut *xs)\n    r := mut *xs\n    r[1] = 2\n}\n",
}

// fuzzSession is one corpus session per test process: std is checked once,
// and every input gets a fresh unit name (a session refuses one twice).
// A session keeps every unit's files and items for its lifetime, so a long
// fuzz run (an hour, tens of millions of inputs) would grow without
// bound; the session is replaced after fuzzSessionInputs inputs.
type fuzzSession struct {
	mu   sync.Mutex
	comp Compiler
	used int // inputs checked on comp
	n    atomic.Int64
}

const fuzzSessionInputs = 10_000

func (s *fuzzSession) start(t testing.TB) Compiler {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.comp == nil || s.used >= fuzzSessionInputs {
		comp := DriverCompiler(repoRoot(t), driver.Config{})
		if _, err := comp.Start(); err != nil {
			t.Fatal(err)
		}
		s.comp, s.used = comp, 0
	}
	s.used++
	return s.comp
}

// checkInput is FuzzCheck's property for one input.
func (s *fuzzSession) checkInput(t *testing.T, data []byte) {
	if len(data) > fuzzMaxInput {
		t.Skip("input over 64 KiB")
	}
	comp := s.start(t)
	u := &Unit{
		Name:    fmt.Sprintf("fuzz/check#%d", s.n.Add(1)),
		Verdict: VerdictOK,
		Files:   []UnitFile{{Path: BlockFile, Text: source.Normalize(data)}},
		Meta:    Meta{Prelude: true},
	}
	u.Key = u.Name
	done := make(chan driver.BlockResult, 1)
	panicked := make(chan any, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				panicked <- p
			}
		}()
		done <- comp.Check(u, driver.UnitCheck)
	}()
	select {
	case p := <-panicked:
		t.Fatalf("panic escaped CheckUnit: %v", p)
	case res := <-done:
		kc, isKnown := knownFuzzCrashers[string(data)]
		known := kc.seed + ", " + kc.reason
		for _, e := range res.Report.Diags {
			if e.Diag.Code != diag.E0000 {
				continue
			}
			if isKnown {
				t.Logf("known crasher (%s): %s", known, e.Diag.Message)
				return
			}
			t.Fatalf("internal error: %s\n%s", e.Diag.Message, res.Report.Text(res.Sources))
		}
		if isKnown {
			t.Errorf("known crasher (%s) has no E0000 now: remove it from knownFuzzCrashers", known)
		}
	case <-time.After(fuzzCheckLimit):
		t.Fatalf("CheckUnit did not return within %v", fuzzCheckLimit)
	}
}

// knownFuzzCrashers are the seeds under testdata/fuzz/FuzzCheck/ whose fix
// lies in another package, keyed by the input, with the seed file, the
// package that owns the fix and what it shows. Such an input's E0000 is
// logged, not failed, so the seed stays a regression test (every crasher is
// kept) while this package stays green and -fuzz can run past it; once the
// fix lands, the entry fails and must be removed. The key is the input, not
// the subtest name, because -fuzz replays seeds under other names.
//
// The table is empty. lower fixed the `ensures` crasher (an `ensures` on a
// non-bool, after its E0301, lowered to MIR that failed mir.Validate's
// invariant M5). Its two seeds, e463cef808bbda91 and lower_ensures_int,
// stay under testdata/fuzz/FuzzCheck as regression tests that must draw no
// E0000. lower_struct_pattern was fixed in lower too, so its seed moved to
// fixedFuzzCrashers.
var knownFuzzCrashers = map[string]knownCrasher{}

type knownCrasher struct{ seed, reason string }

// fixedFuzzCrashers are the seeds of crashers fixed in other packages, by seed
// file and input. TestFuzzCheckSeedsCover keeps each one present with its
// input, so it is replayed (and must draw no E0000) on every go test. A
// seed whose crash was past check (codegen_simd_reinterpret_generic) never
// crashed FuzzCheck, which stops at check: it keeps check clean, and the
// program test its comment names judges the fix.
var fixedFuzzCrashers = map[string]string{
	"e463cef808bbda91":  "A()requires 0 ensures 0{}\ntest\"\"{}",
	"lower_ensures_int": "A() ensures 0 {\n}\n",
	// A PathPattern naming no variant, error or struct was a silent Poison
	// in check, then lower's E0000 "types.Items.Get: unknown item 0"
	// (check's TestFuzzPathPatternNotAPattern).
	"54056f522b943918": "import std.net\nA(e:net.Error){match 0{net.Refused.A{}}}",
	// `.go` on a Scope value that is not the binding was
	// accepted, and lower's spawn failed mir.Validate's invariant M9 (check's
	// TestFuzzSpawnOnScopeValue).
	"c8cfb8114d6ab0ba": "Bump(a00:mut i32){}\nA(s:Scope,x:mut i32){s.go Bump(x)}",
	// Found by go test -fuzz FuzzCheck: the instance-limit walk hashed a fn
	// literal whose check failed ("iface: no body for A#fn0, which a hash
	// requires"); fixed in the driver's walk.go (lostLiterals,
	// TestWalkLeavesLostLiteralOwners).
	"a0330440db9679ea": "A(xs: mut []i32) {xs.Sort(fn(a: &i32,a0: &i32): bool {})} ",
	// A struct pattern in a match arm. check accepts
	// it and set no Use; lower's input_check.go read every
	// non-error PathPattern as a variant ("types.Items.Get: unknown item 0").
	// Fixed in lower.
	"lower_struct_pattern": "struct P {\n\tx: int\n}\n\nA(p: P) {\n\tmatch p {\n\t\tP(x) {\n\t\t\t_ = x\n\t\t}\n\t}\n}\n",
	// An interface method with its own const generic, called through a
	// bound as x.M[0](), failed in lower ("mir.Validate: M2: bb0
	// terminator: type 0 out of range"): lower read Self only from an x.M
	// callee, not x.M[A], and left the call's SelfType NoType. Fixed in
	// lower's input_check.go.
	"lower_iface_const_generic": "interface I {\n    M[const K: int](&self): int\n}\n\nstruct S {\n    a: int\n}\n\nimpl I for S {\n    M[const K: int](&self): int {\n        return K\n    }\n}\n\nH[T: I](x: &T): int {\n    return x.M[0]()\n}\n",
	// Reinterpret in a generic over the lane types, whose size clause check
	// did not restate, was an LLVM bitcast ICE at build ("bitcast <4 x i8>
	// to <4 x i64> is not a valid cast"). Check never drew an E0000 on it.
	// The size clause, which reads R's own bare lane type parameters, is
	// restated, so check gives E0706 at the call and the program is no
	// longer built: this seed keeps that E0706 free of an E0000, and the
	// verdict is pinned by conformance
	// tests/conformance/codes/E0706_simd_reinterpret_restated.mk.
	"codegen_simd_reinterpret_generic": "import std.simd\n\nR[T: simd.Lane, U: simd.Lane](v: simd.Vec[T, 4]): simd.Vec[U, 4] {\n    return v.Reinterpret[U, 4]()\n}\n\nexport Main(sys: &System): int {\n    w := R[u8, u64](simd.Vec[u8, 4].Splat(7))\n    return Cast[int](w.Lane(0))\n}\n",
}

// FuzzCheck is the checker fuzzer.
func FuzzCheck(f *testing.F) {
	root := repoRoot(f)
	blocks, err := DesignBlocks(root)
	if err != nil {
		f.Fatal(err)
	}
	for _, b := range blocks {
		f.Add([]byte(b.Source))
	}
	units, err := Units(root)
	if err != nil {
		f.Fatal(err)
	}
	for _, u := range units {
		for _, file := range u.Files {
			f.Add([]byte(file.Text))
		}
	}
	for _, s := range fuzzAdversarial {
		f.Add([]byte(s))
	}
	var s fuzzSession
	f.Fuzz(s.checkInput)
}

// TestFuzzCheckSeedsCover: FuzzCheck's seeds hold every doc block and
// every conformance unit (more than 100 blocks, more than 300 units), so the
// fuzzer's replay under go test is the full seed set, not a sample.
func TestFuzzCheckSeedsCover(t *testing.T) {
	root := repoRoot(t)
	blocks, err := DesignBlocks(root)
	if err != nil {
		t.Fatal(err)
	}
	units, err := Units(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) <= 100 || len(units) <= 300 {
		t.Errorf("FuzzCheck seeds from %d design blocks and %d conformance units; want more than 100 and 300", len(blocks), len(units))
	}
	t.Logf("FuzzCheck seeds: %d design blocks, %d conformance units, %d adversarial inputs", len(blocks), len(units), len(fuzzAdversarial))
	// A known crasher with no seed file would never run, so never go stale.
	for input, kc := range knownFuzzCrashers {
		b, err := os.ReadFile(filepath.Join("testdata", "fuzz", "FuzzCheck", kc.seed))
		if err != nil {
			t.Errorf("knownFuzzCrashers names %s, which is not a seed under testdata/fuzz/FuzzCheck: %v", kc.seed, err)
			continue
		}
		if want := fmt.Sprintf("go test fuzz v1\n[]byte(%q)\n", input); string(b) != want {
			t.Errorf("seed %s does not hold its knownFuzzCrashers input %q", kc.seed, input)
		}
	}
	for seed, input := range fixedFuzzCrashers {
		if _, held := knownFuzzCrashers[input]; held {
			t.Errorf("seed %s is both fixed and a known crasher", seed)
		}
		b, err := os.ReadFile(filepath.Join("testdata", "fuzz", "FuzzCheck", seed))
		if err != nil {
			t.Errorf("fixed crasher seed %s is gone from testdata/fuzz/FuzzCheck: %v", seed, err)
			continue
		}
		if want := fmt.Sprintf("go test fuzz v1\n[]byte(%q)\n", input); string(b) != want {
			t.Errorf("seed %s does not hold its fixed crasher input %q", seed, input)
		}
	}
}
