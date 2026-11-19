// Why: core/tests_sync.mk's test blocks and the channel lock's handoff path
// need checks run through mk test's runner and under contention.
// What: TestSTDSYNC_CoreTestBlocks (and its Linux twin) and
// TestSTDSYNC_ChanLockHandoffRuns.
// How: runs the blocks with mk test, one child at a time; the Linux test
// starts wsl.exe, so run it only under tools/wsl-lock.ps1 with go test -p 1.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// syncExpectTag is the std harness's expected-panic tag at the end of a
// block's name, as internal/stdtest's stdExpect.
var syncExpectTag = regexp.MustCompile(`\[exit ([0-9]+): ([^\]]*)\]$`)

// syncCoreTestNames are the names of core/tests_sync.mk's test blocks, read
// from the file, so a block added there is checked here without an edit.
func syncCoreTestNames(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash("core/tests_sync.mk"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range regexp.MustCompile(`(?m)^test "([^"]+)"`).FindAllStringSubmatch(string(b), -1) {
		names = append(names, m[1])
	}
	if len(names) == 0 {
		t.Fatal("core/tests_sync.mk holds no test block")
	}
	return names
}

// TestSTDSYNC_CoreTestBlocks runs core/tests_sync.mk with `mk test
// --std=std.core`, dev and release, at 1 carrier and at 8 carriers over
// seeds 1 to syncSeeds() (MK_TEST_SEED: the runtime's jitter points). Each
// of that file's blocks must PASS (other std.core blocks are only logged).
// Each block runs in a child process with the counting heap, so an unfreed
// channel fails it. MK_STDSYNC_SRC lays the sync files over another std
// tree, as buildSync does.
func TestSTDSYNC_CoreTestBlocks(t *testing.T) {
	syncCoreBlocks(t, nil, []string{""})
}

// TestSTDSYNC_CoreTestBlocksLinux is the same on x86_64-linux in WSL:
// `mk test --std=std.core --target x86_64-linux`, whose children run
// through wsl.exe one at a time, under the default I/O backend and under
// MK_IO=epoll, which must give the same verdicts (the epoll fallback's
// parity).
func TestSTDSYNC_CoreTestBlocksLinux(t *testing.T) {
	syncNeedWSL(t)
	syncCoreBlocks(t, []string{"--target", "x86_64-linux"}, []string{"", "epoll"})
}

// syncCoreBlocks runs core/tests_sync.mk's blocks with the target flags,
// under each I/O backend of ios ("" the default, "epoll" MK_IO=epoll).
func syncCoreBlocks(t *testing.T, target, ios []string) {
	names := syncCoreTestNames(t)
	root := t.TempDir()
	stdDir := copyStd(t, root, nil)
	if alt := os.Getenv("MK_STDSYNC_SRC"); alt != "" {
		stdDir = filepath.Join(root, "alt", "std")
		if err := os.CopyFS(stdDir, os.DirFS(alt)); err != nil {
			t.Fatal(err)
		}
		copyLaneFiles(t, stdDir)
		b, err := os.ReadFile(filepath.FromSlash("core/tests_sync.mk"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stdDir, "core", "tests_sync.mk"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	// Passed into WSL for the Linux children (mk test's runner is wsl.exe).
	t.Setenv("WSLENV", "MK_CARRIERS:MK_TEST_SEED:MK_IO")
	type run struct{ carriers, seed int }
	runs := []run{{1, 1}}
	for seed := 1; seed <= syncSeeds(); seed++ {
		runs = append(runs, run{8, seed})
	}
	for _, io := range ios {
		t.Setenv("MK_IO", io)
		for _, flags := range [][]string{nil, {"--release"}} {
			for _, r := range runs {
				t.Setenv("MK_CARRIERS", fmt.Sprint(r.carriers))
				t.Setenv("MK_TEST_SEED", fmt.Sprint(r.seed))
				args := append(append([]string{"test", "--std=std.core"}, target...), flags...)
				out, status := mkCmd(t, root, stdDir, args...)
				if strings.Contains(out, "no usable LLVM") {
					t.Skipf("needs the LLVM toolchain: %s", out)
				}
				where := fmt.Sprintf("mk %s, MK_IO=%s, MK_CARRIERS=%d, MK_TEST_SEED=%d", strings.Join(args, " "), io, r.carriers, r.seed)
				for _, n := range names {
					// A name ending in "[exit N: text]" must panic with exit N
					// and text in its report (internal/stdtest's stdExpect).
					if m := syncExpectTag.FindStringSubmatch(n); m != nil {
						line := fmt.Sprintf("FAIL std.core %q: panic (exit %s)", n, m[1])
						i := strings.Index(out, line)
						if i < 0 || !strings.Contains(out[i:], "panic: "+m[2]) {
							t.Errorf("%s: %q did not panic with exit %s and %q (status %d)\n%s", where, n, m[1], m[2], status, out)
						}
						continue
					}
					if !strings.Contains(out, fmt.Sprintf("PASS std.core %q", n)) {
						t.Errorf("%s: %q did not pass (status %d)\n%s", where, n, status, out)
					}
				}
				if status != 0 {
					t.Logf("%s: status %d (a block outside core/tests_sync.mk failed?)\n%s", where, status, out)
				}
			}
		}
	}
}

// TestSTDSYNC_ChanLockHandoffRuns: a coverage check. In a
// scratch copy, the clockless starvation branch of MutexLockSlow, which
// only the channel's lock takes (Mutex.Lock passes clock true), becomes a
// panic; the capacity-64 and capacity-0 8 x 8 programs at 8 carriers,
// release, must reach it. It proves the wake-counted handoff runs under
// contention; that the handoff bounds a wait is argued in sync_mutex.mk, as
// fairness of the channel's lock is not visible through Chan's API.
func TestSTDSYNC_ChanLockHandoffRuns(t *testing.T) {
	t.Parallel()
	const old = "            MWSetStarving(c, wakes >= MutexStarveWakes)\n"
	const probe = "            if wakes >= MutexStarveWakes {\n                raw.Fail(99, \"probe: clockless starving\")\n            }\n"
	for _, p := range syncBenchContended {
		if !strings.HasPrefix(p.name, "Chan") {
			continue
		}
		exe := buildSyncPatched(t, p.src, func(stdDir string) {
			f := filepath.Join(stdDir, "core", "sync_mutex.mk")
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			s := strings.ReplaceAll(string(b), "\r\n", "\n")
			if !strings.Contains(s, old) {
				t.Fatal("the clockless starvation branch is not in sync_mutex.mk")
			}
			if err := os.WriteFile(f, []byte(strings.Replace(s, old, probe, 1)), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "--release")
		hits, runs := 0, 0
		for seed := 1; seed <= 20 && hits == 0; seed++ {
			r, hung := runSyncEnv(t, exe, syncTimeout, syncStressEnv(8, seed))
			if hung {
				t.Fatalf("%s seed %d: no exit within %v", p.name, seed, syncTimeout)
			}
			runs++
			if r.code == 101 && strings.Contains(r.stderr, "probe: clockless starving") {
				hits++
			} else if r.code != 0 {
				t.Fatalf("%s seed %d: exit %d\nstderr:\n%s", p.name, seed, r.code, r.stderr)
			}
		}
		if hits == 0 {
			t.Errorf("%s: the channel lock's handoff branch was not reached in %d runs", p.name, runs)
		}
		t.Logf("%s: handoff branch reached at run %d", p.name, runs)
	}
}
