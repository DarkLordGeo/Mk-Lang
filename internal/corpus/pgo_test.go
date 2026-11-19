// Why: a profile-guided build must change code generation, not only accept
// a profile file.
// What: TestPGOChangesWeights on tests/programs/pgo/skew.mk.
// How: compares the !prof branch weights of Main in a release build with
// and without a merged profile.
//
// A --pgo-gen release build is run, its profile is merged by mk profile
// merge's function (driver.MergeProfiles), and the --pgo build gives the hot
// function, Main, !prof branch weights that the profile-free release build
// lacks (beyond the static cold weights of its overflow checks, which both
// builds carry), while all three executables print skew.out exactly.

package corpus

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"mk/internal/driver"
	"mk/internal/toolchain"
)

// skewProgram is the PGO program, repository-relative.
const skewProgram = ProgramsDir + "/pgo/skew.mk"

// branchWeights matches one branch_weights node with two weights.
var branchWeights = regexp.MustCompile(`!"branch_weights", i32 (\d+), i32 (\d+)\}`)

// pgoBuild is one release build of skew.mk for target with mode and file;
// it returns the executable and every pack's .ll text joined, and skips on
// a build blocked by a std module std does not provide.
func pgoBuild(t *testing.T, tools *toolchain.Tools, target string, mode driver.PGOMode, file string) (string, string) {
	t.Helper()
	tg, _, ok := driver.TargetNamed(target)
	if !ok {
		t.Fatalf("target %q unknown", target)
	}
	var mu sync.Mutex
	var lls []string
	cfg := driver.Config{
		Root: filepath.Join(repoRoot(t), filepath.FromSlash(skewProgram)), CacheDir: t.TempDir(), Tools: tools,
		Target: tg, Profile: driver.ProfileRelease, PGO: mode, PGOFile: file,
		TestHooks: driver.TestHooks{OnLL: func(_, ll string) {
			mu.Lock()
			lls = append(lls, ll)
			mu.Unlock()
		}},
	}
	s, err := driver.NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "skew")
	if strings.HasSuffix(target, "-windows") {
		exe += ".exe"
	}
	res, paths, err := s.Build(exe)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Report.Diags) > 0 || len(paths) != 1 {
		be := &BuildError{Program: skewProgram, Target: target, Profile: "release", Result: res}
		if why := blockedBuild(be); why != "" {
			skipOrFatal(t, gateRun(), "BLOCKED: %s", why)
		}
		t.Fatal(be)
	}
	return paths[0], strings.Join(lls, "\n")
}

// runSkew runs exe for target with MK_PROFILE_DIR=dir (as the runner sees
// the path; through wsl.exe by WSLENV) and wants exactly skew.out on
// stdout, nothing on stderr and exit 0.
func runSkew(t *testing.T, target, exe, dir string, want []byte) {
	t.Helper()
	tg, _, _ := driver.TargetNamed(target)
	prefix, ok := toolchain.Runner(tg.LLVMTriple)
	if !ok {
		t.Fatalf("no runner for %s", target)
	}
	if len(prefix) > 0 {
		wslGuard()
		testWSLSlots <- struct{}{}
		defer func() { <-testWSLSlots }()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	argv := append(append([]string(nil), prefix...), toolchain.HostPathFor(prefix, exe))
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "MK_PROFILE_DIR="+toolchain.HostPathFor(prefix, dir))
	if len(prefix) > 0 {
		wslenv := "MK_PROFILE_DIR"
		if old := os.Getenv("WSLENV"); old != "" {
			wslenv = old + ":" + wslenv
		}
		cmd.Env = append(cmd.Env, "WSLENV="+wslenv)
	}
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\nstdout %q\nstderr %q", exe, err, o.String(), e.String())
	}
	if !bytes.Equal(o.Bytes(), want) || e.Len() != 0 {
		t.Fatalf("%s: stdout %q, stderr %q; want stdout %q (skew.out) and no stderr", exe, o.String(), e.String(), want)
	}
}

// mainDef is the text of the definition of mk.main.Main in ll, through its
// closing brace, or "".
func mainDef(ll string) string {
	for _, def := range strings.Split(ll, "\ndefine ")[1:] {
		at := strings.Index(def, "@")
		end := strings.Index(def, "\n}\n")
		if at >= 0 && end > 0 && strings.HasPrefix(def[at:], "@mk.main.Main(") {
			return def[:end]
		}
	}
	return ""
}

// profBranches counts def's conditional branches that carry !prof.
func profBranches(def string) int {
	n := 0
	for _, line := range strings.Split(def, "\n") {
		if strings.Contains(line, "br i1 ") && strings.Contains(line, "!prof") {
			n++
		}
	}
	return n
}

func TestPGOChangesWeights(t *testing.T) {
	root := repoRoot(t)
	want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ProgramsDir+"/pgo/skew.out")))
	if err != nil {
		t.Fatal(err)
	}
	tools := needLLVM(t)
	for _, target := range []string{"x86_64-windows", "x86_64-linux"} {
		t.Run(target, func(t *testing.T) {
			tg, _, _ := driver.TargetNamed(target)
			if _, ok := toolchain.Runner(tg.LLVMTriple); !ok {
				if gateRun() {
					t.Fatalf("no runner for %s on this host", target)
				}
				t.Skipf("no runner for %s on this host", target)
			}
			needWSLRun(t, target, true)

			// The profile-free release build. Its Main's only !prof are the
			// static cold weights of the overflow checks' branches.
			offExe, offLL := pgoBuild(t, tools, target, driver.PGOOff, "")
			off := mainDef(offLL)
			if off == "" {
				t.Fatal("no definition of mk.main.Main in the profile-free build's packs")
			}
			runSkew(t, target, offExe, t.TempDir(), want)

			// --pgo-gen, run once, merge.
			genExe, _ := pgoBuild(t, tools, target, driver.PGOGen, "")
			profDir := t.TempDir()
			runSkew(t, target, genExe, profDir, want)
			files, err := filepath.Glob(filepath.Join(profDir, "skew-*.mkprof"))
			if err != nil || len(files) != 1 {
				t.Fatalf("profiles written: %q (%v), want one skew-<pid>.mkprof", files, err)
			}
			merged := filepath.Join(t.TempDir(), "merged.mkprof")
			if _, err := driver.MergeProfiles(merged, files); err != nil {
				t.Fatal(err)
			}

			// --pgo: Main's conditional branches gain !prof that the
			// profile-free build lacks, and the if's edges (999 in 1000 to
			// the else) appear as branch weights, in either order.
			useExe, useLL := pgoBuild(t, tools, target, driver.PGOUse, merged)
			use := mainDef(useLL)
			if n, m := profBranches(off), profBranches(use); m <= n {
				t.Errorf("Main's conditional branches with !prof: %d with --pgo, %d without; want more with it\n--pgo Main:\n%s", m, n, use)
			}
			var seen []string
			found := false
			for _, m := range branchWeights.FindAllStringSubmatch(useLL, -1) {
				seen = append(seen, m[1]+"/"+m[2])
				if (m[1] == "999000" && m[2] == "1000") || (m[1] == "1000" && m[2] == "999000") {
					found = true
				}
			}
			if !found {
				t.Errorf("no branch_weights 999000 and 1000 (the if's else and then edges) in the --pgo packs; branch_weights seen: %v\nMain:\n%s", seen, use)
			}
			runSkew(t, target, useExe, t.TempDir(), want)
		})
	}
}
