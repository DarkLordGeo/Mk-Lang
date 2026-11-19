// Why: switching and removing installed toolchains must work from the command
// line without touching the user's real MK_HOME.
// What: TestToolchainCommands and TestUpgradeNeedsAnAddress.
// How: mk toolchain list, use and remove run on an MK_HOME under the test's
// temporary directory; mk upgrade without a release address is refused. The
// upgrade itself is internal/driver's TestUpgradeBetweenReleases.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestToolchainCommands and TestUpgradeNeedsAnAddress: mk toolchain list,
// use and remove on an MK_HOME under the test's temporary directory, and mk
// upgrade's refusal without a release address. The upgrade itself is
// internal/driver's TestUpgradeBetweenReleases.

func TestToolchainCommands(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MK_HOME", root)
	exe := "mk"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	for _, v := range []string{"0.1.0", "0.2.0"} {
		bin := filepath.Join(root, "toolchains", v, "bin")
		os.MkdirAll(bin, 0o755)
		os.WriteFile(filepath.Join(bin, exe), []byte("mk "+v), 0o755)
	}
	if st, out, errOut := mk("toolchain", "use", "0.1.0"); st != 0 || !strings.Contains(out, "0.1.0") {
		t.Fatalf("use: %d %q %q", st, out, errOut)
	}
	if st, out, _ := mk("toolchain", "list"); st != 0 || out != "* 0.1.0\n  0.2.0\n" {
		t.Errorf("list: %d %q", st, out)
	}
	if st, _, errOut := mk("toolchain", "remove", "0.1.0"); st != 2 || !strings.Contains(errOut, "current") {
		t.Errorf("remove current: %d %q", st, errOut)
	}
	if st, _, _ := mk("toolchain", "use", "0.2.0"); st != 0 {
		t.Error("use 0.2.0")
	}
	if st, out, errOut := mk("toolchain", "remove", "0.1.0"); st != 0 || !strings.Contains(out, "removed") {
		t.Errorf("remove: %d %q %q", st, out, errOut)
	}
	if st, out, _ := mk("toolchain", "list"); out != "* 0.2.0\n" || st != 0 {
		t.Errorf("list after remove: %q", out)
	}
	for _, bad := range [][]string{{"toolchain"}, {"toolchain", "frob"}, {"toolchain", "use"}, {"toolchain", "list", "x"}} {
		if st, _, _ := mk(bad...); st != 2 {
			t.Errorf("%v: status %d, want 2", bad, st)
		}
	}
}

func TestUpgradeNeedsAnAddress(t *testing.T) {
	t.Setenv("MK_HOME", t.TempDir())
	t.Setenv("MK_RELEASES", "")
	st, _, errOut := mk("upgrade")
	if st != 2 || !strings.Contains(errOut, "MK_RELEASES") || !strings.Contains(errOut, "nothing was changed") {
		t.Errorf("upgrade with no address: %d %q", st, errOut)
	}
	if st, _, _ := mk("upgrade", "0.1.0", "0.2.0"); st != 2 {
		t.Error("two versions accepted")
	}
	if st, _, errOut := mk("upgrade", "0.1"); st != 2 || !strings.Contains(errOut, "bad version") {
		t.Errorf("bad version: %d %q", st, errOut)
	}
}
