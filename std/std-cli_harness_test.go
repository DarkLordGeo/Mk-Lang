// Why: the std.cli and std.log tests build many programs in parallel and
// must not share state.
// What: the harness: a copy of std and an mk cache per test, and helpers
// to check, build and run programs over them.
// How: a private cache because a warm cache drops a std generator's literal
// bindings; the copy is the tree's std unchanged.

package std

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// cliPkg is the manifest of the test package: one bin whose root is ".".
const cliPkg = "const Package = pkg.Package(\n    name: \"p\",\n    bins: [pkg.Bin(name: \"p\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n" +
	"    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n"

// cliRoot is one scratch directory: a std copy, a cache, and the package
// directory pkg where main.mk and pkg.mk go.
type cliRoot struct {
	pkg, std, cache string
}

// newCliRoot copies std and writes main.mk as src.
func newCliRoot(t *testing.T, src string) *cliRoot {
	t.Helper()
	root := t.TempDir()
	r := &cliRoot{pkg: filepath.Join(root, "p"), std: copyStd(t, root, nil), cache: filepath.Join(root, "cache")}
	for name, text := range map[string]string{"main.mk": src, "pkg.mk": cliPkg} {
		if err := os.MkdirAll(r.pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(r.pkg, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// mk runs mk with args in the package directory, MK_STD and MK_CACHE set
// for this root alone, and returns its combined output and exit status.
func (r *cliRoot) mk(t *testing.T, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), mkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, mkExe(t), args...)
	cmd.Dir = r.pkg
	cmd.Env = append(os.Environ(), "MK_STD="+r.std, "MK_CACHE="+r.cache)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("mk %s: killed after %s\n%s", strings.Join(args, " "), mkTimeout, out.String())
	}
	status := 0
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(out.String(), "\r\n", "\n"), status
}

// cliCheck runs mk check of src as the package's main.mk.
func cliCheck(t *testing.T, src string) (string, int) {
	t.Helper()
	return newCliRoot(t, src).mk(t, "check", "--target=x86_64-windows", ".")
}

// cliBuild builds src with extra flags and returns the executable; a
// missing LLVM toolchain skips the test.
func cliBuild(t *testing.T, src string, flags ...string) string {
	t.Helper()
	r := newCliRoot(t, src)
	exe := filepath.Join(filepath.Dir(r.pkg), "p")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, ".")
	out, status := r.mk(t, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// cliView checks src and returns the gen view without its comment lines
// (the header, the trace lines and the digest).
func cliView(t *testing.T, src string) string {
	t.Helper()
	r := newCliRoot(t, src)
	out, status := r.mk(t, "check", "--target=x86_64-windows", ".")
	if status != 0 {
		t.Fatalf("mk check: exit %d\n%s", status, out)
	}
	b, err := os.ReadFile(filepath.Join(r.pkg, "gen", "x86_64-windows", "gen.mk"))
	if err != nil {
		t.Fatal(err)
	}
	var keep []string
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(l, "//") {
			keep = append(keep, l)
		}
	}
	return strings.TrimSpace(strings.Join(keep, "\n")) + "\n"
}

// cliModes are the two build profiles every native test runs in.
var cliModes = []struct {
	name  string
	flags []string
}{{"dev", nil}, {"release", []string{"--release"}}}

// cliGolden compares got with std/<rel> (rewritten when MK_CLI_UPDATE=1).
func cliGolden(t *testing.T, rel, got string) {
	t.Helper()
	path := filepath.FromSlash(rel)
	if os.Getenv("MK_CLI_UPDATE") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (MK_CLI_UPDATE=1 writes it)", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("output differs from %s (MK_CLI_UPDATE=1 rewrites it after reading the diff):\n%s", rel, got)
	}
}
