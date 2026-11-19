// Why: std.http's own test blocks (http/tests_http.mk) must pass under the real
// `mk test`, not only in isolation.
// What: TestHttp_Blocks and TestHttp_LinuxBlocks.
// How: dev and release, 1 and 8 carriers, the counting heap; on Linux with io_uring
// and the epoll fallback, only with MK_LINUX=1 under tools/wsl-lock.ps1 and go test -p 1.

package std

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestHttp_Blocks(t *testing.T) { htBlocks(t, hostIo) }

func TestHttp_LinuxBlocks(t *testing.T) {
	stdioNeedWSL(t)
	htBlocks(t, linuxIo)
}

func htBlocks(t *testing.T, h ioHost) {
	names := ioBlockNames(t, []string{"http/tests_http.mk"})
	root := t.TempDir()
	stdDir := htStd(t, root, nil)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	type cfg struct {
		flags    []string
		carriers int
		io       string
	}
	cfgs := []cfg{{nil, 1, ""}, {nil, 8, ""}, {[]string{"--release"}, 1, ""}, {[]string{"--release"}, 8, ""}}
	if h.linux {
		lx := []string{"--target", "x86_64-linux"}
		cfgs = []cfg{{lx, 8, ""}, {lx, 1, "epoll"}, {append([]string{"--release"}, lx...), 1, ""}, {append([]string{"--release"}, lx...), 8, "epoll"}}
		// mk's child environment keeps WSLENV, so these two reach the Linux
		// children.
		t.Setenv("WSLENV", "MK_CARRIERS/u:MK_IO/u")
	}
	for _, c := range cfgs {
		t.Setenv("MK_CARRIERS", fmt.Sprint(c.carriers))
		t.Setenv("MK_IO", c.io)
		args := append([]string{"test", "--std=std.http"}, c.flags...)
		out, status := mkCmd(t, root, stdDir, args...)
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		where := fmt.Sprintf("mk %s, MK_CARRIERS=%d, MK_IO=%q", strings.Join(args, " "), c.carriers, c.io)
		for _, n := range names {
			if !strings.Contains(out, fmt.Sprintf("PASS std.http %q", n)) {
				t.Errorf("%s: %q did not pass (status %d)\n%s", where, n, status, out)
			}
		}
		if status != 0 {
			t.Errorf("%s: status %d\n%s", where, status, out)
		}
	}
}
