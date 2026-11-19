// Why: under a trained profile LLVM may emit a 128-bit division, and the
// link must find __udivti3 in the support pack.
// What: TestSievePGOLinks, with runWithArgs and sievePGOBuild.
// How: tests/bench/same/sieve built with a profile trained on itself, as
// tools/bench's trainPGO does it (a --pgo-gen build at --cpu x86-64-v2,
// one run at the quick arguments 2000000 2 with MK_PROFILE_DIR, the
// profile merged, a --pgo build), links and prints its checksum. Under
// that profile LLVM's loop unroll expands the inner `for j <= n { j += i }`
// loop's trip count as an i128 udiv (sieve.mk:24), which needs __udivti3.
// The test exercises the routines only while LLVM keeps making that
// division; internal/codegen's TestTi* pin the routines themselves.

package driver

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mk/internal/toolchain"
	"mk/internal/types"
)

// runWithArgs runs exe through the target's runner prefix with args and,
// when dir is not "", MK_PROFILE_DIR=dir (through WSLENV on wsl.exe), and
// returns its stdout; it wants status 0 and no stderr.
func runWithArgs(t *testing.T, prefix []string, exe, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	argv := append(append([]string(nil), prefix...), toolchain.HostPathFor(prefix, exe))
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = os.Environ()
	if dir != "" {
		cmd.Env = append(cmd.Env, "MK_PROFILE_DIR="+toolchain.HostPathFor(prefix, dir))
		if len(prefix) > 0 {
			wslenv := "MK_PROFILE_DIR"
			if old := os.Getenv("WSLENV"); old != "" {
				wslenv = old + ":" + wslenv
			}
			cmd.Env = append(cmd.Env, "WSLENV="+wslenv)
		}
	}
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil || e.Len() != 0 {
		t.Fatalf("%q: %v\nstdout: %s\nstderr: %s", argv, err, o.String(), e.String())
	}
	return o.String()
}

// sievePGOBuild is one release build of the sieve package at root with
// mode and file, at --cpu x86-64-v2, into out.
func sievePGOBuild(t *testing.T, target types.Target, root string, mode PGOMode, file, out string) {
	t.Helper()
	cfg := Config{Root: root, CacheDir: t.TempDir(), Target: target, Profile: types.Release, CPU: "x86-64-v2",
		PGO: mode, PGOFile: file, Tools: needTools(t)}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		skipKnownBlockers(t, r)
		t.Fatalf("build (pgo mode %v): %d paths\n%s", mode, len(paths), r.Report.Text(r.Sources))
	}
}

// TestSievePGOLinks: on x86_64-windows natively and on x86_64-linux
// through the runner (MK_LINUX=1).
func TestSievePGOLinks(t *testing.T) {
	src := filepath.Join("..", "..", "tests", "bench", "same", "sieve", "mk")
	files := map[string]string{}
	for _, name := range []string{"pkg.mk", "sieve.mk"} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(b)
	}
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			prefix := runner(t, target)
			root := writeTree(t, files)
			gen := filepath.Join(t.TempDir(), "gen"+exeSuffix(target))
			sievePGOBuild(t, target, root, PGOGen, "", gen)
			profDir := t.TempDir()
			if out := runWithArgs(t, prefix, gen, profDir, "2000000", "2"); out != "297866\n" {
				t.Fatalf("the --pgo-gen build printed %q, want 297866", out)
			}
			raw, err := filepath.Glob(filepath.Join(profDir, "*.mkprof"))
			if err != nil || len(raw) != 1 {
				t.Fatalf("profiles written: %q (%v), want one", raw, err)
			}
			prof := filepath.Join(t.TempDir(), "sieve.mkprof")
			if _, err := MergeProfiles(prof, raw); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(t.TempDir(), "sieve"+exeSuffix(target))
			sievePGOBuild(t, target, root, PGOUse, prof, exe)
			for _, args := range [][]string{{"2000000", "2"}, {"100", "1"}, {"1", "3"}} {
				want := map[string]string{"2000000": "297866\n", "100": "25\n", "1": "0\n"}[args[0]]
				if out := runWithArgs(t, prefix, exe, "", args...); out != want {
					t.Errorf("sieve %s printed %q, want %q", strings.Join(args, " "), out, want)
				}
			}
		})
	}
}
