// Why: a target std/pkg.mk selects no system layer for (aarch64-macos)
// must be refused once, before std is checked, and not with an E0202 from
// every std file that imports std.sys.
// What: TestStdTargetMissing: mk check and mk build for aarch64-macos give
// one E0202 reason no-std-target naming the targets mk builds for and,
// apart, those it only checks; a copy of
// std whose pkg.mk selects a layer for macos is not refused.
// How: check and Build over a small program, and over MK_STD's copy.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

func TestStdTargetMissing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(nutshellProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	one := func(what string, r Result) {
		t.Helper()
		text := r.Report.Text(r.Sources)
		if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0202 ||
			!strings.Contains(text, "reason: no-std-target") || !strings.Contains(text, "mk builds for x86_64-linux, x86_64-windows, x86_64-freestanding, x86_64-nutshell, and checks only for aarch64-linux, aarch64-nutshell") {
			t.Errorf("%s --target aarch64-macos: want one E0202 reason no-std-target naming the targets that build and those only checked, got\n%s", what, text)
		}
	}
	one("mk check", check(t, Config{Root: file, Target: types.Aarch64Macos}))
	s, err := NewSession(Config{Root: file, Target: types.Aarch64Macos, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "main"))
	if err != nil || len(paths) != 0 {
		t.Errorf("mk build: %v, %v", err, paths)
	}
	one("mk build", r)

	b, err := os.ReadFile(filepath.Join("..", "..", "std", "pkg.mk"))
	if err != nil {
		t.Fatal(err)
	}
	m := strings.ReplaceAll(string(b), "\r\n", "\n")
	linux := `pkg.Select(os: "linux", modules: [pkg.Module(name: "sys", dir: "sys/linux"), pkg.Module(name: "linux", dir: "linux/linux")]),`
	if !strings.Contains(m, linux) {
		t.Fatalf("std/pkg.mk has no line %q", linux)
	}
	macos := `pkg.Select(os: "macos", modules: [pkg.Module(name: "sys", dir: "sys/linux"), pkg.Module(name: "linux", dir: "linux/other")]),`
	t.Setenv("MK_STD", realStdWith(t, map[string]string{"pkg.mk": strings.Replace(m, linux, linux+"\n        "+macos, 1)}))
	r = check(t, Config{Root: file, Target: types.Aarch64Macos})
	if text := r.Report.Text(r.Sources); strings.Contains(text, "no-std-target") {
		t.Errorf("a std that selects a layer for macos is refused:\n%s", text)
	}
}
