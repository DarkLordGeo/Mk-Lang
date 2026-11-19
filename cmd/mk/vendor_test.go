// Why: a vendored dependency lets a package build offline, from its own tree.
// What: TestVendorCommand.
// How: mk vendor copies a path dependency into vendor/<canonical id>; with the
// source removed, mk check --offline and mk build --offline still succeed.
// Hermetic: a temporary workspace and cache, no url dependency, no network.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestVendorCommand: mk vendor copies a path dependency into
// vendor/<canonical id> and prints "vendor/<canonical id>  <source>"; with
// the dependency's source removed, mk check --offline and mk build --offline
// succeed from the vendored copy. Hermetic: a temporary workspace and cache,
// no url dependency, no network.
func TestVendorCommand(t *testing.T) {
	t.Setenv("MK_CACHE", t.TempDir())
	ws := t.TempDir()
	manifest := func(name, bins, deps string) string {
		return "const Package = pkg.Package(\n    name: \"" + name + "\",\n    bins: " + bins + ",\n    deps: " + deps +
			",\n    c_sources: [],\n    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n"
	}
	files := map[string]string{
		"app/pkg.mk": manifest("app", `[pkg.Bin(name: "app", root: "src", link_script: "", cpu: "", subsystem: "console")]`,
			`[pkg.Dep(name: "util", path: "../util", url: "", hash: "")]`),
		"app/src/main.mk": "import util.calc\n\nexport Main(sys: &System): int {\n    return calc.Answer()\n}\n",
		"util/pkg.mk":     manifest("util", "[]", "[]"),
		// A dependency imports its own module by its local path.
		"util/calc/calc.mk": "import base\n\nexport Answer(): int {\n    return base.Forty() + 2\n}\n",
		"util/base/base.mk": "export Forty(): int {\n    return 40\n}\n",
	}
	for name, text := range files {
		p := filepath.Join(ws, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app := filepath.Join(ws, "app")

	// util's canonical id: its manifest name and 8 hex of SHA-256 over its
	// directory relative to the root (see doc/packages.md).
	sum := sha256.Sum256([]byte("../util"))
	cid := "util_" + hex.EncodeToString(sum[:4])
	st, out, errOut := mk("vendor", app)
	if st != 0 || out != "vendor/"+cid+"  ../util\n" {
		t.Fatalf("mk vendor: status %d stdout %q stderr %q", st, out, errOut)
	}
	for _, f := range []string{"pkg.mk", "calc/calc.mk", "base/base.mk"} {
		got, err := os.ReadFile(filepath.Join(app, "vendor", cid, filepath.FromSlash(f)))
		if err != nil || string(got) != files["util/"+f] {
			t.Errorf("vendor/%s/%s: %q, %v", cid, f, got, err)
		}
	}

	if err := os.RemoveAll(filepath.Join(ws, "util")); err != nil {
		t.Fatal(err)
	}
	if st, out, errOut := mk("check", "--offline", app); st != 0 {
		t.Fatalf("mk check --offline from vendor/: status %d stdout %q stderr %q", st, out, errOut)
	}

	if _, err := os.Stat(`C:\Program Files\LLVM\bin\clang.exe`); err != nil && os.Getenv("MK_LLVM") == "" {
		if _, err := exec.LookPath("clang"); err != nil {
			t.Skip("no clang: vendored check passed, build not run")
		}
	}
	exe := filepath.Join(t.TempDir(), "app")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	st, out, errOut = mk("build", "--offline", "-o", exe, app)
	if st != 0 {
		t.Fatalf("mk build --offline from vendor/: status %d stdout %q stderr %q", st, out, errOut)
	}
	err := exec.Command(exe).Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 42 {
		t.Errorf("vendored build ran with %v, want exit status 42", err)
	}
	// Without vendor/ and without the source, the build fails at the Dep.
	if err := os.RemoveAll(filepath.Join(app, "vendor")); err != nil {
		t.Fatal(err)
	}
	if st, _, errOut := mk("build", "--offline", "-o", exe, app); st != 1 || !strings.Contains(errOut, "E0202") {
		t.Errorf("build with neither source nor vendor/: status %d stderr %q", st, errOut)
	}
}
