// Why: a trapped call must report exactly what the call it replaces reports,
// and change nothing in builds that have none.
// What: TestTrapForm_Decision, _SameReport, _PairsIntact, _NoTrapNoCost and
// _PackKeys over the probe programs trapProbe, trapDyn, trapHook and trapAsm.
// How: builds each program in the trapped form and the call form and compares
// stderr, stdout, exit status, backtrace frames, executable bytes and pack keys.

package driver

import (
	"bytes"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"mk/internal/types"
)

func TestTrapForm_Decision(t *testing.T) {
	rel := Config{Target: types.X86_64Windows, Profile: types.Release}
	for _, c := range []struct {
		name     string
		cfg      Config
		lib, gro bool
		want     bool
	}{
		{"release", rel, false, false, true},
		{"checked", Config{Target: types.X86_64Windows, Profile: types.Checked}, false, false, true},
		{"size", Config{Target: types.X86_64Windows, Profile: types.Release, Size: true}, false, false, true},
		{"dev", Config{Target: types.X86_64Windows, Profile: types.Dev}, false, false, false},
		{"test", Config{Target: types.X86_64Windows, Profile: types.TestProfile}, false, false, false},
		{"debug", Config{Target: types.X86_64Windows, Profile: types.Release, Debug: true}, false, false, false},
		{"library", rel, true, false, false},
		{"gro", rel, false, true, false},
		{"linux", Config{Target: types.X86_64Linux, Profile: types.Release}, false, false, false},
		{"freestanding", Config{Target: types.X86_64Freestanding, Profile: types.Release}, false, false, false},
		{"nutshell", Config{Target: types.X86_64Nutshell, Profile: types.Release}, false, false, false},
		{"hook", Config{Target: types.X86_64Windows, Profile: types.Release, TestHooks: TestHooks{NoTrapForm: true}}, false, false, false},
	} {
		if got := trapForm(c.cfg, c.lib, c.gro); got != c.want {
			t.Errorf("%s: trapForm %v, want %v", c.name, got, c.want)
		}
	}
}

// trapProbe is a program with zero-parameter never functions called from
// small (alwaysinline) functions, which Main and Framed inline: the cold
// calls are trapped. Framed has a frame of its own; Done exits 0.
const trapProbe = `import std.proc

FailA(): never {
    Panic("a failed")
}

FailB(): never {
    Panic("b failed")
}

Done(): never {
    proc.Exit(0)
}

PickA(x: int): int {
    if x == 7 {
        FailA()
    }
    return x + 1
}

PickB(x: int): int {
    if x == 9 {
        FailB()
    }
    return x + 2
}

PickD(x: int): int {
    if x == 11 {
        Done()
    }
    return x + 3
}

Framed(sys: &System, x: int): WriteError!int {
    r := PickA(x) + PickB(x)
    try sys.Print("framed {r}\n", (r,))
    return r
}

Work(x: int) {
    _ = PickA(x)
}

Deferred(sys: &System, x: int): WriteError!int {
    defer { _ = PickA(x) }
    try sys.Print("body\n", ())
    return x
}

export Main(sys: &System): WriteError!void {
    n := sys.args.Len()
    var s := PickA(n) + PickB(n) + PickD(n)
    if n > 1 {
        a := sys.args[1]
        if a == "a" { s += PickA(7) }
        if a == "b" { s += PickB(9) }
        if a == "done" { s += PickD(11) }
        if a == "fa" { s += try Framed(sys, 7) }
        if a == "fb" { s += try Framed(sys, 9) }
        if a == "direct" { FailA() }
        if a == "ok" { s += try Framed(sys, 3) }
        if a == "defer" { s += try Deferred(sys, 7) }
        if a == "task" {
            scope g := sys.io.Scope() {
                g.go Work(7)
            }
        }
    }
    try sys.Print("{s}\n", (s,))
}
`

