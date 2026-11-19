// Why: the checked profile must cost what release costs at run time while
// keeping dev's checks; a drift in either direction goes unnoticed.
// What: TestCheckedJobsAreRelease, TestCheckedKeepsChecks and
// TestCheckedOptimizesHotLoop.
// How: compares the checked and release jobs' keys, builds planted programs
// under each profile, and diffs mk.main.Main's x86_64-linux instructions.

package driver

// The checked profile builds as release does and keeps dev's
// checks (contracts, the trace hooks, and code under
// `if build.Profile != .Release`).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"mk/internal/types"
)

// TestCheckedJobsAreRelease: the checked profile compiles every
// pack and links exactly as release does (the same -O level, -flto,
// staging and link options), with only the profile's number in the pack
// key and its name in the link key, so the two never share a pack or link
// entry; its number and name are its own.
func TestCheckedJobsAreRelease(t *testing.T) {
	if types.Checked != 3 || types.Checked.String() != "checked" {
		t.Fatalf("Checked is %d %q, want 3 checked (appended)", uint64(types.Checked), types.Checked)
	}
	rel, chk := profileKeys(types.Release, false), profileKeys(types.Checked, false)
	cut := func(s, num, name string) string {
		s = strings.Replace(s, num+" "+name+" |", "|", 1)
		s = strings.Replace(s, " "+name+" ", " ", 1)
		i, j := strings.Index(s, "packkey "), strings.LastIndex(s, " | ")
		return s[:i] + s[j:]
	}
	if a, b := cut(rel, "2", "release"), cut(chk, "3", "checked"); a != b {
		t.Errorf("checked's jobs differ from release's:\nchecked %s\nrelease %s", b, a)
	}
	pk := func(s string) string { i := strings.Index(s, "packkey "); return s[i : i+72] }
	if pk(rel) == pk(chk) {
		t.Error("checked and release share a pack key")
	}
}

// mkh144Build builds src as a one-file package for target and profile
// with the cache dir shared by the test, keeping symbols on an optimized
// build, and returns the executable's path.
func mkh144Build(t *testing.T, src string, target types.Target, prof types.Profile, cache, name string) string {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Root: file, Target: target, Profile: prof, CacheDir: cache, Symbols: prof.Optimized()}
	cfg.Tools = needTools(t)
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), name+exeSuffix(target)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("%s %s build:\n%s", name, prof, r.Report.Text(r.Sources))
	}
	return paths[0]
}

const mkh144Planted = `import std.build

export Main(sys: &System): WriteError!void {
    if build.Profile != build.ProfileKind.Release {
        Assert(sys.args.Len() == 7)
    }
    try sys.Print("planted assert passed\n", ())
}
`

const mkh144Contract = `Half(n: int): int
    requires n > 0
{
    return n / 2
}

export Main(sys: &System): WriteError!void {
    try sys.Print("half {h}\n", (h: Half(sys.args.Len() - 1),))
}
`

// TestCheckedKeepsChecks: a planted `if build.Profile != .Release {
// Assert(...) }` fires under the checked profile and not under release,
// and a `requires` is checked under checked as under dev and off under
// release. Built checked, release, checked, release on one cache, so a
// warm build of either cannot take the other's packs or link.
func TestCheckedKeepsChecks(t *testing.T) {
	target := HostTarget()
	cache := t.TempDir()
	for i, prof := range []types.Profile{types.Checked, types.Release, types.Checked, types.Release} {
		name := fmt.Sprintf("%s%d", prof, i)
		out, errText, code := runExe(t, target, mkh144Build(t, mkh144Planted, target, prof, cache, "planted-"+name), "")
		if prof == types.Checked {
			if code == 0 || !strings.Contains(errText, "panic: assert at main.mk:5:9") {
				t.Errorf("%s planted: exit %d, stdout %q stderr %q, want the Assert's panic", name, code, out, errText)
			}
		} else if code != 0 || out != "planted assert passed\n" {
			t.Errorf("%s planted: exit %d, stdout %q stderr %q, want it passed", name, code, out, errText)
		}
		out, errText, code = runExe(t, target, mkh144Build(t, mkh144Contract, target, prof, cache, "contract-"+name), "")
		if prof == types.Checked {
			if code == 0 || !strings.Contains(errText, "panic: contract: requires at main.mk:2:5") {
				t.Errorf("%s contract: exit %d, stdout %q stderr %q, want the requires' panic", name, code, out, errText)
			}
		} else if code != 0 || out != "half 0\n" {
			t.Errorf("%s contract: exit %d, stdout %q stderr %q, want half 0 (unchecked)", name, code, out, errText)
		}
	}
	_, errText, code := runExe(t, target, mkh144Build(t, mkh144Contract, target, types.Dev, cache, "contract-dev"), "")
	if code == 0 || !strings.Contains(errText, "panic: contract: requires at main.mk:2:5") {
		t.Errorf("dev contract: exit %d, stderr %q, want the requires' panic", code, errText)
	}
}

