// Why: a float % lowers to a call of std.math.Rem64 or Rem32, which the
// program need not import, so a build that does not root them fails.
// What: TestFloatRemainderBuilds.
// How: builds a program that uses float % without importing std.math and
// checks it builds and runs.

package driver

import (
	"os"
	"path/filepath"
	"testing"

	"mk/internal/types"
)

// TestFloatRemainderBuilds: a float % calls std.math.Rem64 or Rem32,
// which the program need not import, so both are runtime roots of every
// build. Without them the build is the ICE "missing runtime entry
// std.math.Rem64".
func TestFloatRemainderBuilds(t *testing.T) {
	src := "export Main(sys: &System): int {\n" +
		"    a: f64 = 7.5\n" +
		"    b: f64 = 2.0\n" +
		"    x: f32 = -7.5\n" +
		"    y: f32 = 2.0\n" +
		"    var r := 0\n" +
		"    if a % b == 1.5 {\n" +
		"        r += 1\n" +
		"    }\n" +
		"    if x % y == -1.5 {\n" +
		"        r += 2\n" +
		"    }\n" +
		"    return r\n" +
		"}\n"
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "frem.mk")
			if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			exe := buildProgram(t, file, target, types.Dev, dir)
			out, errOut, code := runExe(t, target, exe, "")
			if code != 3 || out != "" {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 3", code, out, errOut)
			}
		})
	}
}
