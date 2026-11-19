// Why: x509.SystemPool loads the store once and must stay repeatable under
// cancellation.
// What: TestX509_SystemPoolBlocks.
// How: runs std/x509/tests_system.mk's SystemPool blocks with mk test
// --std=std.x509 at 1 and 16 carriers, dev and release.
// Its tests are cancel-safety tests; tools/gate/safety.go lists each.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestX509_SystemPoolBlocks(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("x509", "tests_system.mk"))
	if err != nil {
		t.Fatal(err)
	}
	names := regexp.MustCompile(`(?m)^test "([^"]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(names) == 0 {
		t.Fatal("no test block in x509/tests_system.mk")
	}
	for _, c := range []struct {
		carriers int
		flags    []string
	}{{1, nil}, {16, nil}, {16, []string{"--release"}}} {
		t.Run(fmt.Sprintf("carriers=%d%s", c.carriers, strings.Join(c.flags, "")), func(t *testing.T) {
			root := t.TempDir()
			stdDir := copyStd(t, root, nil)
			t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
			t.Setenv("MK_CARRIERS", fmt.Sprint(c.carriers))
			out, status := mkCmd(t, root, stdDir, append([]string{"test", "--std=std.x509", "-run", "SystemPool"}, c.flags...)...)
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			for _, m := range names {
				if !strings.Contains(out, fmt.Sprintf("PASS std.x509 %q", m[1])) {
					t.Errorf("%q did not pass (status %d)", m[1], status)
				}
			}
			if t.Failed() || status != 0 {
				t.Logf("mk test output (status %d):\n%s", status, out)
			}
		})
	}
}
