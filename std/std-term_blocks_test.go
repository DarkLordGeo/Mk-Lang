// Why: std.term's own test blocks must pass at dev and release and under
// both carrier counts, not only in the default run.
// What: TestTerm_Blocks and TestTerm_LinuxBlocks.
// How: runs `mk test --std=std.term` over a copy of std in each mode.

// Runs every test block
// of std.term (std/term/tests_*.mk: the decoder over recorded streams, the
// output sequences byte for byte, and the pseudo-terminal programs) with
// `mk test --std=std.term`, at dev and release, with MK_CARRIERS 1 and 16,
// over a copy of std (MK_TERM_BASE or the tree's) with the tree's
// std/term.
//
// TestTerm_LinuxBlocks starts wsl.exe, so it runs only with MK_LINUX=1,
// under tools/wsl-lock.ps1, with go test -p 1 (parallel WSL clients hang
// WSL).

package std

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// termSummary is mk test's last line.
var termSummary = regexp.MustCompile(`(?m)^(\d+) passed, (\d+) failed`)

// termBlocks runs the blocks with the extra mk test flags and checks that
// at least min passed and none failed.
func termBlocks(t *testing.T, carriers string, flags ...string) {
	t.Helper()
	root := t.TempDir()
	stdDir := termStd(t, root, nil)
	t.Setenv("MK_CARRIERS", carriers)
	t.Setenv("MK_CACHE", root+string(os.PathSeparator)+"cache")
	args := append([]string{"test", "--std=std.term"}, flags...)
	out, status := mkCmd(t, root, stdDir, args...)
	m := termSummary.FindStringSubmatch(out)
	if status != 0 || m == nil || m[2] != "0" {
		t.Fatalf("mk %s (MK_CARRIERS=%s): exit %d\n%s", strings.Join(args, " "), carriers, status, out)
	}
	if m[1] == "0" {
		t.Fatalf("mk %s: no block ran\n%s", strings.Join(args, " "), out)
	}
}

// TestTerm_Blocks runs every std.term block on this host.
func TestTerm_Blocks(t *testing.T) {
	for _, prof := range []string{"dev", "release"} {
		for _, carriers := range []string{"1", "16"} {
			t.Run(prof+"/"+carriers, func(t *testing.T) {
				var flags []string
				if prof == "release" {
					flags = append(flags, "--release")
				}
				termBlocks(t, carriers, flags...)
			})
		}
	}
}

// TestTerm_LinuxBlocks runs every std.term block on x86_64-linux in WSL.
func TestTerm_LinuxBlocks(t *testing.T) {
	if os.Getenv("MK_LINUX") != "1" {
		t.Skip("Linux leaves run only with MK_LINUX=1, under tools/wsl-lock.ps1")
	}
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			flags := []string{"--target", "x86_64-linux"}
			if prof == "release" {
				flags = append(flags, "--release")
			}
			termBlocks(t, "1", flags...)
		})
	}
}