const mkh144Loop = `Sum(n: int): int {
    var s := 0
    for i in 0..n {
        s = s ^ (i * 3)
    }
    return s
}

export Main(sys: &System) {
    if Sum(sys.args.Len() * 1000) == 12345 {
        Panic("unlikely")
    }
}
`

// mkh144Main is the instructions of mk.main.Main in an x86_64-linux
// executable, one per line, with addresses and LTO's symbol suffixes cut.
func mkh144Main(t *testing.T, path string) string {
	t.Helper()
	tools := needTools(t)
	exe := "llvm-objdump"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	objdump := filepath.Join(filepath.Dir(tools.Clang), exe)
	if _, err := os.Stat(objdump); err != nil {
		t.Skipf("no %s next to clang: the disassembly check is not run", objdump)
	}
	out, err := exec.Command(objdump, "-d", "--no-show-raw-insn", "--disassemble-symbols=mk.main.Main", path).CombinedOutput()
	if err != nil {
		t.Fatalf("llvm-objdump: %v\n%s", err, out)
	}
	addr := regexp.MustCompile(`\.llvm\.[0-9]+|0x[0-9a-f]+|\b[0-9a-f]{6,}\b`)
	var b strings.Builder
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.SplitN(line, "\t", 2)
		if len(f) != 2 || !strings.HasSuffix(strings.TrimSpace(f[0]), ":") {
			continue
		}
		ins := f[1]
		if i := strings.Index(ins, "#"); i >= 0 {
			ins = ins[:i]
		}
		if ins = strings.TrimSpace(ins); ins == "int3" {
			continue
		}
		b.WriteString(addr.ReplaceAllString(ins, "N") + "\n")
	}
	if b.Len() == 0 {
		t.Fatalf("llvm-objdump found no mk.main.Main in %s:\n%s", path, out)
	}
	return b.String()
}

// TestCheckedOptimizesHotLoop: on x86_64-linux, whose symbols name
// each function, the checked build's Main is release's instruction for
// instruction: Sum inlined and its loop vectorized (paddq), where dev
// calls Sum. The planted Assert's PanicAt is in checked's Main and absent
// from release's.
func TestCheckedOptimizesHotLoop(t *testing.T) {
	cache := t.TempDir()
	lt := types.X86_64Linux
	rel := mkh144Main(t, mkh144Build(t, mkh144Loop, lt, types.Release, cache, "loop-release"))
	chk := mkh144Main(t, mkh144Build(t, mkh144Loop, lt, types.Checked, cache, "loop-checked"))
	dev := mkh144Main(t, mkh144Build(t, mkh144Loop, lt, types.Dev, cache, "loop-dev"))
	if chk != rel {
		t.Errorf("checked Main differs from release's:\nchecked:\n%s\nrelease:\n%s", chk, rel)
	}
	if !strings.Contains(chk, "paddq") || strings.Contains(chk, "mk.main.Sum") {
		t.Errorf("checked Main does not inline Sum and vectorize its loop:\n%s", chk)
	}
	if !strings.Contains(dev, "<mk.main.Sum>") {
		t.Errorf("dev Main does not call Sum, so the check cannot tell -O0 from -O3:\n%s", dev)
	}
	pc := mkh144Main(t, mkh144Build(t, mkh144Planted, lt, types.Checked, cache, "planted-checked"))
	pr := mkh144Main(t, mkh144Build(t, mkh144Planted, lt, types.Release, cache, "planted-release"))
	if !strings.Contains(pc, "<mk.std.rt.PanicAt>") {
		t.Errorf("checked planted Main has no PanicAt (the Assert):\n%s", pc)
	}
	if strings.Contains(pr, "<mk.std.rt.PanicAt>") {
		t.Errorf("release planted Main holds a PanicAt (the Assert):\n%s", pr)
	}
}
