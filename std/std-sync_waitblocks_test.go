// Why: sync.Semaphore, Notify, Barrier and Once lose wakeups only under rare
// interleavings, so their blocks must run across carriers and seeds.
// What: the TestSTDSYNC_ tests over std/sync/tests_waits.mk: blocks, stress, panics,
// atomic edge comments, compile-time order checks and the Linux runs.
// How: mk test --std, dev and release, 1 and 8 carriers, MK_TEST_SEED jitter
// seeds; Linux only with MK_LINUX=1 under tools/wsl-lock.ps1, -p 1.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sync2Files are the wait primitives' .mk files with atomic sites.
var sync2Files = []string{"sync/mem_wait.mk", "sync/semaphore.mk", "sync/notify.mk", "sync/barrier.mk", "sync/once.mk", "atomic/order.mk"}

// sync2BlockCount is the number of Wait- test blocks in tests_waits.mk,
// so a run that skips one fails.
func sync2BlockCount(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash("sync/tests_waits.mk"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\ntest \"Wait-")
}

// sync2Seeds is the jitter seeds a block run uses: MK_STDSYNC_SEEDS, default 8.
func sync2Seeds() int { return syncSeeds() }

func sync2Test(t *testing.T, root, stdDir string, extra ...string) {
	t.Helper()
	args := append([]string{"test", "--std=std.sync", "-run", "Wait-"}, extra...)
	out, status := mkCmd(t, root, stdDir, args...)
	if strings.Contains(out, "no usable LLVM") {
		t.Skipf("needs the LLVM toolchain: %s", out)
	}
	if status != 0 || !strings.Contains(out, " 0 failed") || !strings.Contains(out, fmt.Sprintf("%d passed", sync2BlockCount(t))) {
		t.Errorf("mk %s, MK_CARRIERS=%s, MK_TEST_SEED=%s: status %d\n%s", strings.Join(args, " "), os.Getenv("MK_CARRIERS"), os.Getenv("MK_TEST_SEED"), status, out)
	}
}

// TestSTDSYNC_Blocks: every Wait- block, dev and release, 1 and 8
// carriers, jitter seeds 1 to sync2Seeds() (each seed changes where the
// runtime's jitter points yield, std/task/jitter.mk).
func TestSTDSYNC_Blocks(t *testing.T) {
	root, stdDir := missStd(t)
	for _, profile := range []string{"dev", "release"} {
		for _, n := range syncCarriers {
			t.Run(fmt.Sprintf("%s/carriers=%d", profile, n), func(t *testing.T) {
				t.Setenv("MK_CARRIERS", fmt.Sprint(n))
				for seed := 1; seed <= sync2Seeds(); seed++ {
					t.Setenv("MK_TEST_SEED", fmt.Sprint(seed))
					if profile == "release" {
						sync2Test(t, root, stdDir, "-release")
					} else {
						sync2Test(t, root, stdDir)
					}
				}
			})
		}
	}
}

// TestSTDSYNC_Stress: the blocks again at 2, 4 and 16 carriers, release,
// more seeds (a lost wakeup shows as the child's 60 s timeout).
func TestSTDSYNC_Stress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress")
	}
	root, stdDir := missStd(t)
	for _, n := range []int{2, 4, 16} {
		t.Setenv("MK_CARRIERS", fmt.Sprint(n))
		for seed := 101; seed <= 100+2*sync2Seeds(); seed++ {
			t.Setenv("MK_TEST_SEED", fmt.Sprint(seed))
			sync2Test(t, root, stdDir, "-release")
		}
	}
}

// TestSTDSYNC_Panics: the argument checks panic with their code and text.
func TestSTDSYNC_Panics(t *testing.T) {
	cases := []struct{ name, body, msg string }{
		{"SemNewNegative", "_ = sync.Semaphore.New(-1)", "Semaphore.New: permits below 0"},
		{"SemNewTooMany", "_ = sync.Semaphore.New(0x1000_0000_0000_0000)", "Semaphore.New: permits below 0"},
		{"SemAcquireNegative", "s := sync.Semaphore.New(1)\n    s.Acquire(sys.io, -1) else {}", "Semaphore.Acquire: n below 0"},
		{"SemTryNegative", "s := sync.Semaphore.New(1)\n    _ = s.TryAcquire(-1)", "Semaphore.TryAcquire: n below 0"},
		{"SemReleaseNegative", "s := sync.Semaphore.New(1)\n    s.Release(-1)", "Semaphore.Release: n below 0"},
		{"SemReleaseOverflow", "s := sync.Semaphore.New(0x0FFF_FFFF_FFFF_FFFF)\n    s.Release(1)", "Semaphore.Release: more than 2^60 - 1 permits"},
		{"BarrierNewZero", "_ = sync.Barrier.New(0)", "Barrier.New: n below 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := "import std.sync\n\nexport Main(sys: &System) {\n    " + c.body + "\n}\n"
			exe := buildSync(t, src)
			wantPanic(t, exe, c.msg)
		})
	}
}

