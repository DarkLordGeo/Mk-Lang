// Why: std's I/O test blocks must pass under the real `mk test`, whose
// counting heap fails a block that leaks (status 102).
// What: TestStdIo_CoreTestBlocks over core/tests_rw.mk, core/tests_io.mk,
// fs/tests_fs.mk, time/tests_time.mk, proc/tests_proc.mk and net/tests_net.mk.
// How: `mk test --std=<module>` on the host, dev and release, at 1 and 8
// carriers; TestStdIo_LinuxRuns/MkTest adds --target x86_64-linux, which
// runs each child through wsl.exe (so only under tools/wsl-lock.ps1).

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ioBlockFiles maps each module tested here with blocks to its files.
var ioBlockFiles = []struct {
	module string
	files  []string
}{
	{"std.core", []string{"core/tests_rw.mk", "core/tests_io.mk"}},
	{"std.fs", []string{"fs/tests_fs.mk"}},
	{"std.time", []string{"time/tests_time.mk"}},
	{"std.proc", []string{"proc/tests_proc.mk"}},
	{"std.net", []string{"net/tests_net.mk"}},
}

// ioBlockNames reads the test names of files, so a block added there is
// judged here without an edit.
func ioBlockNames(t *testing.T, files []string) []string {
	t.Helper()
	var names []string
	for _, f := range files {
		b, err := os.ReadFile(filepath.FromSlash(f))
		if err != nil {
			t.Fatal(err)
		}
		ms := regexp.MustCompile(`(?m)^test "([^"]+)"`).FindAllStringSubmatch(string(b), -1)
		if len(ms) == 0 {
			t.Fatalf("%s holds no test block", f)
		}
		for _, m := range ms {
			names = append(names, m[1])
		}
	}
	return names
}

// ioOpenInRelease are blocks that find an open bug elsewhere in release
// builds only: judged in dev, logged in release until it is fixed. Empty:
// the last one (a release miscompile of an empty Str) is fixed and judged
// in release too.
var ioOpenInRelease = map[string]string{}

func TestStdIo_CoreTestBlocks(t *testing.T) { stdIoCoreBlocks(t, hostIo) }

func stdIoCoreBlocks(t *testing.T, h ioHost) {
	root := t.TempDir()
	stdDir := copyStd(t, root, nil)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	type cfg struct {
		flags    []string
		carriers int
	}
	cfgs := []cfg{{nil, 1}, {nil, 8}, {[]string{"--release"}, 1}, {[]string{"--release"}, 8}}
	if h.linux {
		// One wsl.exe per block: dev at 8 carriers only.
		cfgs = []cfg{{[]string{"--target", "x86_64-linux"}, 8}}
	}
	for _, m := range ioBlockFiles {
		names := ioBlockNames(t, m.files)
		// On Linux every block is one wsl.exe, and std.core holds blocks
		// of every topic: run the I/O ones alone, by name (-run is a
		// substring), so one mk call stays well inside mkTimeout.
		runs := [][]string{nil}
		if h.linux && m.module == "std.core" {
			runs = nil
			for _, n := range names {
				runs = append(runs, []string{"-run", n})
			}
		}
		for _, c := range cfgs {
			t.Setenv("MK_CARRIERS", fmt.Sprint(c.carriers))
			var out string
			var status int
			for _, r := range runs {
				args := append(append([]string{"test", "--std=" + m.module}, c.flags...), r...)
				o, st := mkCmd(t, root, stdDir, args...)
				out += o
				if st != 0 {
					status = st
				}
			}
			args := append([]string{"test", "--std=" + m.module}, c.flags...)
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			where := fmt.Sprintf("mk %s, MK_CARRIERS=%d", strings.Join(args, " "), c.carriers)
			release := len(c.flags) > 0 && c.flags[0] == "--release"
			for _, n := range names {
				passed := strings.Contains(out, fmt.Sprintf("PASS %s %q", m.module, n))
				if why, open := ioOpenInRelease[n]; open && release {
					if passed {
						t.Logf("%s: %q passes now: %s is fixed, drop it from ioOpenInRelease", where, n, why)
					} else {
						t.Logf("%s: %q fails, open on %s", where, n, why)
					}
					continue
				}
				if !passed {
					t.Errorf("%s: %q did not pass (status %d)\n%s", where, n, status, out)
				}
			}
			if status != 0 {
				// Another std.core block; the I/O ones are judged above.
				t.Logf("%s: status %d\n%s", where, status, out)
			}
		}
	}
}
