// Why: Spin, RwLock, Select, Rng, Dir and Path keep their invariants in Mk
// test blocks, which must pass under every build and carrier count.
// What: TestSTDMISS_Blocks, TestSTDMISS_SyncStress and their Linux twins.
// How: mk test --std over missBlocks, dev and release, with 1 and 8
// carriers; the Linux tests run only with MK_LINUX=1 under the WSL lock.

package std

import (
	"fmt"
	"strings"
	"testing"
)

// missBlocks are the modules and the name filter of these tests' blocks.
var missBlocks = []struct{ mod, run string }{
	{"std.atomic", ""},
	{"std.sync", ""},
	{"std.math", ""},
	{"std.fs", "Dir: "},
	{"std.core", "Path"},
}

func TestSTDMISS_Blocks(t *testing.T) {
	root, stdDir := missStd(t)
	for _, profile := range []string{"dev", "release"} {
		for _, b := range missBlocks {
			for _, n := range []int{1, 8} {
				t.Run(fmt.Sprintf("%s/%s/carriers=%d", profile, b.mod, n), func(t *testing.T) {
					t.Setenv("MK_CARRIERS", fmt.Sprint(n))
					args := []string{"test", "--std=" + b.mod}
					if b.run != "" {
						args = append(args, "-run", b.run)
					}
					if profile == "release" {
						args = append(args, "-release")
					}
					out, status := mkCmd(t, root, stdDir, args...)
					if strings.Contains(out, "no usable LLVM") {
						t.Skipf("needs the LLVM toolchain: %s", out)
					}
					if status != 0 || !strings.Contains(out, " 0 failed") || strings.Contains(out, "\n0 passed") {
						t.Errorf("mk %s: status %d\n%s", strings.Join(args, " "), status, out)
					}
				})
			}
		}
	}
}

// TestSTDMISS_SyncStress reruns the RwLock, Select and Spin blocks, which
// race tasks on carriers, at 2, 4 and 16 carriers, 5 rounds each, release
// (the schedule differs from run to run; a lost wakeup shows as the
// child's 60 s timeout).
func TestSTDMISS_SyncStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress")
	}
	root, stdDir := missStd(t)
	for _, mod := range []string{"std.sync", "std.atomic"} {
		for _, n := range []int{2, 4, 16} {
			for round := range 5 {
				t.Setenv("MK_CARRIERS", fmt.Sprint(n))
				args := []string{"test", "--std=" + mod, "-run", "Conc-", "-release"}
				out, status := mkCmd(t, root, stdDir, args...)
				if strings.Contains(out, "no usable LLVM") {
					t.Skipf("needs the LLVM toolchain: %s", out)
				}
				if status != 0 || !strings.Contains(out, " 0 failed") {
					t.Errorf("%s carriers=%d round %d: status %d\n%s", mod, n, round, status, out)
				}
			}
		}
	}
}

// TestSTDMISS_LinuxBlocks runs the same blocks on x86_64-linux through
// wsl.exe: only with MK_LINUX=1, under tools/wsl-lock.ps1, go test -p 1.
// fs.Dir and Stat reach rt-io2's Linux readdir and statx, Path's rules
// change with the target, and the sync blocks run on both Linux backends.
func TestSTDMISS_LinuxBlocks(t *testing.T) {
	stdioNeedWSL(t)
	root, stdDir := missStd(t)
	t.Setenv("WSLENV", "MK_CARRIERS/u:MK_IO/u")
	for _, b := range missBlocks {
		for _, c := range []struct {
			release  bool
			carriers int
			io       string
		}{{false, 8, ""}, {true, 1, ""}, {true, 8, "epoll"}} {
			t.Setenv("MK_CARRIERS", fmt.Sprint(c.carriers))
			t.Setenv("MK_IO", c.io)
			args := []string{"test", "--std=" + b.mod, "--target", "x86_64-linux"}
			if b.run != "" {
				args = append(args, "-run", b.run)
			}
			if c.release {
				args = append(args, "-release")
			}
			out, status := mkCmd(t, root, stdDir, args...)
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			if status != 0 || !strings.Contains(out, " 0 failed") || strings.Contains(out, "\n0 passed") {
				t.Errorf("mk %s, MK_CARRIERS=%d, MK_IO=%q: status %d\n%s", strings.Join(args, " "), c.carriers, c.io, status, out)
			}
		}
	}
}

// TestSTDMISS_LinuxSyncStress is SyncStress on x86_64-linux, on both
// backends (so stress does not run on Windows only): the sync and
// atomic blocks at 2 and 16 carriers, 3 rounds each, release. Only with
// MK_LINUX=1, under tools/wsl-lock.ps1, go test -p 1.
func TestSTDMISS_LinuxSyncStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress")
	}
	stdioNeedWSL(t)
	root, stdDir := missStd(t)
	t.Setenv("WSLENV", "MK_CARRIERS/u:MK_IO/u")
	for _, mod := range []string{"std.sync", "std.atomic"} {
		for _, io := range []string{"", "epoll"} {
			for _, n := range []int{2, 16} {
				for round := range 3 {
					t.Setenv("MK_CARRIERS", fmt.Sprint(n))
					t.Setenv("MK_IO", io)
					args := []string{"test", "--std=" + mod, "-run", "Conc-", "-release", "--target", "x86_64-linux"}
					out, status := mkCmd(t, root, stdDir, args...)
					if strings.Contains(out, "no usable LLVM") {
						t.Skipf("needs the LLVM toolchain: %s", out)
					}
					if status != 0 || !strings.Contains(out, " 0 failed") {
						t.Errorf("%s MK_IO=%q carriers=%d round %d: status %d\n%s", mod, io, n, round, status, out)
					}
				}
			}
		}
	}
}