// TestSTDSYNC_WaitsEveryAtomicNamesItsEdge: over sync2Files, every atomic operation has, in its function's doc comment
// or a comment above it in the body, words naming the edge it creates (or
// saying it orders nothing), as TestSTDSYNC_EveryAtomicNamesItsEdge checks std-sync's files.
func TestSTDSYNC_WaitsEveryAtomicNamesItsEdge(t *testing.T) {
	for _, f := range sync2Files {
		b, err := os.ReadFile(filepath.FromSlash(f))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		for i, line := range lines {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "//") || strings.HasPrefix(code, "export ") || !syncAtomicSites.MatchString(code) {
				continue
			}
			start := i
			for start > 0 && !(len(lines[start]) > 0 && lines[start][0] != ' ' && lines[start][0] != '}' && lines[start][0] != '/' && lines[start][0] != '\t') {
				start--
			}
			for start > 0 && strings.HasPrefix(lines[start-1], "//") {
				start--
			}
			ok := false
			for j := start; j < i; j++ {
				if c := strings.TrimSpace(lines[j]); strings.HasPrefix(c, "//") && syncEdgeWords.MatchString(c) {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("%s:%d: %s: no comment names the ordering edge of this atomic operation", f, i+1, code)
			}
		}
	}
}

// TestSTDSYNC_LinuxBlocks: the blocks on x86_64-linux, io_uring and
// epoll, dev at 8 carriers and release at 1 and 8, two seeds each. Only
// with MK_LINUX=1, under tools/wsl-lock.ps1, go test -p 1.
func TestSTDSYNC_LinuxBlocks(t *testing.T) {
	stdioNeedWSL(t)
	root, stdDir := missStd(t)
	t.Setenv("WSLENV", "MK_CARRIERS/u:MK_IO/u:MK_TEST_SEED/u")
	for _, c := range []struct {
		release  bool
		carriers int
		io       string
	}{{false, 8, ""}, {true, 1, ""}, {true, 8, ""}, {true, 8, "epoll"}, {true, 16, "epoll"}} {
		t.Setenv("MK_CARRIERS", fmt.Sprint(c.carriers))
		t.Setenv("MK_IO", c.io)
		for seed := 1; seed <= 2; seed++ {
			t.Setenv("MK_TEST_SEED", fmt.Sprint(seed))
			extra := []string{"--target", "x86_64-linux"}
			if c.release {
				extra = append(extra, "-release")
			}
			sync2Test(t, root, stdDir, extra...)
		}
	}
}

// TestSTDSYNC_OrderRejectedAtCompileTime: an order that does not
// apply is E0706 at the call, through Fence's compile-time requires, and
// the orders that apply check clean. The clause is the
// exported method `o.CanFence()`, which a wrapper can restate.
func TestSTDSYNC_OrderRejectedAtCompileTime(t *testing.T) {
	root, stdDir := missStd(t)
	write := func(name, body string) {
		src := "import std.atomic\n\nexport Main(sys: &System) {\n" + body + "}\n"
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("good.mk", "    atomic.Fence(atomic.Acquire)\n    atomic.Fence(atomic.Release)\n    atomic.Fence(atomic.AcqRel)\n    atomic.Fence(atomic.SeqCst)\n")
	write("bad.mk", "    atomic.Fence(atomic.Relaxed)\n")
	if out, status := mkCmd(t, root, stdDir, "check", "good.mk"); status != 0 {
		t.Errorf("mk check good.mk: status %d\n%s", status, out)
	}
	out, status := mkCmd(t, root, stdDir, "check", "bad.mk")
	if status == 0 || !strings.Contains(out, "bad.mk:4:5: E0706") || !strings.Contains(out, "clause: o.CanFence()") {
		t.Errorf("mk check bad.mk: status %d, want E0706 at 4:5 naming the clause\n%s", status, out)
	}
}

// TestSTDSYNC_LinuxStress: the Linux twin of TestSTDSYNC_Stress, release,
// io_uring and epoll, at 2, 4 and 16 carriers, jitter seeds 101 to 104
// (a lost wakeup shows as the child's timeout). Only with MK_LINUX=1,
// under tools/wsl-lock.ps1, go test -p 1.
func TestSTDSYNC_LinuxStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress")
	}
	stdioNeedWSL(t)
	root, stdDir := missStd(t)
	t.Setenv("WSLENV", "MK_CARRIERS/u:MK_IO/u:MK_TEST_SEED/u")
	for _, io := range []string{"", "epoll"} {
		for _, n := range []int{2, 4, 16} {
			t.Setenv("MK_IO", io)
			t.Setenv("MK_CARRIERS", fmt.Sprint(n))
			for seed := 101; seed <= 104; seed++ {
				t.Setenv("MK_TEST_SEED", fmt.Sprint(seed))
				sync2Test(t, root, stdDir, "-release", "--target", "x86_64-linux")
			}
		}
	}
}
