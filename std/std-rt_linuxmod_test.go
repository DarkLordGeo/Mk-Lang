// Why: std.linux exists for x86_64-linux only, and importing it anywhere else
// must fail at the import, not deep in the build.
// What: TestRt_LinuxOnlyOnX64Linux, with `mk check` only (nothing starts WSL).
// How: wants E0202 reason target-selected, naming the target, on every other
// target (aarch64-linux and Nutshell included), and a clean check on x86_64-linux.

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRt_LinuxOnlyOnX64Linux(t *testing.T) {
	root, stdDir := missStd(t)
	pkgSrc := "const Package = pkg.Package(\n    name: \"lx\",\n    bins: [pkg.Bin(name: \"lx\", root: \"app\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [\"app\"],\n    links: [],\n)\n"
	app := "import std.linux\n\nexport Main(sys: &System) {\n    unsafe \"getpid reads nothing\" {\n        v := linux.Syscall(39, 0, 0, 0, 0, 0, 0)\n        _ = v.r\n    }\n}\n"
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg.mk"), []byte(pkgSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "main.mk"), []byte(app), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86_64-linux", "x86_64-windows", "aarch64-linux", "x86_64-nutshell"} {
		t.Run(target, func(t *testing.T) {
			out, status := mkCmd(t, root, stdDir, "check", "-target", target, ".")
			if target == "x86_64-linux" {
				if status != 0 {
					t.Fatalf("mk check: status %d\n%s", status, out)
				}
				return
			}
			if status == 0 || !strings.Contains(out, "main.mk:1:8: E0202") || !strings.Contains(out, "reason: target-selected") ||
				!strings.Contains(out, "exists only on x86_64-linux, not on "+target) {
				t.Errorf("mk check: status %d, want E0202 target-selected at the import naming %s\n%s", status, target, out)
			}
		})
	}
}
