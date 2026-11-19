// Why: a lost lock or a double wake in Conn or the stream echo shows only
// under scheduler jitter, at many seeds and carrier counts.
// What: TestStdNet_StressSeeds and TestStdNet_LinuxStressSeeds.
// How: MK_TEST_SEED 1 to net3Seeds() at 2, 8 and 16 carriers; overlaps, wrong
// totals and heap counts fail; Linux runs io_uring and MK_IO=epoll.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// net3StressBlock and net3EchoBlock are the blocks the matrix runs.
const (
	net3StressBlock = "A Conn over an in-memory transport serialises its Reads and its WriteAlls across 16 tasks and frees its box"
	net3EchoBlock   = "One task reads a stream's Conn while another writes it, through an echo"
)

// net3StressBlocks is the matrix's block list.
var net3StressBlocks = []string{net3StressBlock, net3EchoBlock}

// net3Seeds is how many seeds a run sweeps: MK_NET3_SEEDS, else 8.
func net3Seeds() int {
	if n, err := strconv.Atoi(os.Getenv("MK_NET3_SEEDS")); err == nil && n > 0 {
		return n
	}
	return 8
}

// net3StressRun runs one block of the matrix with extra mk test flags and
// fails t unless it passed.
func net3StressRun(t *testing.T, root, stdDir, where, block string, flags ...string) {
	t.Helper()
	args := append([]string{"test", "--std=std.net", "-run", block}, flags...)
	out, status := mkCmd(t, root, stdDir, args...)
	if strings.Contains(out, "no usable LLVM") {
		t.Skipf("needs the LLVM toolchain: %s", out)
	}
	if status != 0 || !strings.Contains(out, fmt.Sprintf("PASS std.net %q", block)) {
		t.Errorf("%s %q: the block did not pass (status %d):\n%s", where, block, status, out)
	}
}

func TestStdNet_StressSeeds(t *testing.T) {
	if testing.Short() {
		t.Skip("the stress matrix runs without -short")
	}
	root := t.TempDir()
	stdDir := copyStd(t, root, nil)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	for _, flags := range [][]string{nil, {"--release"}} {
		for _, carriers := range []int{2, 8, 16} {
			for seed := 1; seed <= net3Seeds(); seed++ {
				where := fmt.Sprintf("carriers=%d seed=%d %s", carriers, seed, strings.Join(flags, ""))
				t.Setenv("MK_CARRIERS", fmt.Sprint(carriers))
				t.Setenv("MK_TEST_SEED", fmt.Sprint(seed))
				for _, block := range net3StressBlocks {
					net3StressRun(t, root, stdDir, where, block, flags...)
				}
			}
		}
	}
}

// TestStdNet_LinuxStressSeeds is the same matrix on
// x86_64-linux in dev, under io_uring (the default) and MK_IO=epoll, one
// wsl.exe per block and seed. MK_LINUX=1 only, under the WSL lock with go
// test -p 1.
func TestStdNet_LinuxStressSeeds(t *testing.T) {
	linuxGate(t)
	if testing.Short() {
		t.Skip("the stress matrix runs without -short")
	}
	root := t.TempDir()
	stdDir := copyStd(t, root, nil)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	// MK_TEST_SEED must cross into WSL, or every seed runs unarmed.
	t.Setenv("WSLENV", "MK_CARRIERS/u:MK_IO/u:MK_TEST_SEED/u")
	for _, backend := range []string{"", "epoll"} {
		t.Setenv("MK_IO", backend)
		for _, carriers := range []int{2, 8, 16} {
			for seed := 1; seed <= net3Seeds(); seed++ {
				where := fmt.Sprintf("x86_64-linux io=%s carriers=%d seed=%d", backend, carriers, seed)
				t.Setenv("MK_CARRIERS", fmt.Sprint(carriers))
				t.Setenv("MK_TEST_SEED", fmt.Sprint(seed))
				for _, block := range net3StressBlocks {
					net3StressRun(t, root, stdDir, where, block, "--target", "x86_64-linux")
				}
			}
		}
	}
}
