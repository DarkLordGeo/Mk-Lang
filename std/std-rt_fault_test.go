// Why: a task's 16 KiB stack guarantee (std/task/spawn.mk's TaskGuarantee)
// must still hold the stack-overflow report when a fault filter runs first.
// What: TestRt_FilterInGuarantee, Windows only (Linux runs the filter on the
// carrier's 64 KiB alternate signal stack).
// How: a task overflows its 1 MiB stack under a runtime.AddFaultFilter filter
// that uses 0 or 8 KiB; wants stack_task.mk's report and exit 101, dev and release.
//
// A sweep on x86_64-windows found that 12 KiB works and 14 KiB loses the
// report in a dev build.

package std

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRt_FilterInGuarantee(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the guarantee area is Windows'; Linux's filter runs on the alternate signal stack")
	}
	root, stdDir := missStd(t)
	for _, kb := range []int{0, 8} {
		n := kb*1024 + 8
		dir := filepath.Join(root, fmt.Sprintf("k%d", kb))
		if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
			t.Fatal(err)
		}
		man := "const Package = pkg.Package(\n    name: \"so\",\n    bins: [pkg.Bin(name: \"so\", root: \"app\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [\"app\"],\n    links: [],\n)\n"
		src := "import std.runtime\n\nDeep(n: int): int {\n    if n < 0 {\n        return 0\n    }\n    var pad: [256]u8 = [0; 256]\n    pad[n % 256] = Trunc[u8](n)\n    r := Deep(n + 1)\n    return r + int(pad[(n + 1) % 256])\n}\n\n" +
			"Run(n: int, out: mut int) {\n    *out = Deep(n)\n}\n\n" +
			fmt.Sprintf("Pass(c: &runtime.FaultContext): runtime.FaultAction {\n    var buf: [%d]u8 = [0; %d]\n    buf[c.Sp() %% %d] = 1\n    var s := 0\n    for i in 0..%d {\n        s += int(buf[i])\n    }\n    if s == 7 {\n        return runtime.FaultAction.Resume(ctx: *c)\n    }\n    return runtime.FaultAction.Pass\n}\n\n", n, n, n, n) +
			"export Main(sys: &System): int {\n    unsafe \"AddFaultFilter is unsafe only\" {\n        _ = runtime.AddFaultFilter(Pass)\n    }\n    var r := 0\n    scope s := sys.io.Scope() {\n        s.go Run(sys.args.Len(), mut r)\n    }\n    return r\n}\n"
		if err := os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(man), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "app", "main.mk"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, prof := range []string{"dev", "release"} {
			t.Run(fmt.Sprintf("%dKiB/%s", kb, prof), func(t *testing.T) {
				exe := filepath.Join(dir, "so-"+prof+".exe")
				args := []string{"build", "-o", exe, "."}
				if prof == "release" {
					args = []string{"build", "-release", "-o", exe, "."}
				}
				if out, status := mkCmd(t, dir, stdDir, args...); status != 0 {
					if strings.Contains(out, "no usable LLVM") {
						t.Skipf("needs the LLVM toolchain: %s", out)
					}
					t.Fatalf("mk build: status %d\n%s", status, out)
				}
				cmd := exec.Command(exe)
				b, err := cmd.CombinedOutput()
				status := 0
				if ee, ok := err.(*exec.ExitError); ok {
					status = ee.ExitCode()
				} else if err != nil {
					t.Fatal(err)
				}
				out := strings.ReplaceAll(string(b), "\r\n", "\n")
				if status != 101 || !strings.Contains(out, "panic: stack limit in task 2 at app/main.mk:3:1") {
					t.Errorf("status %d, want 101 and the stack report\n%s", status, out)
				}
			})
		}
	}
}
