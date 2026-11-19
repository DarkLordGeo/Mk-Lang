// Why: the pinned-carrier, fault and allocator tests each build a small user
// package and run it without hanging the test binary.
// What: rt10bBuildPkg, rt10bBuildIn and rt10bRun.
// How: writes a package with one module app (listed in unsafe_modules), builds
// it under a fresh std copy, and runs the executable under a watchdog.

package std

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// rt10bBuildPkg writes a package name whose module app is src, under a
// fresh std copy, and builds it in profile (dev or release); a missing
// toolchain skips the test.
func rt10bBuildPkg(t *testing.T, name, src, profile string) string {
	t.Helper()
	root, stdDir := missStd(t)
	return rt10bBuildIn(t, root, stdDir, name, src, profile)
}

// rt10bBuildIn is rt10bBuildPkg in root with the std copy stdDir.
func rt10bBuildIn(t *testing.T, root, stdDir, name, src, profile string) string {
	t.Helper()
	dir := filepath.Join(root, name+"-"+profile)
	man := strings.Join([]string{
		"const Package = pkg.Package(",
		`    name: "` + name + `",`,
		`    bins: [pkg.Bin(name: "` + name + `", root: "app", link_script: "", cpu: "", subsystem: "console")],`,
		"    deps: [],",
		"    c_sources: [],",
		"    c_imports: [],",
		"    asm_sources: [],",
		"    targets: [],",
		`    unsafe_modules: ["app"],`,
		"    links: [],",
		")",
		"",
	}, "\n")
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"build", "-o", exe, "."}
	if profile == "release" {
		args = []string{"build", "-release", "-o", exe, "."}
	}
	out, status := mkCmd(t, dir, stdDir, args...)
	if strings.Contains(out, "no usable LLVM") {
		t.Skipf("needs the LLVM toolchain: %s", out)
	}
	if status != 0 {
		t.Fatalf("mk build: status %d\n%s", status, out)
	}
	return exe
}

// rt10bRun runs exe with env added, under a 120 s watchdog, and returns
// its combined output and exit status.
func rt10bRun(t *testing.T, exe string, env []string, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = append(os.Environ(), env...)
	b, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s: killed after 120 s\n%s", exe, b)
	}
	status := 0
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n"), status
}