// trapDyn pairs one object's erased Ref with another type's adapter, as
// std/std-dyn_native_test.go's ForgedMix does: std.dyn's Ref.Mut finds
// the other type's key and calls Mismatch, trapped in release.
const trapDyn = `import std.dyn

interface Step {
    Step(mut self, x: u64): u64
}

gen dyn.Of(Step)

struct Add {
    acc: u64
}

impl Step for Add {
    Step(mut self, x: u64): u64 {
        self.acc = self.acc +% x
        return self.acc
    }
}

struct Xor {
    acc: u64
}

impl Step for Xor {
    Step(mut self, x: u64): u64 {
        self.acc = (self.acc ^ x) *% 0x100000001b3
        return self.acc
    }
}

Forge(a: mut Add, x: mut Xor, n: int): u64 {
    good := StepDyn.Of(a)
    var bad := StepDyn(obj: dyn.Ref.Of(x), table: good.table)
    var s: u64 = 0
    for i in 0..n {
        s = s +% bad.Step(Cast[u64](i))
    }
    return s
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var a := Add(acc: 1)
    var x := Xor(acc: 2)
    try out.Str("start\n")
    k := Forge(mut a, mut x, sys.args.Len())
    try Print(mut out, "unreachable {k}\n", (k: k))
}
`

// trapHook fails a trapped call inside MkPanicHook, during the first
// panic's report: a nested panic, which replays the first report.
const trapHook = `import std.runtime

FailA(): never {
    Panic("a failed")
}

PickA(x: int): int {
    if x == 7 {
        FailA()
    }
    return x + 1
}

export extern MkPanicHook(info: &runtime.PanicInfo) {
    _ = PickA(7)
}

export Main(sys: &System): WriteError!void {
    _ = PickA(sys.args.Len())
    if sys.args.Len() > 0 {
        Panic("first")
    }
}
`

