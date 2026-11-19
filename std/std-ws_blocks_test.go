// Why: std.websocket's own test blocks must pass under the real `mk test`,
// with the counting heap, at several carrier counts and on Linux too.
// What: TestStdWs_Blocks and TestStdWs_LinuxBlocks.
// How: each block runs in a child process; Linux runs go through WSL.

// Runs std.websocket's test blocks (websocket/tests_frame.mk, tests_conn.mk,
// tests_sweep.mk and tests_fail.mk: RFC 6455's examples, the frame and
// handshake checks, the connection under std.sim, the sweep of a cancel at
// every await point of Recv, Send, Dial and Accept, and the failed
// connection seen across tasks) under
// the real `mk test`, each block in a child process with the counting
// heap, at 1 and 16 carriers in dev and at 16 in release. On x86_64-linux
// (MK_LINUX=1, under tools/wsl-lock.ps1 with -p 1) at 16 carriers, under
// the default backend and under MK_IO=epoll: the module runs over net.Conn
// and std.sim's network, so it adds no system call of its own.
// Its tests are cancel-safety tests; tools/gate/safety.go lists each.

package std

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// wsBlockFiles are the files whose blocks these tests run.
var wsBlockFiles = []string{"websocket/tests_frame.mk", "websocket/tests_conn.mk", "websocket/tests_accept.mk", "websocket/tests_sweep.mk", "websocket/tests_fail.mk"}

// wsJudge fails t unless every block of wsBlockFiles passed in out.
func wsJudge(t *testing.T, where, out string, status int) {
	t.Helper()
	names := ioBlockNames(t, wsBlockFiles)
	if len(names) < 50 {
		t.Fatalf("only %d blocks found", len(names))
	}
	for _, n := range names {
		if !strings.Contains(out, fmt.Sprintf("PASS std.websocket %q", n)) {
			t.Errorf("%s: %q did not pass (status %d)", where, n, status)
		}
	}
	if t.Failed() {
		t.Logf("%s:\n%s", where, out)
	}
}

func TestStdWs_Blocks(t *testing.T) {
	root := t.TempDir()
	stdDir := copyStd(t, root, nil)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	for _, c := range []struct {
		carriers int
		flags    []string
	}{{1, nil}, {16, nil}, {16, []string{"--release"}}} {
		where := fmt.Sprintf("carriers=%d%s", c.carriers, strings.Join(c.flags, ""))
		t.Run(where, func(t *testing.T) {
			t.Setenv("MK_CARRIERS", fmt.Sprint(c.carriers))
			args := append([]string{"test", "--std=std.websocket"}, c.flags...)
			out, status := mkCmd(t, root, stdDir, args...)
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			wsJudge(t, where, out, status)
		})
	}
}

// TestStdWs_LinuxBlocks runs the same blocks on x86_64-linux, one wsl.exe
// per block, at 16 carriers under io_uring (the default) and epoll.
// MK_LINUX=1 only, under the WSL lock with go test -p 1.
func TestStdWs_LinuxBlocks(t *testing.T) {
	linuxGate(t)
	root := t.TempDir()
	stdDir := copyStd(t, root, nil)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	t.Setenv("MK_CARRIERS", "16")
	t.Setenv("WSLENV", "MK_CARRIERS/u:MK_IO/u")
	for _, backend := range []string{"", "epoll"} {
		t.Run("io="+backend, func(t *testing.T) {
			t.Setenv("MK_IO", backend)
			var out string
			status := 0
			for _, n := range ioBlockNames(t, wsBlockFiles) {
				o, st := mkCmd(t, root, stdDir, "test", "--std=std.websocket", "--target", "x86_64-linux", "-run", n)
				out += o
				if st != 0 {
					status = st
				}
			}
			wsJudge(t, "x86_64-linux io="+backend, out, status)
		})
	}
}