// trapAsm is a package whose unsafe module k runs body (an illegal
// instruction by asm) when Boom's argument is positive, and which has a
// trapped call that does not fail (PickA), so the handler is live and must
// decline the instruction.
func trapAsm(body string) map[string]string {
	return map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"trapasm"`, "unsafe_modules": `["k"]`,
			"bins": `[pkg.Bin(name: "trapasm", root: ".", link_script: "", cpu: "", subsystem: "console")]`}),
		"main.mk": "import k\n\nFailA(): never {\n    Panic(\"a failed\")\n}\n\n" +
			"PickA(x: int): int {\n    if x == 7 {\n        FailA()\n    }\n    return x + 1\n}\n\n" +
			"export Main(sys: &System): WriteError!void {\n    n := PickA(sys.args.Len()) + PickA(sys.args.Len() + 1)\n" +
			"    k.Boom(n)\n    try sys.Print(\"unreachable\\n\", ())\n}\n",
		"k/k.mk": "export Boom(n: int) {\n    if n > 0 {\n        unsafe \"a deliberate illegal instruction\" {\n            asm(\"" + body + "\")\n        }\n    }\n}\n",
	}
}

// trapUd1 is the trapped call's ud1l 0x7f(%eax), %eax, which follows a
// prefetchnta rel32(%rip).
var trapUd1 = []byte{0x67, 0x0F, 0xB9, 0x40, 0x7F}

// trapSites counts the trapped calls in an executable's bytes, and how
// many of them lack the prefetchnta (0F 18 05) seven bytes before.
func trapSites(exe []byte) (n, bad int) {
	for i := 0; ; {
		j := bytes.Index(exe[i:], trapUd1)
		if j < 0 {
			return n, bad
		}
		at := i + j
		n++
		if at < 7 || exe[at-7] != 0x0F || exe[at-6] != 0x18 || exe[at-5] != 0x05 {
			bad++
		}
		i = at + 1
	}
}

// trapBuild builds root (a .mk file or a package directory) for
// x86_64-windows in prof, in the trapped form or the call form.
func trapBuild(t *testing.T, root string, prof types.Profile, size, call bool, out string) []byte {
	t.Helper()
	cfg := Config{Root: root, Target: types.X86_64Windows, Profile: prof, Size: size, Tools: needTools(t), CacheDir: t.TempDir()}
	cfg.TestHooks.NoTrapForm = call
	_, exe, _ := incBuild(t, cfg, out)
	return exe
}

// trapLines are stderr's lines with addresses masked.
func trapLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "0x") {
			l = "  0x"
		}
		if i := strings.Index(l, " at 0x"); i >= 0 {
			j := strings.Index(l[i+6:], " ")
			if j < 0 {
				j = len(l) - i - 6
			}
			l = l[:i+6] + l[i+6+j:]
		}
		out = append(out, l)
	}
	return out
}

// subsequence reports whether every line of want appears in got, in order.
func subsequence(want, got []string) bool {
	k := 0
	for _, l := range got {
		if k < len(want) && l == want[k] {
			k++
		}
	}
	return k == len(want)
}

func TestTrapForm_SameReport(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("SKIP: x86_64-windows executables run on Windows only")
	}
	type run struct {
		args  []string
		traps bool // the run reaches a trapped call
	}
	file := func(src string) map[string]string { return map[string]string{"main.mk": src} }
	for _, c := range []struct {
		name  string
		files map[string]string
		root  string // "" for the directory, else the file in it
		runs  []run
		sites bool // the trapped build has trapped calls
	}{
		{"probe", file(trapProbe), "main.mk", []run{{nil, false}, {[]string{"a"}, true}, {[]string{"b"}, true},
			{[]string{"done"}, true}, {[]string{"fa"}, true}, {[]string{"fb"}, true}, {[]string{"direct"}, false},
			{[]string{"ok"}, false}, {[]string{"defer"}, true}, {[]string{"task"}, true}}, true},
		{"dyn", file(trapDyn), "main.mk", []run{{nil, true}}, true},
		{"hook", file(trapHook), "main.mk", []run{{nil, true}}, true},
		{"ud2", trapAsm("ud2"), "", []run{{nil, false}}, true},
		{"lone-ud1", trapAsm(".byte 0x67, 0x0f, 0xb9, 0x40, 0x7f"), "", []run{{nil, false}}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := writeTree(t, c.files)
			root := dir
			if c.root != "" {
				root = filepath.Join(dir, c.root)
			}
			out := t.TempDir()
			trapExe := filepath.Join(out, "trap.exe")
			callExe := filepath.Join(out, "call.exe")
			tb := trapBuild(t, root, types.Release, false, false, trapExe)
			trapBuild(t, root, types.Release, false, true, callExe)
			n, bad := trapSites(tb)
			// The lone ud1 is k's own asm, not a trapped call codegen
			// emitted: it has no prefetch before it.
			wantBad := 0
			if c.name == "lone-ud1" {
				wantBad = 1
			}
			if bad != wantBad {
				t.Errorf("%d of %d trapped calls have no prefetchnta before them, want %d", bad, n, wantBad)
			}
			if (n-wantBad > 0) != c.sites {
				t.Errorf("trapped build has %d trapped calls, want some: %v", n-wantBad, c.sites)
			}
			for _, r := range c.runs {
				so, se, code := runExe(t, types.X86_64Windows, trapExe, "", r.args...)
				co, ce, ccode := runExe(t, types.X86_64Windows, callExe, "", r.args...)
				tl, cl := trapLines(se), trapLines(ce)
				if so != co || code != ccode || tl[0] != cl[0] {
					t.Errorf("%v: trapped stdout %q exit %d stderr %q; call form stdout %q exit %d stderr %q", r.args, so, code, se, co, ccode, ce)
					continue
				}
				// The backtrace keeps every frame of the call form's, in
				// order; it may show the never callee's own frame where
				// LLVM had inlined the callee into the call form's cold
				// path, since a trapped call is never inlined.
				if !subsequence(cl, tl) {
					t.Errorf("%v: the trapped backtrace drops frames of the call form's:\n%s\nagainst\n%s", r.args, se, ce)
				}
				t.Logf("%v: exit %d, %q", r.args, code, tl[0])
			}
		})
	}
}

// TestTrapForm_PairsIntact: in release, checked and --profile size, every
// trapped call in the probe is the pair the handler reads, and there are
// some (two different never callees inlined into one function).
func TestTrapForm_PairsIntact(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.mk": trapProbe})
	for _, c := range []struct {
		name string
		prof types.Profile
		size bool
	}{{"release", types.Release, false}, {"checked", types.Checked, false}, {"size", types.Release, true}} {
		t.Run(c.name, func(t *testing.T) {
			exe := trapBuild(t, filepath.Join(dir, "main.mk"), c.prof, c.size, false, filepath.Join(t.TempDir(), "p.exe"))
			n, bad := trapSites(exe)
			if n < 2 || bad != 0 {
				t.Errorf("%d trapped calls, %d without their prefetchnta; want at least 2, all paired", n, bad)
			}
		})
	}
}

// TestTrapForm_NoTrapNoCost: release hello has no trapped call, so its
// executable is the call form's, byte for byte (no mk_trap_call, and
// TrapResume and the backtrace's step fold away).
func TestTrapForm_NoTrapNoCost(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "tests", "programs", "hello.mk"))
	if err != nil {
		t.Fatal(err)
	}
	a := trapBuild(t, root, types.Release, false, false, filepath.Join(t.TempDir(), "a.exe"))
	b := trapBuild(t, root, types.Release, false, true, filepath.Join(t.TempDir(), "b.exe"))
	if n, _ := trapSites(a); n != 0 {
		t.Errorf("hello has %d trapped calls", n)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("release hello differs from its call form (%d against %d bytes)", len(a), len(b))
	}
}

// TestTrapForm_PackKeys (a Config field that changes the IR must be in the
// pack key, which is computed before codegen runs): flipping
// the setting misses every pack; and a first trapped call in app's pack
// misses std.sys's pack too, whose std.sys.TrapEntry turns from null into
// mk_trap_call's address, which the MK_CACHE_VERIFY build checks.
func TestTrapForm_PackKeys(t *testing.T) {
	tools := needTools(t)
	base := pkFiles()
	dir := writeTree(t, base)
	out := t.TempDir()
	cfg := Config{Root: dir, Target: types.X86_64Windows, Profile: types.Release, Tools: tools,
		CacheDir: filepath.Join(t.TempDir(), "cache")}
	pkBuild(t, cfg, filepath.Join(out, "cold.exe"))
	flip := cfg
	flip.TestHooks.NoTrapForm = true
	if f := pkBuild(t, flip, filepath.Join(out, "flip.exe")); len(f.r.Trace.PacksEmitted) != len(f.events) {
		t.Errorf("NoTrapForm emitted %d of %d packs, want every one", len(f.r.Trace.PacksEmitted), len(f.events))
	}
	app := base["app/app.mk"]
	app = strings.Replace(app, "import shape\n", "import shape\n\nFail(): never {\n    Panic(\"x\")\n}\n\n"+
		"Pick(x: int): int {\n    if x == 77 {\n        Fail()\n    }\n    return x\n}\n", 1)
	app = strings.Replace(app, "lib.Counter - 26", "lib.Counter + Pick(sys.args.Len()) - Pick(sys.args.Len() + 1) - 25", 1)
	e := pkEdit{name: "first-trap", file: "app/app.mk", old: base["app/app.mk"], new: app}
	pkWrite(t, dir, e.apply(t, base))
	ed := pkBuild(t, cfg, filepath.Join(out, "edit.exe"))
	if got := ed.emittedMods(); !slices.Contains(got, "app") || !slices.Contains(got, "std") {
		t.Errorf("emitted %v, want app (its trapped call) and std (std.sys.TrapEntry)", got)
	}
	if n, bad := trapSites(ed.exe); n == 0 || bad != 0 {
		t.Errorf("edit has %d trapped calls, %d unpaired", n, bad)
	}
	pkVerify(t, cfg, filepath.Join(out, "verify.exe"), ed.exe)
}
